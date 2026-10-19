package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/httperr"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
	textgenruntime "github.com/lazu-ai/lazu/internal/protocol/textgen/runtime"
	"github.com/lazu-ai/lazu/internal/service"
	"github.com/lazu-ai/lazu/internal/types"
	"gorm.io/gorm"
)

// A new route is watched for model.RouteObservationDays() before it may carry
// traffic. The hourly liveness probe already samples it like any other route;
// this file adds the three things a liveness probe cannot see — an upstream
// that bills for a prompt it injected, one that cuts long inputs, and one that
// drops tools — and decides when a watched route has earned going live.
//
// The bars a route is judged by are deployment settings, read from
// common.AppConfig.SourceChecks (source_checks in config.yaml):
//
//   - observation_days: how long the watch lasts.
//   - check_every_hours: how often each check runs on a watched route.
//   - min_samples: the least liveness record that can back a success-rate
//     verdict; below it the rate is still pending.
//   - min_success_rate: the share of liveness probes that must succeed.
//   - max_billing_ratio: how far billed input may exceed what was sent.
//     Message framing costs a few tokens on every provider; an injected
//     system prompt costs far more.
//   - billing_framing_tokens: an absolute excess that passes whatever the
//     ratio says. A chat template with a default system line is a fixed cost
//     that can read as a noticeable ratio on a short prompt; an injected
//     system prompt is many times larger.
//   - long_context_tokens: the long-context check's input.
//   - long_context_min_share: how much of that input the upstream must bill.
//     Tokenizers differ somewhat; a cut context bills a fraction.
const (
	// observationRetryAfter spaces out a check whose last run never reached
	// the upstream, so a local fault does not retry every sweep.
	observationRetryAfter = time.Hour
	// observationMaxChecksPerSweep bounds one sweep. A long-context check is
	// tens of thousands of tokens; a batch of new sources must not spend the
	// day's probe budget in one pass.
	observationMaxChecksPerSweep = 6

	// observationLongContextOutputTokens caps the long-context reply. The
	// answer is a dozen tokens, but models that think by default (GLM, Qwen)
	// spend a small cap on reasoning and return no answer at all.
	observationLongContextOutputTokens = 1024
	// observationBillingBaseTokens and observationBillingStepTokens size the
	// billing check's two prompts; see runObservationBillingCheck.
	observationBillingBaseTokens = 1_000
	observationBillingStepTokens = 1_000
)

// observationFillerParagraph is neutral prose, long enough that repeating it
// does not look like a single repeated token to a tokenizer.
const observationFillerParagraph = "The harbour town kept its lighthouse lit through every winter storm, " +
	"and the keeper wrote the weather in a ledger each evening before supper. " +
	"Fishing boats left before dawn and returned by noon with herring, cod and the occasional crab. " +
	"In spring the hills behind the town turned green, and children walked the cliff path to school. " +
	"The market opened on Saturdays, selling bread, wool, candles and tools mended by the smith. " +
	"Visitors came in summer for the long evenings, and the inn kept a fire going even in July.\n"

// observationFiller returns roughly `tokens` tokens of prose, as the local
// tokenizer counts them for this model.
func observationFiller(tokens int, modelName string) string {
	if tokens <= 0 {
		return ""
	}
	per := service.CountTextToken(observationFillerParagraph, modelName)
	if per <= 0 {
		per = len(observationFillerParagraph) / 4
	}
	return strings.Repeat(observationFillerParagraph, tokens/per+1)
}

// observationNonce makes each prompt unique, so no upstream cache can turn
// one run's input into a cheaper cached read on the next.
func observationNonce() string {
	return strings.ToUpper(strings.ReplaceAll(uuid.NewString(), "-", "")[:10])
}

var observationToolParameters = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"city": map[string]any{"type": "string", "description": "City name, e.g. Paris"},
	},
	"required": []string{"city"},
}

const (
	observationToolName        = "get_weather"
	observationToolDescription = "Returns the current weather for a city."
	observationToolPrompt      = "What is the weather in Paris right now? Call the get_weather tool to find out."
)

// observationRequest builds one check request in the shape the route's
// endpoint speaks. Checks are not streamed: the reply is read whole, and a
// route that cannot answer a plain request is a finding in itself.
func observationRequest(modelName, endpointType, prompt string, maxTokens uint, withTool bool) dto.Request {
	if constant.EndpointType(endpointType) == constant.EndpointTypeGemini && maxTokens < 1024 {
		// Gemini thinks by default and spends small caps before answering.
		maxTokens = 1024
	}
	effort := probeReasoningEffort(modelName)
	if effort != "" && maxTokens < probeReasoningCap {
		maxTokens = probeReasoningCap
	}
	if constant.EndpointType(endpointType) == constant.EndpointTypeOpenAIResponse {
		input, _ := json.Marshal(prompt)
		request := &dto.OpenAIResponsesRequest{Model: modelName, Input: input, MaxOutputTokens: maxTokens}
		if effort != "" {
			request.Reasoning = &dto.Reasoning{Effort: effort}
		}
		if withTool {
			tools, _ := json.Marshal([]map[string]any{{
				"type": "function", "name": observationToolName,
				"description": observationToolDescription, "parameters": observationToolParameters,
			}})
			request.Tools = tools
		}
		return request
	}
	request := &dto.GeneralOpenAIRequest{
		Model:    modelName,
		Messages: []dto.Message{{Role: "user", Content: prompt}},
	}
	if effort != "" {
		request.ReasoningEffort = effort
	}
	// Mirror buildTestRequest: o-series take the newer field, everything else
	// the legacy one, and never both.
	if strings.HasPrefix(strings.ToLower(modelName), "o") {
		request.MaxCompletionTokens = maxTokens
	} else {
		request.MaxTokens = maxTokens
	}
	if withTool {
		request.Tools = []dto.ToolCallRequest{{
			Type: "function",
			Function: dto.FunctionRequest{
				Name: observationToolName, Description: observationToolDescription,
				Parameters: observationToolParameters,
			},
		}}
		request.ToolChoice = "auto"
	}
	return request
}

// observedToolCall is one function call found in a reply.
type observedToolCall struct {
	Name      string
	Arguments string
}

// parseObservationReply reads the text and tool calls out of a reply in any
// of the shapes a test can come back in: OpenAI chat, Anthropic messages,
// Responses, or Gemini.
func parseObservationReply(body []byte) (string, []observedToolCall) {
	var reply struct {
		Choices []struct {
			Message struct {
				Content   any `json:"content"`
				ToolCalls []struct {
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
		Content []struct {
			Type  string          `json:"type"`
			Text  string          `json:"text"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
		Output []struct {
			Type      string `json:"type"`
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
			Content   []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text         string `json:"text"`
					FunctionCall *struct {
						Name string          `json:"name"`
						Args json.RawMessage `json:"args"`
					} `json:"functionCall"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(body, &reply); err != nil {
		return "", nil
	}
	var text strings.Builder
	var calls []observedToolCall
	for _, choice := range reply.Choices {
		switch content := choice.Message.Content.(type) {
		case string:
			text.WriteString(content)
		case []any:
			for _, part := range content {
				if typed, ok := part.(map[string]any); ok {
					if value, ok := typed["text"].(string); ok {
						text.WriteString(value)
					}
				}
			}
		}
		for _, call := range choice.Message.ToolCalls {
			calls = append(calls, observedToolCall{Name: call.Function.Name, Arguments: call.Function.Arguments})
		}
	}
	for _, block := range reply.Content {
		switch block.Type {
		case "text":
			text.WriteString(block.Text)
		case "tool_use":
			calls = append(calls, observedToolCall{Name: block.Name, Arguments: string(block.Input)})
		}
	}
	for _, item := range reply.Output {
		switch item.Type {
		case "message":
			for _, part := range item.Content {
				text.WriteString(part.Text)
			}
		case "function_call":
			calls = append(calls, observedToolCall{Name: item.Name, Arguments: item.Arguments})
		}
	}
	for _, candidate := range reply.Candidates {
		for _, part := range candidate.Content.Parts {
			text.WriteString(part.Text)
			if part.FunctionCall != nil {
				calls = append(calls, observedToolCall{Name: part.FunctionCall.Name, Arguments: string(part.FunctionCall.Args)})
			}
		}
	}
	return text.String(), calls
}

// observationTarget is a watched route resolved to what a check needs.
type observationTarget struct {
	cell         model.RouteObservationCell
	channel      *model.Channel
	endpointType string
}

// observationNotApplicable reports why a route cannot be asked `kind`, or ""
// when it can. Routes that do not produce text are asked nothing: a billing
// ratio or a tool call means nothing for an embedding or an image.
func observationNotApplicable(channel *model.Channel, modelName string, kind string) (string, string) {
	endpointType, ok := probeEndpointType(channel, modelName)
	if !ok {
		return "", "no request shape to check this model with"
	}
	switch constant.EndpointType(endpointType) {
	case constant.EndpointTypeEmbeddings, constant.EndpointTypeImageGeneration, constant.EndpointTypeJinaRerank:
		return endpointType, "not a text model"
	}
	if kind == model.ObservationCheckTools {
		if meta, found := observationModelMeta(modelName); found && !meta.Capabilities.SupportsFunctionCalling {
			return endpointType, "model does not declare tool support"
		}
	}
	return endpointType, ""
}

// observationModelMeta looks a routed model up in the catalog, by its own
// name first and then by the name it is priced under.
func observationModelMeta(modelName string) (model.Model, bool) {
	var meta model.Model
	if model.CoreDB() == nil {
		return meta, false
	}
	for _, name := range []string{modelName, model.NormalizePricingModelName(modelName)} {
		if name == "" {
			continue
		}
		if err := model.CoreDB().Where("model_name = ?", name).First(&meta).Error; err == nil {
			return meta, true
		}
	}
	return meta, false
}

// observationLongContextSize is the long-context check's input for one model:
// the standard size, or half the model's window when the window is smaller.
func observationLongContextSize(modelName string) int {
	size := common.AppConfig.SourceChecks.LongContextTokens
	if meta, found := observationModelMeta(modelName); found && meta.ContextLength > 0 && meta.ContextLength < int64(size)*2 {
		size = int(meta.ContextLength / 2)
	}
	return size
}

// estimateObservationCostUSD prices one run the way probes are priced, with
// the check's own input size.
func estimateObservationCostUSD(cell model.RouteObservationCell, kind string) float64 {
	inputTokens := observationBillingBaseTokens*2 + observationBillingStepTokens
	outputTokens := 64
	if kind == model.ObservationCheckLongContext {
		inputTokens = observationLongContextSize(cell.ModelName)
		outputTokens = observationLongContextOutputTokens
	}
	if kind == model.ObservationCheckTools {
		inputTokens = 200
		outputTokens = 128
	}
	if probeReasoningEffort(cell.ModelName) != "" && outputTokens < probeReasoningCap {
		outputTokens = probeReasoningCap
	}
	price := func(requestUSD, inputPerMtok, outputPerMtok *float64) float64 {
		cost := derefPrice(requestUSD) +
			float64(inputTokens)*derefPrice(inputPerMtok)/1_000_000 +
			float64(outputTokens)*derefPrice(outputPerMtok)/1_000_000
		if kind == model.ObservationCheckBilling {
			// Two requests.
			cost += derefPrice(requestUSD)
		}
		return cost
	}
	if pricing, ok := model.GetActiveProviderCostPrice(cell.ModelName, cell.ChannelId); ok {
		return price(pricing.RequestUSD, pricing.InputPerMtok, pricing.OutputPerMtok)
	}
	if sell, ok := model.GetActiveModelSellPrice(cell.ModelName, cell.Lane); ok {
		return price(sell.RequestUSD, sell.InputPerMtok, sell.OutputPerMtok)
	}
	// No price on file: assume the dearest input we route.
	return float64(inputTokens) * 15 / 1_000_000
}

// observationSend runs one check request and classifies what came back.
// ok=false with skipped=true means the request never reached the upstream.
func observationSend(ctx context.Context, target observationTarget, request dto.Request) (testResult, bool, bool, string) {
	result := testChannelForLaneWithRequest(ctx, target.channel, target.cell.ModelName,
		target.endpointType, target.cell.Lane, false, request)
	if err := ctx.Err(); err != nil {
		return result, false, true, err.Error()
	}
	switch {
	case !result.providerAttempted && (result.localErr != nil || result.lazuErr != nil):
		if result.localErr != nil {
			return result, false, true, result.localErr.Error()
		}
		return result, false, true, result.lazuErr.Error()
	case result.lazuErr != nil:
		return result, false, false, result.lazuErr.Error()
	case result.localErr != nil:
		return result, false, true, result.localErr.Error()
	}
	return result, true, false, ""
}

func observationShort(message string) string {
	message = strings.TrimSpace(message)
	if len(message) > 160 {
		message = message[:160]
	}
	return message
}

// runObservationBillingCheck measures what the upstream bills for a prompt
// against what the prompt holds.
//
// The upstream's tokenizer is not ours, so one prompt cannot be compared with
// a local count directly. Two prompts can: they differ by a known stretch of
// text, and the difference in what the upstream billed for them is that
// stretch in the upstream's own tokens. That gives its tokens per local token,
// which converts the first prompt's local count into what it should have
// billed. Anything billed beyond that was added on the way — typically a
// system prompt the reseller prepends to every request.
func runObservationBillingCheck(ctx context.Context, target observationTarget, check *model.RouteObservationCheck) {
	name := target.cell.ModelName
	base := observationFiller(observationBillingBaseTokens, name)
	step := observationFiller(observationBillingStepTokens, name)
	ask := "\nReply with the single word OK."
	short := "Reference " + observationNonce() + "\n" + base + ask
	long := "Reference " + observationNonce() + "\n" + base + step + ask
	shortLocal := service.CountTextToken(short, name)
	longLocal := service.CountTextToken(long, name)

	first, ok, skipped, message := observationSend(ctx, target, observationRequest(name, target.endpointType, short, 16, false))
	if !ok {
		check.Skipped, check.Detail = skipped, observationShort(message)
		return
	}
	second, ok, skipped, message := observationSend(ctx, target, observationRequest(name, target.endpointType, long, 16, false))
	if !ok {
		check.Skipped, check.Detail = skipped, observationShort(message)
		return
	}
	if first.promptTokens <= 0 || second.promptTokens <= first.promptTokens || longLocal <= shortLocal || shortLocal <= 0 {
		check.Detail = fmt.Sprintf("usage did not grow with the prompt (%d then %d tokens)", first.promptTokens, second.promptTokens)
		return
	}
	perLocal := float64(second.promptTokens-first.promptTokens) / float64(longLocal-shortLocal)
	expected := perLocal * float64(shortLocal)
	ratio := float64(first.promptTokens) / expected
	check.Value = ratio
	bars := common.AppConfig.SourceChecks
	check.Passed = ratio <= bars.MaxBillingRatio ||
		float64(first.promptTokens)-expected <= float64(bars.BillingFramingTokens)
	if !check.Passed {
		check.Detail = fmt.Sprintf("billed %d input tokens for about %.0f", first.promptTokens, expected)
	}
}

// runObservationLongContextCheck sends a long input with a code word at its
// very start and asks for the word back. A pool that trims the context either
// bills far less than was sent or cannot answer.
func runObservationLongContextCheck(ctx context.Context, target observationTarget, check *model.RouteObservationCheck) {
	name := target.cell.ModelName
	size := observationLongContextSize(name)
	word := "HARBOUR-" + observationNonce()[:6]
	prompt := "Remember this code word: " + word + "\n\n" + observationFiller(size, name) +
		"\n\nWhat was the code word given at the very start of this message? Reply with the code word only."
	local := service.CountTextToken(prompt, name)

	result, ok, skipped, message := observationSend(ctx, target,
		observationRequest(name, target.endpointType, prompt, observationLongContextOutputTokens, false))
	if !ok {
		check.Skipped, check.Detail = skipped, observationShort(message)
		return
	}
	check.Value = float64(result.promptTokens)
	if local > 0 && float64(result.promptTokens) < float64(local)*common.AppConfig.SourceChecks.LongContextMinShare {
		check.Detail = fmt.Sprintf("truncated: billed %d of about %d input tokens", result.promptTokens, local)
		return
	}
	text, _ := parseObservationReply(result.body)
	if !strings.Contains(strings.ToUpper(text), word) {
		check.Detail = "the reply did not contain the code word from the start of the input" + observationReplyQuote(text)
		return
	}
	check.Passed = true
}

// runObservationToolsCheck offers one function and requires a call to it with
// usable arguments.
func runObservationToolsCheck(ctx context.Context, target observationTarget, check *model.RouteObservationCheck) {
	name := target.cell.ModelName
	result, ok, skipped, message := observationSend(ctx, target,
		observationRequest(name, target.endpointType, observationToolPrompt, 128, true))
	if !ok && !skipped && observationToolsBelongOnResponses(target, result) {
		// Reasoning models such as gpt-6 refuse function tools on Chat
		// Completions and take them only on Responses — on every upstream,
		// official included. A customer calling tools reaches the route
		// there, so that is where the route is asked.
		responses := target
		responses.endpointType = string(constant.EndpointTypeOpenAIResponse)
		result, ok, skipped, message = observationSend(ctx, responses,
			observationRequest(name, responses.endpointType, observationToolPrompt, 128, true))
	}
	if !ok {
		check.Skipped, check.Detail = skipped, observationShort(message)
		return
	}
	text, calls := parseObservationReply(result.body)
	for _, call := range calls {
		if call.Name != observationToolName {
			continue
		}
		var args map[string]any
		if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
			check.Detail = "the tool call's arguments were not valid JSON"
			return
		}
		if city, _ := args["city"].(string); strings.TrimSpace(city) == "" {
			check.Detail = "the tool call had no city argument"
			return
		}
		check.Passed = true
		return
	}
	check.Detail = "the reply did not call the tool" + observationReplyQuote(text)
}

// observationToolsBelongOnResponses reports whether a tools check that the
// upstream rejected on Chat Completions should be asked again on Responses:
// the rejection was a 400 about the request, and the channel serves Responses
// natively for this model.
func observationToolsBelongOnResponses(target observationTarget, result testResult) bool {
	if constant.EndpointType(target.endpointType) != constant.EndpointTypeOpenAI ||
		result.lazuErr == nil || result.lazuErr.StatusCode != http.StatusBadRequest {
		return false
	}
	snapshot, _, err := textgenruntime.SnapshotForChannel(target.channel, target.cell.ModelName)
	return err == nil && snapshot.Features.Has(textgen.FeatureEndpointResponses)
}

// observationReplyQuote appends the start of a reply to a failed check's
// detail, so the console shows what came back instead of only that it was
// wrong.
func observationReplyQuote(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	if text == "" {
		return " (empty reply)"
	}
	if len(text) > 60 {
		cut := 60
		for cut > 0 && !utf8.RuneStart(text[cut]) {
			cut--
		}
		text = text[:cut] + "…"
	}
	return ": " + strconv.Quote(text)
}

// runObservationCheck runs one check on one watched route. It never records
// anything itself.
func runObservationCheck(ctx context.Context, cell model.RouteObservationCell, kind string, estimatedUSD float64) model.RouteObservationCheck {
	check := model.RouteObservationCheck{
		Lane: cell.Lane, ModelName: cell.ModelName, ChannelId: cell.ChannelId,
		Kind: kind, CheckedAt: time.Now().UTC(),
	}
	channel, err := model.GetChannelById(cell.ChannelId, true)
	if err != nil || channel == nil {
		check.Skipped, check.Detail = true, "channel unavailable"
		return check
	}
	endpointType, reason := observationNotApplicable(channel, cell.ModelName, kind)
	if reason != "" {
		check.NotApplicable, check.Passed, check.Detail = true, true, reason
		return check
	}
	target := observationTarget{cell: cell, channel: channel, endpointType: endpointType}
	check.CostUSD = estimatedUSD
	switch kind {
	case model.ObservationCheckBilling:
		runObservationBillingCheck(ctx, target, &check)
	case model.ObservationCheckLongContext:
		runObservationLongContextCheck(ctx, target, &check)
	case model.ObservationCheckTools:
		runObservationToolsCheck(ctx, target, &check)
	}
	return check
}

// observationDue lists the checks each watched route is owed now, oldest
// route first.
func observationDue(now time.Time) ([]model.RouteObservationCell, []string, error) {
	watched, err := model.LoadActiveRouteObservations(nil)
	if err != nil || len(watched) == 0 {
		return nil, nil, err
	}
	sort.Slice(watched, func(i, j int) bool {
		if !watched[i].StartedAt.Equal(watched[j].StartedAt) {
			return watched[i].StartedAt.Before(watched[j].StartedAt)
		}
		return watched[i].ChannelId < watched[j].ChannelId
	})
	ids := observationChannelIDs(watched)
	since := now.Add(-common.AppConfig.SourceChecks.CheckEvery())
	checks, err := model.LoadRouteObservationChecks(ids, since)
	if err != nil {
		return nil, nil, err
	}
	type last struct {
		ran     time.Time
		skipped bool
	}
	latest := map[string]last{}
	for _, check := range checks {
		key := model.ChannelHealthCellKey(check.ModelName, check.Lane, check.ChannelId) + "\x1f" + check.Kind
		latest[key] = last{ran: check.CheckedAt, skipped: check.Skipped}
	}
	channels := map[int]bool{}
	var cells []model.RouteObservationCell
	var kinds []string
	for _, row := range watched {
		if _, ok := channels[row.ChannelId]; !ok {
			channel, err := model.GetChannelById(row.ChannelId, false)
			// A disabled source is not probed either; its checks wait with it.
			channels[row.ChannelId] = err == nil && channel != nil && channel.Status == common.ChannelStatusEnabled
		}
		if !channels[row.ChannelId] {
			continue
		}
		cell := model.RouteObservationCell{Lane: row.Lane, ModelName: row.ModelName, ChannelId: row.ChannelId}
		for _, kind := range model.ObservationCheckKinds {
			previous, ran := latest[cell.Key()+"\x1f"+kind]
			if ran && !previous.skipped {
				continue
			}
			if ran && previous.skipped && now.Sub(previous.ran) < observationRetryAfter {
				continue
			}
			cells = append(cells, cell)
			kinds = append(kinds, kind)
		}
	}
	return cells, kinds, nil
}

func observationChannelIDs(rows []model.RouteObservation) []int {
	seen := map[int]bool{}
	ids := []int{}
	for _, row := range rows {
		if !seen[row.ChannelId] {
			seen[row.ChannelId] = true
			ids = append(ids, row.ChannelId)
		}
	}
	return ids
}

// RunRouteObservationSweep runs the checks watched routes are owed, within the
// probe budget. It shares the probe's daily ceiling and its per-cell lease, so
// a replica never runs the same check twice and observation never outspends
// what an operator allowed for synthetic traffic.
func RunRouteObservationSweep(ctx context.Context, now time.Time) (ChannelProbeSweepResult, error) {
	result := ChannelProbeSweepResult{}
	budget := channelProbeDailyBudgetUSD()
	if budget == 0 {
		result.BudgetHit = true
		return result, nil
	}
	cells, kinds, err := observationDue(now)
	if err != nil {
		return result, err
	}
	result.Considered = len(cells)
	for index, cell := range cells {
		select {
		case <-ctx.Done():
			result.Skipped += len(cells) - index
			return result, nil
		default:
		}
		if result.Probed >= observationMaxChecksPerSweep {
			result.Skipped += len(cells) - index
			break
		}
		kind := kinds[index]
		claimKey := "observe\x1f" + kind + "\x1f" + cell.Key()
		claimToken := uuid.NewString()
		claimedAt := time.Now().UTC()
		claimed, err := model.ClaimChannelProbe(claimKey, claimToken, claimedAt, claimedAt.Add(channelProbeClaimLease()))
		if err != nil {
			return result, err
		}
		if !claimed {
			result.Skipped++
			continue
		}
		release := func() {
			if err := model.ReleaseChannelProbeClaim(claimKey, claimToken); err != nil {
				common.SysLog("route observation: failed to release claim: " + err.Error())
			}
		}
		estimated := estimateObservationCostUSD(cell, kind)
		day := channelProbeUTCDate(time.Now())
		reserved, err := model.ReserveChannelProbeBudget(day, budget, estimated)
		if err != nil {
			release()
			return result, err
		}
		if !reserved {
			release()
			result.BudgetHit = true
			result.Skipped += len(cells) - index
			common.SysLog(fmt.Sprintf("route observation: daily probe budget $%.4f reached, %d checks wait", budget, len(cells)-index))
			break
		}
		check := runObservationCheck(ctx, cell, kind, estimated)
		if check.NotApplicable || (check.Skipped && check.CostUSD == 0) {
			check.CostUSD = 0
			if err := model.ReleaseChannelProbeBudget(day, estimated); err != nil {
				common.SysLog("route observation: failed to release unused budget: " + err.Error())
			}
		}
		if err := model.RecordRouteObservationCheck(check); err != nil {
			common.SysLog("route observation: failed to record check: " + err.Error())
		}
		release()
		result.Probed++
		result.EstimatedUSD += check.CostUSD
		if check.Skipped {
			result.Skipped++
		} else if !check.Passed {
			result.Failures++
		}
	}
	return result, nil
}

// --- Verdicts -------------------------------------------------------------

// Check and record states as the console reads them.
const (
	observationStatePending = "pending"
	observationStatePass    = "pass"
	observationStateFail    = "fail"
	observationStateNA      = "na"
)

type observationCheckView struct {
	Kind   string   `json:"kind"`
	State  string   `json:"state"`
	Passed int      `json:"passed"`
	Runs   int      `json:"runs"`
	Value  *float64 `json:"value"`
	// Latest is the billing ratio of the most recent run, where Value is the
	// worst seen. A check that failed once and recovered reads by its latest.
	Latest *float64 `json:"latest,omitempty"`
	Detail string   `json:"detail,omitempty"`
}

// observationView is where a watched route — or a source's watched routes
// taken together — stands.
type observationView struct {
	StartedAt    time.Time `json:"started_at"`
	Day          int       `json:"day"`
	DaysRequired int       `json:"days_required"`
	// MinSamples, MinSuccessRate and LongContextTokens are the bars this
	// deployment judges the route by, so the console can state them without
	// carrying its own copy. LongContextTokens is the standard size; a model
	// with a small window is checked with less.
	MinSamples        int                    `json:"min_samples"`
	MinSuccessRate    float64                `json:"min_success_rate"`
	LongContextTokens int                    `json:"long_context_tokens"`
	Complete          bool                   `json:"complete"`
	Samples           int64                  `json:"samples"`
	Failures          int64                  `json:"failures"`
	SuccessRate       *float64               `json:"success_rate"`
	SuccessState      string                 `json:"success_state"`
	Checks            []observationCheckView `json:"checks"`
	Ready             bool                   `json:"ready"`
	// Models counts the distinct models under observation; only a source's
	// aggregate sets it.
	Models int `json:"models,omitempty"`
	// Routes and LiveRoutes let the ledger tell a new source from a live one
	// that has picked up a few new models.
	Routes     int `json:"routes,omitempty"`
	LiveRoutes int `json:"live_routes,omitempty"`
	// Summary names the routes behind a source's aggregate, so the ledger
	// can say which model holds it back instead of showing the worst cell.
	Summary *observationSummary `json:"summary,omitempty"`
}

// setBars fills in the bars the view is judged by from the current config.
func (view *observationView) setBars() {
	bars := common.AppConfig.SourceChecks
	view.DaysRequired = bars.ObservationDays
	view.MinSamples = bars.MinSamples
	view.MinSuccessRate = bars.MinSuccessRate
	view.LongContextTokens = bars.LongContextTokens
}

func observationDay(startedAt time.Time, now time.Time) (int, bool) {
	elapsed := now.Sub(startedAt)
	if elapsed < 0 {
		elapsed = 0
	}
	days := model.RouteObservationDays()
	required := time.Duration(days) * 24 * time.Hour
	day := int(elapsed/(24*time.Hour)) + 1
	if day > days {
		day = days
	}
	return day, elapsed >= required
}

func observationSuccessState(samples, failures int64) (*float64, string) {
	if samples <= 0 {
		return nil, observationStatePending
	}
	rate := 1 - float64(failures)/float64(samples)
	bars := common.AppConfig.SourceChecks
	if samples < int64(bars.MinSamples) {
		return &rate, observationStatePending
	}
	if rate < bars.MinSuccessRate {
		return &rate, observationStateFail
	}
	return &rate, observationStatePass
}

// observationCheckVerdict folds one check's runs into a state. The latest run
// has to have passed and no more than one earlier run may have failed: one
// bad afternoon on a multi-day record is noise, a pattern is not.
func observationCheckVerdict(kind string, runs []model.RouteObservationCheck) observationCheckView {
	view := observationCheckView{Kind: kind, State: observationStatePending}
	var real []model.RouteObservationCheck
	allNA := true
	for _, run := range runs {
		if run.Skipped {
			continue
		}
		if !run.NotApplicable {
			allNA = false
		}
		real = append(real, run)
	}
	if len(real) == 0 {
		return view
	}
	if allNA {
		view.State = observationStateNA
		view.Detail = real[len(real)-1].Detail
		return view
	}
	failures := 0
	var worst *float64
	for _, run := range real {
		if run.NotApplicable {
			continue
		}
		view.Runs++
		if run.Passed {
			view.Passed++
		} else {
			failures++
			view.Detail = run.Detail
		}
		if kind == model.ObservationCheckBilling && run.Value > 0 {
			value := run.Value
			if worst == nil || value > *worst {
				worst = &value
			}
		}
	}
	view.Value = worst
	latest := real[len(real)-1]
	if kind == model.ObservationCheckBilling && !latest.NotApplicable && latest.Value > 0 {
		value := latest.Value
		view.Latest = &value
	}
	if latest.Passed && failures <= 1 {
		view.State = observationStatePass
		return view
	}
	view.State = observationStateFail
	if !latest.Passed {
		view.Detail = latest.Detail
	}
	return view
}

func observationRank(state string) int {
	switch state {
	case observationStateFail:
		return 3
	case observationStatePending:
		return 2
	case observationStatePass:
		return 1
	default:
		return 0
	}
}

// buildObservationViews reads every active observation on the given channels
// and returns a view per route.
func buildObservationViews(channelIDs []int, now time.Time) (map[string]observationView, []model.RouteObservation, error) {
	views := map[string]observationView{}
	watched, err := model.LoadActiveRouteObservations(channelIDs)
	if err != nil || len(watched) == 0 {
		return views, watched, err
	}
	ids := observationChannelIDs(watched)
	earliest := now
	for _, row := range watched {
		if row.StartedAt.Before(earliest) {
			earliest = row.StartedAt
		}
	}
	checks, err := model.LoadRouteObservationChecks(ids, earliest)
	if err != nil {
		return views, watched, err
	}
	tallies, err := model.LoadRouteProbeTallies(ids, earliest)
	if err != nil {
		return views, watched, err
	}
	// Tallies are counted from the earliest start; a route that joined later
	// must not inherit samples from before its own start, so those are
	// counted again from their own start.
	byCell := map[string][]model.RouteObservationCheck{}
	for _, check := range checks {
		cell := model.ChannelHealthCellKey(check.ModelName, check.Lane, check.ChannelId)
		byCell[cell] = append(byCell[cell], check)
	}
	for _, row := range watched {
		cell := model.RouteObservationCell{Lane: row.Lane, ModelName: row.ModelName, ChannelId: row.ChannelId}
		key := cell.Key()
		tally := tallies[key]
		if row.StartedAt.After(earliest) {
			own, err := model.LoadRouteProbeTallies([]int{row.ChannelId}, row.StartedAt)
			if err != nil {
				return views, watched, err
			}
			tally = own[key]
		}
		day, complete := observationDay(row.StartedAt, now)
		rate, state := observationSuccessState(tally.Samples, tally.Failures)
		view := observationView{
			StartedAt: row.StartedAt, Day: day,
			Complete: complete, Samples: tally.Samples, Failures: tally.Failures, SuccessRate: rate, SuccessState: state,
		}
		view.setBars()
		ready := complete && state == observationStatePass
		for _, kind := range model.ObservationCheckKinds {
			var runs []model.RouteObservationCheck
			for _, check := range byCell[key] {
				if check.Kind == kind && !check.CheckedAt.Before(row.StartedAt) {
					runs = append(runs, check)
				}
			}
			verdict := observationCheckVerdict(kind, runs)
			if verdict.State != observationStatePass && verdict.State != observationStateNA {
				ready = false
			}
			view.Checks = append(view.Checks, verdict)
		}
		view.Ready = ready
		views[key] = view
	}
	return views, watched, nil
}

// aggregateObservationViews folds a source's watched routes into one view:
// the least advanced day, the worst state of every check, and ready only when
// every route is.
func aggregateObservationViews(views []observationView, models int) observationView {
	if len(views) == 0 {
		return observationView{}
	}
	result := observationView{
		Complete: true, Ready: true,
		Day: model.RouteObservationDays(), Models: models, Routes: len(views),
	}
	result.setBars()
	var samples, failures float64
	checks := map[string]*observationCheckView{}
	for _, view := range views {
		if result.StartedAt.IsZero() || view.StartedAt.After(result.StartedAt) {
			result.StartedAt = view.StartedAt
		}
		if view.Day < result.Day {
			result.Day = view.Day
		}
		result.Complete = result.Complete && view.Complete
		result.Ready = result.Ready && view.Ready
		result.Samples += view.Samples
		result.Failures += view.Failures
		samples += float64(view.Samples)
		if view.SuccessRate != nil {
			failures += float64(view.Samples) * (1 - *view.SuccessRate)
		}
		if observationRank(view.SuccessState) > observationRank(result.SuccessState) {
			result.SuccessState = view.SuccessState
		}
		for _, check := range view.Checks {
			current, ok := checks[check.Kind]
			if !ok {
				copied := check
				checks[check.Kind] = &copied
				continue
			}
			current.Runs += check.Runs
			current.Passed += check.Passed
			if check.Value != nil && (current.Value == nil || *check.Value > *current.Value) {
				value := *check.Value
				current.Value = &value
			}
			if observationRank(check.State) > observationRank(current.State) {
				current.State = check.State
				current.Detail = check.Detail
			}
		}
	}
	if samples > 0 {
		rate := 1 - failures/samples
		result.SuccessRate = &rate
	}
	for _, kind := range model.ObservationCheckKinds {
		if check, ok := checks[kind]; ok {
			result.Checks = append(result.Checks, *check)
		}
	}
	return result
}

// --- Going live ------------------------------------------------------------

// requireObservationPassed refuses to take routes out of inventory while any
// of them is still being watched and has not passed.
func requireObservationPassed(channelID int, requestedID string, lane string, modelName string, routed []string) error {
	views, _, err := buildObservationViews([]int{channelID}, time.Now().UTC())
	if err != nil {
		return err
	}
	for _, name := range routed {
		view, watched := views[model.ChannelHealthCellKey(name, lane, channelID)]
		if watched && !view.Ready {
			return types.Err(types.ErrorCodeRouteObservationIncomplete, types.D{
				"channel_id": requestedID, "model": modelName,
			})
		}
	}
	return nil
}

type routeObservationGoLiveRequest struct {
	Role string `json:"role"`
	// Models limits the go-live to these watched models. Empty means every
	// watched model of the source, as before.
	Models []string `json:"models"`
}

// GoLiveModelOpsSource takes watched routes of one source live: every one of
// them, or only the models the request names. Either way it is all or nothing
// for what was asked: one route that has not passed refuses the whole call.
func GoLiveModelOpsSource(c *gin.Context) {
	channelID, err := resolveChannelID(c.Param("id"))
	if err != nil {
		httperr.Write(c, err)
		return
	}
	var req routeObservationGoLiveRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httperr.Code(c, types.ErrorCodeBadRequestBody)
		return
	}
	role := strings.ToLower(strings.TrimSpace(req.Role))
	if role == "" {
		role = model.AbilityRoleBackup
	}
	if role != model.AbilityRoleBackup && role != model.AbilityRolePrimary {
		httperr.Invalid(c, "role")
		return
	}
	views, watched, err := buildObservationViews([]int{channelID}, time.Now().UTC())
	if err != nil {
		httperr.Write(c, err)
		return
	}
	if len(req.Models) > 0 {
		wanted := map[string]bool{}
		for _, name := range req.Models {
			wanted[strings.TrimSpace(name)] = true
		}
		selected := watched[:0:0]
		found := map[string]bool{}
		for _, row := range watched {
			if wanted[row.ModelName] {
				selected = append(selected, row)
				found[row.ModelName] = true
			}
		}
		for name := range wanted {
			if !found[name] {
				httperr.Code(c, types.ErrorCodeRouteObservationNone, types.D{"channel_id": c.Param("id"), "model": name})
				return
			}
		}
		watched = selected
	}
	if len(watched) == 0 {
		httperr.Code(c, types.ErrorCodeRouteObservationNone, types.D{"channel_id": c.Param("id")})
		return
	}
	for _, row := range watched {
		if view := views[model.ChannelHealthCellKey(row.ModelName, row.Lane, row.ChannelId)]; !view.Ready {
			httperr.Code(c, types.ErrorCodeRouteObservationIncomplete, types.D{
				"channel_id": c.Param("id"), "model": row.ModelName,
			})
			return
		}
	}
	byLane := map[string][]string{}
	for _, row := range watched {
		byLane[row.Lane] = append(byLane[row.Lane], row.ModelName)
	}
	err = model.CoreDB().Transaction(func(tx *gorm.DB) error {
		for lane, models := range byLane {
			if err := tx.Model(&model.Ability{}).
				Where("channel_id = ? AND model IN ? AND role = ?", channelID, models, model.AbilityRoleInventory).
				Where(modelOpsAbilityGroupColumn()+" = ?", lane).
				Update("role", role).Error; err != nil {
				return err
			}
			if err := model.EndRouteObservations(tx, channelID, lane, models); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		httperr.Write(c, err)
		return
	}
	model.WriteAdminAuditLog(c, "model_ops.source.go_live", "channel",
		fmt.Sprintf("%d", channelID), c.Param("id"),
		gin.H{"role": model.AbilityRoleInventory}, gin.H{"role": role, "routes": byLane})
	model.InitChannelCache()
	InvalidateChannelHealthCache()
	common.ApiSuccess(c, gin.H{"role": role, "routes": len(watched)})
}

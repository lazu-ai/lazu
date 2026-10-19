package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/httperr"
	"github.com/lazu-ai/lazu/internal/middleware"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/relay"
	relaychannel "github.com/lazu-ai/lazu/internal/relay/channel"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	relayconstant "github.com/lazu-ai/lazu/internal/relay/constant"
	"github.com/lazu-ai/lazu/internal/relay/helper"
	"github.com/lazu-ai/lazu/internal/service"
	"github.com/lazu-ai/lazu/internal/setting/operation_setting"
	"github.com/lazu-ai/lazu/internal/types"
	"github.com/lazu-ai/lazu/pkg/billing"

	"github.com/gin-gonic/gin"
)

type testResult struct {
	context  *gin.Context
	localErr error
	lazuErr  *types.LazuError
	// providerAttempted distinguishes an upstream verdict from request setup,
	// mapping, pricing or conversion failures inside this process.
	providerAttempted bool
	// The remaining fields describe a successful upstream exchange. durationMs
	// runs from sending the request to the end of the reply; the stream fields
	// are only set when the reply was streamed and produced a first chunk.
	durationMs   int
	streamed     bool
	ttftMs       int
	maxStallMs   int
	outputTokens int
	// answerTtftMs and reasoningTokens keep the decode-speed sample to the
	// answer; see RelayInfo.FirstAnswerTime.
	answerTtftMs    int
	reasoningTokens int
	// promptTokens is the input the upstream billed, which the observation
	// checks compare against what was actually sent.
	promptTokens int
	// body is the reply as the client would have received it, for probes
	// that must check what came back and not only that something did.
	body []byte
}

func testChannel(channel *model.Channel, testModel string, endpointType string) testResult {
	return testChannelWithOptions(channel, testModel, endpointType, true)
}

// testChannelWithOptions runs one synthetic request against a channel.
//
// recordConsumption controls whether the call is written to the consume log.
// An operator-triggered test should be recorded — someone asked for it and the
// spend is real. Automated health probes must not be: they run hourly, are
// attributed to user 1, and would otherwise show up as customer usage, feed
// spend exports and skew model popularity counters.
func testChannelWithOptions(
	channel *model.Channel,
	testModel string,
	endpointType string,
	recordConsumption bool,
) testResult {
	return testChannelForLane(context.Background(), channel, testModel, endpointType, channel.Group, recordConsumption)
}

// testChannelForLane additionally pins the lane the request is priced and routed
// under. A probe must run as its own lane: pricing normalises an unspecified
// group to direct, so an eco-only model would fail locally as
// PricingNotConfigured and be recorded as an upstream outage it never reached.
func testChannelForLane(
	ctx context.Context,
	channel *model.Channel,
	testModel string,
	endpointType string,
	lane string,
	recordConsumption bool,
) testResult {
	return testChannelForLaneWithRequest(ctx, channel, testModel, endpointType, lane, recordConsumption, nil)
}

// testChannelForLaneWithRequest sends `custom` instead of the built-in test
// request. The observation checks use it to ask things a liveness probe does
// not: a long input, a tool, a prompt of known size. The model name on
// `custom` is overwritten with the mapped upstream name like any test's.
func testChannelForLaneWithRequest(
	ctx context.Context,
	channel *model.Channel,
	testModel string,
	endpointType string,
	lane string,
	recordConsumption bool,
	custom dto.Request,
) testResult {
	if channel.GetOtherSettings().CodexClientOnly {
		return testResult{localErr: errors.New("Codex-only source: use a real Codex session; generic health probe skipped")}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	tik := time.Now()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	testModel = strings.TrimSpace(testModel)
	if testModel == "" {
		if channel.TestModel != nil && *channel.TestModel != "" {
			testModel = strings.TrimSpace(*channel.TestModel)
		} else {
			models := channel.GetModels()
			if len(models) > 0 {
				testModel = strings.TrimSpace(models[0])
			}
			if testModel == "" {
				testModel = "gpt-4o-mini"
			}
		}
	}

	requestPath := "/v1/chat/completions"

	// 如果指定了端点类型，使用指定的端点类型
	if endpointType != "" {
		endpointInfo, ok := common.GetDefaultEndpointInfo(constant.EndpointType(endpointType))
		if !ok {
			// 曾经这里静默回退到 /v1/chat/completions：传错 endpoint_type（例如把
			// image-generation 写成 image_generation）会拿 chat 接口去打图像模型，
			// 上游合理地拒绝，于是"模型不可用"的结论完全是测试自己造出来的。
			return testResult{
				localErr: fmt.Errorf("unknown endpoint_type %q", endpointType),
				lazuErr:  nil,
			}
		}
		requestPath = endpointInfo.Path
	} else {
		// 如果没有指定端点类型，使用原有的自动检测逻辑
		// 先判断是否为 Embedding 模型
		if strings.Contains(strings.ToLower(testModel), "embedding") ||
			strings.HasPrefix(testModel, "m3e") || // m3e 系列模型
			strings.Contains(testModel, "bge-") || // bge 系列模型
			strings.Contains(testModel, "embed") {
			requestPath = "/v1/embeddings" // 修改请求路径
		}
		if common.IsVideoGenerationModel(testModel) {
			requestPath = "/v1/videos"
		}
	}

	c.Request = &http.Request{
		Method: "POST",
		URL:    &url.URL{Path: requestPath}, // 使用动态路径
		Body:   nil,
		Header: make(http.Header),
	}
	c.Request = c.Request.WithContext(ctx)

	cache, err := model.GetUserCache(1)
	if err != nil {
		return testResult{
			localErr: err,
			lazuErr:  nil,
		}
	}
	cache.WriteContext(c)

	//c.Request.Header.Set("Authorization", "Bearer "+channel.Key)
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("channel", channel.Type)
	c.Set("base_url", channel.GetBaseURL())
	group := model.ChannelLaneDirect
	if lane = model.NormalizeChannelLane(lane); lane != "" && model.IsValidChannelLane(lane) {
		group = lane
	}
	c.Set("group", group)

	lazuErr := middleware.SetupContextForSelectedChannel(c, channel, testModel)
	if lazuErr != nil {
		return testResult{
			context:  c,
			localErr: lazuErr,
			lazuErr:  lazuErr,
		}
	}

	// Determine relay format based on endpoint type or request path
	var relayFormat types.RelayFormat
	if endpointType != "" {
		// 根据指定的端点类型设置 relayFormat
		switch constant.EndpointType(endpointType) {
		case constant.EndpointTypeOpenAI:
			relayFormat = types.RelayFormatOpenAI
		case constant.EndpointTypeOpenAIResponse:
			relayFormat = types.RelayFormatOpenAIResponses
		case constant.EndpointTypeAnthropic:
			relayFormat = types.RelayFormatClaude
		case constant.EndpointTypeGemini:
			relayFormat = types.RelayFormatGemini
		case constant.EndpointTypeJinaRerank:
			relayFormat = types.RelayFormatRerank
		case constant.EndpointTypeDecisions:
			relayFormat = types.RelayFormatDecisions
		case constant.EndpointTypeImageGeneration:
			relayFormat = types.RelayFormatOpenAIImage
		case constant.EndpointTypeVideoGeneration:
			relayFormat = types.RelayFormatOpenAI
		case constant.EndpointTypeEmbeddings:
			relayFormat = types.RelayFormatEmbedding
		default:
			relayFormat = types.RelayFormatOpenAI
		}
	} else {
		// 根据请求路径自动检测
		relayFormat = types.RelayFormatOpenAI
		if c.Request.URL.Path == "/v1/embeddings" {
			relayFormat = types.RelayFormatEmbedding
		}
		if c.Request.URL.Path == "/v1/images/generations" {
			relayFormat = types.RelayFormatOpenAIImage
		}
		if c.Request.URL.Path == "/v1/messages" {
			relayFormat = types.RelayFormatClaude
		}
		if strings.Contains(c.Request.URL.Path, "/v1beta/models") {
			relayFormat = types.RelayFormatGemini
		}
		if c.Request.URL.Path == "/v1/rerank" || c.Request.URL.Path == "/rerank" {
			relayFormat = types.RelayFormatRerank
		}
		if c.Request.URL.Path == "/v1/responses" {
			relayFormat = types.RelayFormatOpenAIResponses
		}
	}

	request := custom
	if request == nil {
		request = buildTestRequest(testModel, endpointType)
		if !recordConsumption {
			// Automated probes are shaped for measurement and priced by the daily
			// budget; see prepareProbeRequest.
			prepareProbeRequest(request)
		}
	}
	applyProbeIdentity(c, channel, request)

	info, err := relaycommon.GenRelayInfo(c, relayFormat, request, nil)

	if err != nil {
		return testResult{
			context:  c,
			localErr: err,
			lazuErr:  types.NewError(err, types.ErrorCodeGenRelayInfoFailed),
		}
	}

	info.InitChannelMeta(c)
	if relayFormat == types.RelayFormatDecisions {
		return testDecisionChannel(c, info, recordConsumption)
	}

	err = helper.ModelMappedHelper(c, info, request)
	if err != nil {
		return testResult{
			context:  c,
			localErr: err,
			lazuErr:  types.NewError(err, types.ErrorCodeChannelModelMappedError),
		}
	}

	testModel = info.UpstreamModelName
	// 更新请求中的模型名称
	request.SetModelName(testModel)

	apiType, _ := common.ChannelType2APIType(channel.Type)
	adaptor := relay.GetAdaptor(apiType)
	if adaptor == nil {
		return testResult{
			context:  c,
			localErr: fmt.Errorf("invalid api type: %d, adaptor is nil", apiType),
			lazuErr:  types.NewError(fmt.Errorf("invalid api type: %d, adaptor is nil", apiType), types.ErrorCodeInvalidApiType),
		}
	}

	//// 创建一个用于日志的 info 副本，移除 ApiKey
	//logInfo := info
	//logInfo.ApiKey = ""
	common.SysLog(fmt.Sprintf("testing channel %d with model %s , info %+v ", channel.Id, testModel, info.ToString()))

	adaptor.Init(info)
	if info.RelayMode == relayconstant.RelayModeVideoSubmit {
		err := errors.New("video channel tests are unsupported until provider execution is implemented")
		return testResult{
			context:  c,
			localErr: err,
		}
	}

	priceData, err := helper.PriceQuoteHelper(c, info, 0, request.GetTokenCountMeta())
	if err != nil {
		return testResult{
			context:  c,
			localErr: err,
			lazuErr:  types.NewError(err, types.ErrorCodePricingNotConfigured),
		}
	}

	var convertedRequest any
	// 根据 RelayMode 选择正确的转换函数
	switch info.RelayMode {
	case relayconstant.RelayModeEmbeddings:
		// Embedding 请求 - request 已经是正确的类型
		if embeddingReq, ok := request.(*dto.EmbeddingRequest); ok {
			embeddingConverter, castOK := adaptor.(relaychannel.EmbeddingRequestConverter)
			if !castOK {
				return testResult{
					context:  c,
					localErr: errors.New("adaptor does not support embedding conversion"),
					lazuErr:  types.NewError(errors.New("adaptor does not support embedding conversion"), types.ErrorCodeConvertRequestFailed),
				}
			}
			convertedRequest, err = embeddingConverter.ConvertEmbeddingRequest(c, info, *embeddingReq)
		} else {
			return testResult{
				context:  c,
				localErr: errors.New("invalid embedding request type"),
				lazuErr:  types.NewError(errors.New("invalid embedding request type"), types.ErrorCodeConvertRequestFailed),
			}
		}
	case relayconstant.RelayModeImagesGenerations:
		// 图像生成请求 - request 已经是正确的类型
		if imageReq, ok := request.(*dto.ImageRequest); ok {
			imageConverter, castOK := adaptor.(relaychannel.ImageRequestConverter)
			if !castOK {
				return testResult{
					context:  c,
					localErr: errors.New("adaptor does not support image conversion"),
					lazuErr:  types.NewError(errors.New("adaptor does not support image conversion"), types.ErrorCodeConvertRequestFailed),
				}
			}
			convertedRequest, err = imageConverter.ConvertImageRequest(c, info, *imageReq)
		} else {
			return testResult{
				context:  c,
				localErr: errors.New("invalid image request type"),
				lazuErr:  types.NewError(errors.New("invalid image request type"), types.ErrorCodeConvertRequestFailed),
			}
		}
	case relayconstant.RelayModeRerank:
		// Rerank 请求 - request 已经是正确的类型
		if rerankReq, ok := request.(*dto.RerankRequest); ok {
			rerankConverter, castOK := adaptor.(relaychannel.RerankRequestConverter)
			if !castOK {
				return testResult{
					context:  c,
					localErr: errors.New("adaptor does not support rerank conversion"),
					lazuErr:  types.NewError(errors.New("adaptor does not support rerank conversion"), types.ErrorCodeConvertRequestFailed),
				}
			}
			convertedRequest, err = rerankConverter.ConvertRerankRequest(c, info.RelayMode, *rerankReq)
		} else {
			return testResult{
				context:  c,
				localErr: errors.New("invalid rerank request type"),
				lazuErr:  types.NewError(errors.New("invalid rerank request type"), types.ErrorCodeConvertRequestFailed),
			}
		}
	case relayconstant.RelayModeResponses:
		// Response 请求 - request 已经是正确的类型
		if responseReq, ok := request.(*dto.OpenAIResponsesRequest); ok {
			responsesConverter, castOK := adaptor.(relaychannel.ResponsesRequestConverter)
			if !castOK {
				return testResult{
					context:  c,
					localErr: errors.New("adaptor does not support responses conversion"),
					lazuErr:  types.NewError(errors.New("adaptor does not support responses conversion"), types.ErrorCodeConvertRequestFailed),
				}
			}
			convertedRequest, err = responsesConverter.ConvertOpenAIResponsesRequest(c, info, *responseReq)
		} else {
			return testResult{
				context:  c,
				localErr: errors.New("invalid response request type"),
				lazuErr:  types.NewError(errors.New("invalid response request type"), types.ErrorCodeConvertRequestFailed),
			}
		}
	default:
		// Chat/Completion 等其他请求类型
		if generalReq, ok := request.(*dto.GeneralOpenAIRequest); ok {
			openAIConverter, castOK := adaptor.(relaychannel.OpenAIRequestConverter)
			if !castOK {
				return testResult{
					context:  c,
					localErr: errors.New("adaptor does not support openai conversion"),
					lazuErr:  types.NewError(errors.New("adaptor does not support openai conversion"), types.ErrorCodeConvertRequestFailed),
				}
			}
			convertedRequest, err = openAIConverter.ConvertOpenAIRequest(c, info, generalReq)
		} else {
			return testResult{
				context:  c,
				localErr: errors.New("invalid general request type"),
				lazuErr:  types.NewError(errors.New("invalid general request type"), types.ErrorCodeConvertRequestFailed),
			}
		}
	}

	if err != nil {
		return testResult{
			context:  c,
			localErr: err,
			lazuErr:  types.NewError(err, types.ErrorCodeConvertRequestFailed),
		}
	}
	jsonData, err := marshalChannelTestRequest(convertedRequest, info)
	if err != nil {
		return testResult{
			context:  c,
			localErr: err,
			lazuErr:  types.NewError(err, types.ErrorCodeJsonMarshalFailed),
		}
	}
	requestBody := bytes.NewBuffer(jsonData)
	c.Request.Body = io.NopCloser(requestBody)
	// Timings run from the moment the request leaves, so local setup above —
	// user cache, pricing, conversion — is not charged to the upstream.
	requestStarted := time.Now()
	resp, err := adaptor.DoRequest(c, info, requestBody)
	if err != nil {
		return testResult{
			context: c, localErr: err,
			lazuErr:           types.NewOpenAIError(err, types.ErrorCodeAPIConnectionError, http.StatusInternalServerError),
			providerAttempted: true,
		}
	}
	var httpResp *http.Response
	if resp != nil {
		httpResp = resp.(*http.Response)
		if httpResp.StatusCode != http.StatusOK {
			err := service.RelayErrorHandler(c, httpResp, true)
			return testResult{
				context: c, localErr: err,
				lazuErr:           types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusInternalServerError),
				providerAttempted: true,
			}
		}
	}
	usageA, respErr := adaptor.DoResponse(c, httpResp, info)
	if respErr != nil {
		failed := testResult{
			context: c, localErr: respErr, lazuErr: respErr,
			providerAttempted: true,
		}
		if info.IsStream && info.HasSendResponse() {
			// A stream that broke after its first output is an interruption,
			// not a refusal; the probe keeps the timing that says so.
			failed.streamed = true
			failed.ttftMs = int(info.OutputTTFT(requestStarted).Milliseconds())
			failed.maxStallMs = info.MaxStallMs
		}
		return failed
	}
	if usageA == nil {
		return testResult{
			context: c, localErr: errors.New("usage is nil"),
			lazuErr:           types.NewOpenAIError(errors.New("usage is nil"), types.ErrorCodeBadResponseBody, http.StatusInternalServerError),
			providerAttempted: true,
		}
	}
	usage := usageA.(*dto.Usage)
	result := w.Result()
	respBody, err := io.ReadAll(result.Body)
	if err != nil {
		return testResult{
			context: c, localErr: err,
			lazuErr:           types.NewOpenAIError(err, types.ErrorCodeReadResponseBodyFailed, http.StatusInternalServerError),
			providerAttempted: true,
		}
	}
	info.PromptTokens = usage.PromptTokens

	// Image output tokens bill on their own dimension, so keep them out of the
	// text-output count — mirror SummaryFromDTO, which subtracts them too.
	textOutputTokens := usage.CompletionTokens - usage.CompletionTokenDetails.ImageTokens
	if textOutputTokens < 0 {
		textOutputTokens = 0
	}

	quote := service.QuoteModelUsage(info, info.OriginModelName, billing.Usage{
		InputTokens:       int64(usage.PromptTokens),
		OutputTokens:      int64(textOutputTokens),
		CacheReadTokens:   int64(usage.PromptTokensDetails.CachedTokens),
		ImageInputTokens:  int64(usage.PromptTokensDetails.ImageTokens),
		ImageOutputTokens: int64(usage.CompletionTokenDetails.ImageTokens),
	}, nil)
	tok := time.Now()
	milliseconds := tok.Sub(tik).Milliseconds()
	consumedTime := float64(milliseconds) / 1000.0
	if recordConsumption {
		other := service.GenerateTextOtherInfo(c, info, usage.PromptTokensDetails.CachedTokens, priceData.PerCallPriceUSD)
		model.RecordConsumeLog(c, 1, model.RecordConsumeLogParams{
			ChannelId:        channel.Id,
			PromptTokens:     usage.PromptTokens,
			CompletionTokens: usage.CompletionTokens,
			ModelName:        info.OriginModelName,
			TokenName:        "模型测试",
			AmountMicrousd:   quote.TotalMicrousd,
			PricingVersion:   quote.PricingVersion,
			LineItems:        service.QuoteLineItemsJSON(quote),
			Content:          "模型测试",
			UseTimeSeconds:   int(consumedTime),
			IsStream:         info.IsStream,
			Group:            info.UsingGroup,
			Other:            other,
		})
	}
	// An image reply is megabytes of base64; the log only needs its start.
	logged := respBody
	if len(logged) > 2048 {
		logged = logged[:2048]
	}
	common.SysLog(fmt.Sprintf("testing channel #%d, response: \n%s", channel.Id, string(logged)))
	outcome := testResult{
		context: c, localErr: nil, lazuErr: nil,
		providerAttempted: true,
		durationMs:        int(time.Since(requestStarted).Milliseconds()),
		outputTokens:      usage.CompletionTokens,
		reasoningTokens:   service.ReportedReasoningTokens(usage),
		promptTokens:      observedPromptTokens(usage),
		body:              respBody,
	}
	if info.IsStream && info.HasSendResponse() {
		outcome.streamed = true
		// First token, not first frame: see RelayInfo.FirstOutputTime.
		outcome.ttftMs = int(info.OutputTTFT(requestStarted).Milliseconds())
		outcome.answerTtftMs = int(info.AnswerTTFT(requestStarted).Milliseconds())
		outcome.maxStallMs = info.MaxStallMs
	}
	return outcome
}

func marshalChannelTestRequest(convertedRequest any, info *relaycommon.RelayInfo) ([]byte, error) {
	jsonData, err := json.Marshal(convertedRequest)
	if err != nil {
		return nil, err
	}
	if info == nil || info.ChannelMeta == nil || !info.ChannelOtherSettings.ExplicitStreamRequired {
		return jsonData, nil
	}
	switch info.RelayMode {
	case relayconstant.RelayModeChatCompletions, relayconstant.RelayModeCompletions, relayconstant.RelayModeResponses:
	default:
		if info.RelayFormat != types.RelayFormatClaude {
			return jsonData, nil
		}
	}
	return relaycommon.SetExplicitStream(jsonData, info.IsStream)
}

// probePrompt asks for a reply that is certain to run past the output cap, so
// every probe decodes the same number of tokens and its speed is comparable
// from hour to hour and model to model.
const probePrompt = "Count from 1 to 100, separated by single spaces. Output only the numbers."

// probeImagePrompt asks for the simplest image a model can draw, so the probe
// measures the route rather than the model's ambition.
const probeImagePrompt = "A single red circle centred on a plain white background."

// probeReasoningCap is the output ceiling for a reasoning model whose thinking
// cannot be switched off entirely. At ChannelProbeMaxOutputTokens such a model
// spends the whole budget thinking and never emits a visible token, so its
// probes report no first-token time at all.
const probeReasoningCap = 512

// probeReasoningEffort is the lowest reasoning effort a model family accepts
// through the OpenAI request shape, or "" when the family either does not
// reason or must not be sent the parameter. Only the families listed here
// pass reasoning_effort straight through; other adaptors translate it into a
// thinking budget, which would switch thinking ON for a probe.
func probeReasoningEffort(modelName string) string {
	name := strings.ToLower(strings.TrimSpace(modelName))
	switch {
	case strings.HasPrefix(name, "gpt-5-chat"):
		return ""
	case name == "gpt-5" || strings.HasPrefix(name, "gpt-5-"):
		// The original gpt-5 family accepts "minimal", which usually skips
		// thinking outright; later gpt-5.x releases default to little or no
		// reasoning and do not accept it.
		return "minimal"
	case len(name) > 1 && name[0] == 'o' && name[1] >= '1' && name[1] <= '9':
		return "low"
	default:
		return ""
	}
}

// prepareProbeRequest turns a manual-test request into a health probe: a
// streamed reply to a fixed prompt, capped at ChannelProbeMaxOutputTokens, or
// the smallest image the model can make.
//
// Streaming is what makes a text probe a sample of the lane rather than a
// ping: it yields time to first token, the longest pause between chunks and
// decode speed, none of which depend on how long the reply is. The cap is
// what the daily budget prices; buildTestRequest is tuned for a manual test —
// Gemini gets 3000 output tokens and the Responses shape is uncapped — so an
// uncapped probe on a reasoning model could cost orders of magnitude more.
func prepareProbeRequest(request dto.Request) {
	switch typed := request.(type) {
	case *dto.GeneralOpenAIRequest:
		typed.Stream = true
		typed.StreamOptions = &dto.StreamOptions{IncludeUsage: true}
		typed.Messages = []dto.Message{{Role: "user", Content: probePrompt}}
		limit := uint(ChannelProbeMaxOutputTokens)
		if effort := probeReasoningEffort(typed.Model); effort != "" {
			typed.ReasoningEffort = effort
			limit = probeReasoningCap
		}
		// Cap whichever field the builder already chose. Setting both would
		// leave MaxTokens non-zero on a reasoning model, and the OpenAI
		// adaptor only drops the legacy field when MaxCompletionTokens is
		// zero — so filling both forwards an unsupported parameter and the
		// upstream rejection would be recorded as an outage.
		switch {
		case typed.MaxCompletionTokens > 0:
			typed.MaxCompletionTokens = limit
		default:
			typed.MaxTokens = limit
		}
	case *dto.OpenAIResponsesRequest:
		typed.Stream = true
		prompt, _ := json.Marshal(probePrompt)
		typed.Input = prompt
		limit := uint(ChannelProbeMaxOutputTokens)
		if effort := probeReasoningEffort(typed.Model); effort != "" {
			typed.Reasoning = &dto.Reasoning{Effort: effort}
			limit = probeReasoningCap
		}
		typed.MaxOutputTokens = limit
	case *dto.ImageRequest:
		typed.Prompt = probeImagePrompt
		typed.N = 1
		typed.Size = dto.SeedreamPresetSize(typed.Model, "1024x1024")
		if strings.HasPrefix(strings.ToLower(typed.Model), "gpt-image") {
			typed.Quality = "low"
		}
	}
}

// observedPromptTokens is every input token the upstream billed for the
// request. OpenAI-shaped usage already counts cached tokens inside
// prompt_tokens; Anthropic's input_tokens leaves out cache reads and writes, so
// a pool that caches on its own side would look as if it had billed a
// handful of tokens for a 60k-token prompt.
func observedPromptTokens(usage *dto.Usage) int {
	if usage == nil {
		return 0
	}
	if !usage.PromptExcludesCache {
		return usage.PromptTokens
	}
	details := usage.PromptTokensDetails
	written := details.CachedCreationTokens
	if written == 0 {
		written = details.CachedCreation5mTokens + details.CachedCreation1hTokens
	}
	return usage.PromptTokens + details.CachedTokens + written
}

// applyProbeIdentity lets a probe or observation check reach a source that
// admits only one client. The identity comes from probe_identities in
// config.yaml; a source may override its User-Agent. Without a configured
// identity the probe goes out as-is. Every other source is tested exactly as
// before.
func applyProbeIdentity(c *gin.Context, channel *model.Channel, request dto.Request) {
	settings := channel.GetOtherSettings()
	if !settings.ClaudeCodeClientOnly {
		return
	}
	identity, configured := common.ProbeIdentityFor(common.ProbeClientClaudeCode)
	if userAgent := strings.TrimSpace(settings.ClaudeCodeUserAgent); userAgent != "" {
		identity.UserAgent = userAgent
		configured = true
	}
	if !configured {
		return
	}
	if identity.UserAgent != "" {
		c.Request.Header.Set("User-Agent", identity.UserAgent)
	}
	for name, value := range identity.Headers {
		c.Request.Header.Set(name, value)
	}
	metadata, err := common.RenderProbeMetadata(identity.Metadata, channel.Id)
	if err != nil {
		common.SysError(fmt.Sprintf("probe identity for channel %d: %v", channel.Id, err))
		return
	}
	if len(metadata) == 0 {
		return
	}
	switch typed := request.(type) {
	case *dto.GeneralOpenAIRequest:
		if len(typed.Metadata) == 0 {
			typed.Metadata = metadata
		}
	case *dto.ClaudeRequest:
		if len(typed.Metadata) == 0 {
			typed.Metadata = metadata
		}
	}
}

func buildTestRequest(model string, endpointType string) dto.Request {
	// 根据端点类型构建不同的测试请求
	if endpointType != "" {
		switch constant.EndpointType(endpointType) {
		case constant.EndpointTypeDecisions:
			return &dto.DecisionRequest{Model: model, State: json.RawMessage(`"The item is a red apple."`), Questions: map[string]json.RawMessage{"red_apple": json.RawMessage(`{"type":"noul","instructions":"Is the item a red apple?"}`)}}
		case constant.EndpointTypeEmbeddings:
			// 返回 EmbeddingRequest
			return &dto.EmbeddingRequest{
				Model: model,
				Input: []any{"hello world"},
			}
		case constant.EndpointTypeImageGeneration:
			// 返回 ImageRequest
			return &dto.ImageRequest{
				Model:  model,
				Prompt: "a cute cat",
				N:      1,
				Size:   dto.SeedreamPresetSize(model, "1024x1024"),
			}
		case constant.EndpointTypeVideoGeneration:
			return &dto.VideoRequest{
				Model:       model,
				Prompt:      "a short product reveal shot",
				Seconds:     5,
				AspectRatio: "16:9",
				Quality:     "standard",
			}
		case constant.EndpointTypeJinaRerank:
			// 返回 RerankRequest
			return &dto.RerankRequest{
				Model:     model,
				Query:     "What is Deep Learning?",
				Documents: []any{"Deep Learning is a subset of machine learning.", "Machine learning is a field of artificial intelligence."},
				TopN:      2,
			}
		case constant.EndpointTypeOpenAIResponse:
			// 返回 OpenAIResponsesRequest
			return &dto.OpenAIResponsesRequest{
				Model: model,
				Input: json.RawMessage("\"hi\""),
			}
		case constant.EndpointTypeAnthropic, constant.EndpointTypeGemini, constant.EndpointTypeOpenAI:
			// 返回 GeneralOpenAIRequest
			maxTokens := uint(10)
			if constant.EndpointType(endpointType) == constant.EndpointTypeGemini {
				maxTokens = 3000
			}
			return &dto.GeneralOpenAIRequest{
				Model:  model,
				Stream: false,
				Messages: []dto.Message{
					{
						Role:    "user",
						Content: "hi",
					},
				},
				MaxTokens: maxTokens,
			}
		}
	}

	// 自动检测逻辑（保持原有行为）
	// 先判断是否为 Embedding 模型
	if strings.Contains(strings.ToLower(model), "embedding") ||
		strings.HasPrefix(model, "m3e") ||
		strings.Contains(model, "bge-") {
		// 返回 EmbeddingRequest
		return &dto.EmbeddingRequest{
			Model: model,
			Input: []any{"hello world"},
		}
	}
	if common.IsVideoGenerationModel(model) {
		return &dto.VideoRequest{
			Model:       model,
			Prompt:      "a short product reveal shot",
			Seconds:     5,
			AspectRatio: "16:9",
			Quality:     "standard",
		}
	}

	// Chat/Completion 请求 - 返回 GeneralOpenAIRequest
	testRequest := &dto.GeneralOpenAIRequest{
		Model:  model,
		Stream: false,
		Messages: []dto.Message{
			{
				Role:    "user",
				Content: "hi",
			},
		},
	}

	if strings.HasPrefix(model, "o") {
		testRequest.MaxCompletionTokens = 10
	} else if strings.Contains(model, "thinking") {
		if !strings.Contains(model, "claude") {
			testRequest.MaxTokens = 50
		}
	} else if strings.Contains(model, "gemini") {
		testRequest.MaxTokens = 3000
	} else {
		testRequest.MaxTokens = 10
	}

	return testRequest
}

func TestChannel(c *gin.Context) {
	channelId, err := resolveChannelID(c.Param("id"))
	if err != nil {
		httperr.Write(c, err)
		return
	}
	channel, err := model.CacheGetChannel(channelId)
	if err != nil {
		channel, err = model.GetChannelById(channelId, true)
		if err != nil {
			httperr.Write(c, err)
			return
		}
	}
	if constant.IsSearchChannelType(channel.Type) {
		rejectSearchBackendChannelMutation(c, "edit")
		return
	}
	if channel.GetOtherSettings().CodexClientOnly {
		// A Codex-only source is validated by a real Codex session; the
		// generic probe is skipped, not failed.
		common.ApiSuccess(c, gin.H{"skipped": true, "time": 0.0})
		return
	}
	testModel := c.Query("model")
	endpointType := c.Query("endpoint_type")
	tik := time.Now()
	result := testChannel(channel, testModel, endpointType)
	milliseconds := time.Since(tik).Milliseconds()
	consumedTime := float64(milliseconds) / 1000.0
	if result.localErr != nil {
		// The relay sets localErr on upstream failures too; neither kind
		// records a response time.
		httperr.Write(c, channelTestFailure(result, consumedTime))
		return
	}
	go channel.UpdateResponseTime(milliseconds)
	if result.lazuErr != nil {
		httperr.Write(c, channelTestFailure(result, consumedTime))
		return
	}
	common.ApiSuccess(c, gin.H{"time": consumedTime})
}

// channelTestFailure turns a failed test into its catalog error. A test that
// reached the provider failed there (channel_test_failed, carrying the
// provider's classified code); one that never left this process could not be
// built for the channel (channel_test_not_sent). The full cause, which may
// hold upstream text, is logged with the request ID and never sent.
func channelTestFailure(result testResult, seconds float64) error {
	if result.lazuErr == nil && result.localErr == nil {
		return nil
	}
	cause := result.localErr
	if result.lazuErr != nil {
		cause = result.lazuErr
	}
	if !result.providerAttempted {
		return types.WrapErr(cause, types.ErrorCodeChannelTestNotSent)
	}
	details := types.D{"seconds": seconds}
	if result.lazuErr != nil {
		wire := result.lazuErr.Wire()
		details["cause_code"] = string(wire.Code)
		details["cause_lazu"] = wire.ID
		details["status"] = result.lazuErr.StatusCode
	}
	return &types.APIError{Code: types.ErrorCodeChannelTestFailed, Details: details, Cause: cause}
}

var testAllChannelsLock sync.Mutex
var testAllChannelsRunning bool = false

func acquireAllChannelsTest() error {
	testAllChannelsLock.Lock()
	defer testAllChannelsLock.Unlock()
	if testAllChannelsRunning {
		return types.Err(types.ErrorCodeChannelTaskRunning, types.D{"task": "channel_test"})
	}
	testAllChannelsRunning = true
	return nil
}

func releaseAllChannelsTest() {
	testAllChannelsLock.Lock()
	testAllChannelsRunning = false
	testAllChannelsLock.Unlock()
}

func loadAllChannelsForTest() ([]*model.Channel, int64, error) {
	channels, getChannelErr := model.GetAllChannels(0, 0, true, false)
	if getChannelErr != nil {
		return nil, 0, getChannelErr
	}
	var disableThreshold = int64(common.ChannelDisableThreshold * 1000)
	if disableThreshold == 0 {
		disableThreshold = 10000000 // a impossible value
	}
	return channels, disableThreshold, nil
}

func runAllChannelTests(channels []*model.Channel, disableThreshold int64, notify bool) {
	for _, channel := range channels {
		if constant.IsSearchChannelType(channel.Type) || channel.GetOtherSettings().CodexClientOnly {
			continue
		}
		isChannelEnabled := channel.Status == common.ChannelStatusEnabled
		tik := time.Now()
		result := testChannel(channel, "", "")
		tok := time.Now()
		milliseconds := tok.Sub(tik).Milliseconds()

		shouldBanChannel := false
		lazuErr := result.lazuErr
		if lazuErr != nil {
			shouldBanChannel = service.ShouldDisableChannel(channel.Type, result.lazuErr)
		}

		if common.AutomaticDisableChannelEnabled && !shouldBanChannel {
			if milliseconds > disableThreshold {
				responseSeconds := float64(milliseconds) / 1000.0
				thresholdSeconds := float64(disableThreshold) / 1000.0
				err := fmt.Errorf("response time %.2fs exceeded the %.2fs threshold", responseSeconds, thresholdSeconds)
				lazuErr = types.NewOpenAIError(err, types.ErrorCodeChannelResponseTimeExceeded, http.StatusRequestTimeout,
					types.ErrOptionWithDetails(map[string]any{
						"response_seconds":  responseSeconds,
						"threshold_seconds": thresholdSeconds,
					}))
				shouldBanChannel = true
			}
		}

		if result.localErr != nil && result.lazuErr == nil {
			common.SysLog(fmt.Sprintf("skip channel %d response-time update after local test error: %s", channel.Id, result.localErr.Error()))
			time.Sleep(common.RequestInterval)
			continue
		}

		if isChannelEnabled && shouldBanChannel && channel.GetAutoBan() {
			processChannelError(result.context, *types.NewChannelError(channel.Id, channel.Type, channel.Name, channel.ChannelInfo.IsMultiKey, common.GetContextKeyString(result.context, constant.ContextKeyChannelKey), channel.GetAutoBan()), lazuErr, false)
		}

		if !isChannelEnabled && service.ShouldEnableChannel(lazuErr, channel.Status) {
			service.EnableChannel(channel.Id, common.GetContextKeyString(result.context, constant.ContextKeyChannelKey), channel.Name)
		}

		channel.UpdateResponseTime(milliseconds)
		time.Sleep(common.RequestInterval)
	}

	if notify {
	}
}

func testAllChannels(notify bool) error {
	if err := acquireAllChannelsTest(); err != nil {
		return err
	}
	channels, disableThreshold, err := loadAllChannelsForTest()
	if err != nil {
		releaseAllChannelsTest()
		return err
	}
	common.SafeGo("test-all-channels", func() {
		defer releaseAllChannelsTest()
		runAllChannelTests(channels, disableThreshold, notify)
	})
	return nil
}

// RunChannelAutoTestOnce performs one synchronous auto-test pass. External
// schedulers use it so the process cannot exit before channel checks finish.
func RunChannelAutoTestOnce() error {
	if !operation_setting.GetMonitorSetting().AutoTestChannelEnabled {
		return nil
	}
	if err := acquireAllChannelsTest(); err != nil {
		return err
	}
	defer releaseAllChannelsTest()
	channels, disableThreshold, err := loadAllChannelsForTest()
	if err != nil {
		return err
	}
	runAllChannelTests(channels, disableThreshold, false)
	return nil
}

func TestAllChannels(c *gin.Context) {
	err := testAllChannels(true)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
}

var autoTestChannelsOnce sync.Once

func AutomaticallyTestChannels() {
	autoTestChannelsOnce.Do(func() {
		for {
			if !operation_setting.GetMonitorSetting().AutoTestChannelEnabled {
				time.Sleep(10 * time.Minute)
				continue
			}
			for {
				frequency := operation_setting.GetMonitorSetting().AutoTestChannelMinutes
				time.Sleep(time.Duration(frequency) * time.Minute)
				common.SysLog(fmt.Sprintf("automatically test channels with interval %d minutes", frequency))
				common.SysLog("automatically testing all channels")
				_ = testAllChannels(false)
				common.SysLog("automatically channel test finished")
				if !operation_setting.GetMonitorSetting().AutoTestChannelEnabled {
					break
				}
			}
		}
	})
}

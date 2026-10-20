package claude

import (
	"strings"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/model"
	textgenruntime "github.com/lazu-ai/lazu/internal/protocol/textgen/runtime"
	"github.com/lazu-ai/lazu/internal/setting/model_setting"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const (
	ThinkingMinBudgetTokens = 1024
	ThinkingMinMaxTokens    = 1280
	// thinkingBudgetHeadroom is the least room left under max_tokens for the
	// answer when a budget has to fit under the model's output limit.
	thinkingBudgetHeadroom = 1024
)

// ModelPolicy is what the catalog says about the model a Claude request goes
// to: how it takes its thinking controls and how long its replies may be.
// Converters take it as a value and never look a model up themselves; the
// adaptor and the native handler resolve it through CatalogPolicy, tests
// pass one directly.
type ModelPolicy struct {
	Reasoning model.ModelReasoningOptions
	// MaxOutputTokens is the catalog's output limit, 0 when it names none.
	MaxOutputTokens int
	// Known is whether the catalog has a row for the model at all.
	Known bool
}

// Adaptive is whether the model takes thinking.type=adaptive with the effort
// in output_config, as Claude 4.6 and later do. A model the catalog does not
// describe keeps the budget shape it was sent before Mode existed.
func (p ModelPolicy) Adaptive() bool { return p.Reasoning.Mode == model.ReasoningModeAdaptive }

// PolicyResolver gives the policy for a model name. Converters call it for
// the name a request carries and for the base of a suffixed name
// (claude-opus-4-7-xhigh), which only a resolver can tell is a model.
type PolicyResolver func(modelName string) ModelPolicy

// CatalogPolicy resolves policies from the model catalog. originModel is the
// name the client sent; a channel may map it to another upstream name, and
// the catalog row is on the client's. Each lookup tries the name asked, then
// the origin. Descriptor is the lookup the planner already makes per
// attempt, so this adds no request-path round trip.
func CatalogPolicy(originModel string) PolicyResolver {
	return policyResolver(originModel, model.ReasoningPolicy, textgenruntime.Descriptor)
}

func policyResolver(originModel string, reasoning func(...string) model.ModelReasoningOptions, describe func(string) textgenruntime.ModelDescriptor) PolicyResolver {
	return func(name string) ModelPolicy {
		names := []string{name}
		if originModel != "" && originModel != name {
			names = append(names, originModel)
		}
		policy := ModelPolicy{Reasoning: reasoning(names...)}
		for _, candidate := range names {
			descriptor := describe(candidate)
			if !descriptor.Found {
				continue
			}
			policy.MaxOutputTokens = int(descriptor.MaxOutputTokens)
			policy.Known = true
			break
		}
		policy.Known = policy.Known || !policy.Reasoning.IsZero()
		return policy
	}
}

// StaticPolicy is a resolver that answers policy for every name; for tests
// and for callers that resolved the policy already.
func StaticPolicy(policy ModelPolicy) PolicyResolver {
	return func(string) ModelPolicy { return policy }
}

func parseThinkingSuffix(model string) string {
	if strings.HasSuffix(model, "-thinking") {
		return "-thinking"
	}
	if strings.HasSuffix(model, "#thinking") {
		return "#thinking"
	}
	return ""
}

func TrimThinkingSuffix(model string) (string, string) {
	suffix := parseThinkingSuffix(model)
	if suffix == "" {
		return model, ""
	}
	return strings.TrimSuffix(model, suffix), suffix
}

func TrimEffortSuffix(model string) (string, string, bool) {
	for _, suffix := range []string{"-max", "-xhigh", "-high", "-medium", "-low", "-minimal"} {
		if strings.HasSuffix(model, suffix) {
			return strings.TrimSuffix(model, suffix), strings.TrimPrefix(suffix, "-"), true
		}
	}
	return model, "", false
}

// ApplyAdaptiveThinking asks the model to think adaptively at effort. The
// thinking text is still shown (display "summarized"): Opus 4.7 and later
// leave it out of adaptive thinking unless asked. Sampling parameters are
// left out, as Anthropic refuses them beside adaptive thinking.
func ApplyAdaptiveThinking(request *dto.ClaudeRequest, effort string) {
	if request == nil {
		return
	}
	if effort == "" {
		effort = "high"
	}
	display := "summarized"
	if request.Thinking != nil && request.Thinking.Display != "" {
		display = request.Thinking.Display
	}
	request.Thinking = &dto.Thinking{Type: "adaptive", Display: display}
	request.OutputConfig = setOutputConfigEffort(request.OutputConfig, effort)
	request.Temperature = nil
	request.TopP = 0
	request.TopK = 0
}

// EffortOfBudget is the effort nearest a thinking budget, for a model that
// takes an effort where the client sent a budget. The rungs are the ones
// model.EffortBudgetTokens maps the other way.
func EffortOfBudget(budgetTokens int) string {
	switch {
	case budgetTokens <= 0:
		return ""
	case budgetTokens <= 4096:
		return "low"
	case budgetTokens <= 8192:
		return "medium"
	case budgetTokens <= 16384:
		return "high"
	case budgetTokens <= 24576:
		return "xhigh"
	}
	return "max"
}

// NormalizeThinking makes a request's thinking field the shape its model
// takes: a budget sent to an adaptive model becomes adaptive at the effort
// the budget is nearest (or the effort the request names), and thinking
// turned off goes as the model takes that (model.ClaudeThinkingOff). A
// request to a model the catalog does not describe is left as it came.
func NormalizeThinking(request *dto.ClaudeRequest, policy ModelPolicy) {
	if request == nil || request.Thinking == nil {
		return
	}
	switch request.Thinking.Type {
	case "enabled":
		if !policy.Adaptive() {
			return
		}
		effort := outputConfigEffort(request.OutputConfig)
		if effort == "" {
			effort = model.SnapEffort(request.Model, EffortOfBudget(request.Thinking.GetBudgetTokens()))
			if effort == "none" {
				effort = "low"
			}
		}
		ApplyAdaptiveThinking(request, effort)
	case "disabled", "between_tools":
		thinkingType, effort := model.ClaudeThinkingOff(policy.Reasoning, request.Thinking.Type, outputConfigEffort(request.OutputConfig))
		if thinkingType == "" {
			request.Thinking = nil
		} else {
			request.Thinking = &dto.Thinking{Type: thinkingType}
		}
		if effort != "" {
			request.OutputConfig = setOutputConfigEffort(request.OutputConfig, effort)
		}
	}
}

func EffortToPercentage(effort string) float64 {
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "high":
		return 0.8
	case "medium":
		return 0.5
	case "low":
		return 0.2
	default:
		return 0
	}
}

// EnsureThinkingConstraints applies Anthropic extended thinking constraints.
// It only mutates the request when thinking is enabled.
func EnsureThinkingConstraints(request *dto.ClaudeRequest) {
	if request == nil || request.Thinking == nil {
		return
	}

	// An adaptive request is either one lazu built (ApplyAdaptiveThinking
	// chose its display and left sampling out) or a Claude client's own ask,
	// which goes as written.
	if request.Thinking.Type != "enabled" {
		return
	}

	if request.MaxTokens < ThinkingMinMaxTokens {
		request.MaxTokens = ThinkingMinMaxTokens
	}

	budgetTokens := request.Thinking.GetBudgetTokens()
	if budgetTokens <= 0 {
		percentage := model_setting.GetClaudeSettings().ThinkingAdapterBudgetTokensPercentage
		budgetTokens = int(float64(request.MaxTokens) * percentage)
	}

	if budgetTokens < ThinkingMinBudgetTokens {
		budgetTokens = ThinkingMinBudgetTokens
	}
	if request.MaxTokens < uint(budgetTokens) {
		request.MaxTokens = uint(budgetTokens)
	}

	request.Thinking.BudgetTokens = common.GetPointer[int](budgetTokens)

	// https://docs.anthropic.com/en/docs/build-with-claude/extended-thinking#important-considerations-when-using-extended-thinking
	request.TopP = 0
	request.Temperature = common.GetPointer[float64](1.0)
}

// FitThinkingBudget keeps a thinking budget under the catalog's output limit
// with room for the answer: the budget is lazu's own number (a percentage,
// or an effort's budget), and a budget at the limit leaves the model nothing
// to answer with. max_tokens is the client's and is not lowered; its
// relation to the budget is EnsureThinkingConstraints'. A request with no
// budget, a policy naming no limit, or a budget that fits is left as it is.
func FitThinkingBudget(request *dto.ClaudeRequest, policy ModelPolicy) {
	if request == nil || request.Thinking == nil || request.Thinking.Type != "enabled" || request.Thinking.BudgetTokens == nil || policy.MaxOutputTokens <= 0 {
		return
	}
	limit := uint(policy.MaxOutputTokens)
	if budget := uint(*request.Thinking.BudgetTokens); budget+thinkingBudgetHeadroom > limit {
		next := int(limit) - thinkingBudgetHeadroom
		if next < ThinkingMinBudgetTokens {
			next = ThinkingMinBudgetTokens
		}
		request.Thinking.BudgetTokens = common.GetPointer[int](next)
	}
}

func GetDefaultMaxTokensForThinkingModel(originalModel, normalizedModel string) int {
	settings := model_setting.GetClaudeSettings()
	if maxTokens, ok := settings.DefaultMaxTokens[originalModel]; ok {
		return maxTokens
	}
	return settings.GetDefaultMaxTokens(normalizedModel)
}

func outputConfigEffort(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	return gjson.GetBytes(raw, "effort").String()
}

func setOutputConfigEffort(raw []byte, effort string) []byte {
	if len(raw) == 0 || !gjson.ValidBytes(raw) {
		raw = []byte(`{}`)
	}
	next, err := sjson.SetBytes(raw, "effort", effort)
	if err != nil {
		return raw
	}
	return next
}

// PrepareNativeThinking readies a Messages request a Claude client sent for
// the model it goes to: an effort or thinking suffix on the model name is
// taken off and applied, a request naming no max_tokens gets the default,
// and the thinking field takes the shape the model's policy says
// (NormalizeThinking, EnsureThinkingConstraints, CapOutputToCatalog). The
// request's Model is the upstream name when it returns.
func PrepareNativeThinking(request *dto.ClaudeRequest, resolve PolicyResolver) {
	if request == nil {
		return
	}
	if resolve == nil {
		resolve = StaticPolicy(ModelPolicy{})
	}
	originalModel := request.Model
	suffixEffort := ""
	if baseModel, effort, ok := TrimEffortSuffix(request.Model); ok && resolve(baseModel).Known {
		request.Model = baseModel
		suffixEffort = effort
	}
	suffixThinking := false
	if model_setting.GetClaudeSettings().ThinkingAdapterEnabled {
		if base, suffix := TrimThinkingSuffix(request.Model); suffix != "" {
			suffixThinking = true
			request.Model = base
		}
	}
	policy := resolve(request.Model)
	if request.MaxTokens == 0 {
		request.MaxTokens = uint(GetDefaultMaxTokensForThinkingModel(originalModel, request.Model))
	}
	switch {
	case request.Thinking == nil && (suffixEffort != "" || suffixThinking):
		if policy.Adaptive() {
			ApplyAdaptiveThinking(request, suffixEffort)
		} else {
			budgetTokens := int(float64(request.MaxTokens) * model_setting.GetClaudeSettings().ThinkingAdapterBudgetTokensPercentage)
			request.Thinking = &dto.Thinking{Type: "enabled", BudgetTokens: &budgetTokens}
		}
	case suffixEffort != "" && request.Thinking.Type == "enabled" && policy.Adaptive():
		// the suffix names the effort; a budget the client sent beside it
		// would otherwise pick one
		ApplyAdaptiveThinking(request, suffixEffort)
	}
	NormalizeThinking(request, policy)
	EnsureThinkingConstraints(request)
	FitThinkingBudget(request, policy)
}

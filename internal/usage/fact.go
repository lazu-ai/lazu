package usage

import (
	"encoding/json"
	"strings"

	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/pkg/billing"
)

const FactVersion = 1

type ProviderFamily string

const (
	ProviderFamilyUnknown          ProviderFamily = ""
	ProviderFamilyOpenAI           ProviderFamily = "openai"
	ProviderFamilyAnthropic        ProviderFamily = "anthropic"
	ProviderFamilyGemini           ProviderFamily = "gemini"
	ProviderFamilyQwen             ProviderFamily = "qwen"
	ProviderFamilyDeepSeek         ProviderFamily = "deepseek"
	ProviderFamilyOpenRouter       ProviderFamily = "openrouter"
	ProviderFamilyTypeSafe         ProviderFamily = "typesafe"
	ProviderFamilyCloudflare       ProviderFamily = "cloudflare"
	ProviderFamilyXAI              ProviderFamily = "xai"
	ProviderFamilyOpenAICompatible ProviderFamily = "openai_compatible"
)

type ProviderHint struct {
	APIType           int
	ChannelType       int
	ModelName         string
	UpstreamModelName string
	ProviderName      string
	BaseURL           string
}

type Fact struct {
	ImageUsage       *billing.ImageUsage   `json:"image_usage,omitempty"`
	Version          int                   `json:"version"`
	RequestID        string                `json:"request_id,omitempty"`
	SessionID        string                `json:"session_id,omitempty"`
	AgentID          string                `json:"agent_id,omitempty"`
	Source           string                `json:"source,omitempty"`
	UserID           int                   `json:"user_id,omitempty"`
	TokenID          int                   `json:"token_id,omitempty"`
	ChannelID        int                   `json:"channel_id,omitempty"`
	ModelName        string                `json:"model_name,omitempty"`
	ProviderFamily   string                `json:"provider_family,omitempty"`
	Measurement      *dto.UsageMeasurement `json:"measurement,omitempty"`
	PromptTokens     int                   `json:"prompt_tokens"`
	CompletionTokens int                   `json:"completion_tokens"`
	TotalTokens      int                   `json:"total_tokens"`
	InputTokens      int                   `json:"input_tokens,omitempty"`
	OutputTokens     int                   `json:"output_tokens,omitempty"`
	Dimensions       []DimensionUsage      `json:"dimensions,omitempty"`
	CustomerCostUSD  *float64              `json:"customer_cost_usd,omitempty"`
	ProviderCostUSD  *float64              `json:"provider_cost_usd,omitempty"`
	GrossMarginUSD   *float64              `json:"gross_margin_usd,omitempty"`
	MediaUnits       *MediaUnits           `json:"media_units,omitempty"`
	ProviderRawUsage map[string]any        `json:"provider_raw_usage,omitempty"`
}

type MediaUnits struct {
	ImageCount     int64  `json:"image_count,omitempty"`
	VideoSeconds   int64  `json:"video_seconds,omitempty"`
	ResolutionTier string `json:"resolution_tier,omitempty"`
	ProviderUnit   string `json:"provider_unit,omitempty"`
	// Video facts the provider reported: its own token count (Ark), the
	// price it charged (aggregators), frame rate and whether audio was made.
	OutputTokens    int64    `json:"output_tokens,omitempty"`
	ReportedCostUSD *float64 `json:"reported_cost_usd,omitempty"`
	FPS             int      `json:"fps,omitempty"`
	Audio           bool     `json:"audio,omitempty"`
	Reported        bool     `json:"reported,omitempty"`
}

func FactFromDTO(usage *dto.Usage) Fact {
	fact := FactFromSummary(SummaryFromDTO(usage))
	measurement := usage.Measurement()
	fact.Measurement = &measurement
	if usage != nil {
		fact.ImageUsage = usage.ImageUsage
	}
	return fact
}

func FactFromDTOAndFields(usage *dto.Usage, fields map[string]interface{}) Fact {
	return FactFromProviderUsage(usage, fields, ProviderFamilyOpenAICompatible)
}

func FactFromProviderUsage(usage *dto.Usage, fields map[string]interface{}, providerFamily ProviderFamily) Fact {
	providerFamily = normalizeProviderFamily(providerFamily)
	if providerFamily == ProviderFamilyUnknown {
		providerFamily = ProviderFamilyOpenAICompatible
	}

	var fact Fact
	switch providerFamily {
	case ProviderFamilyAnthropic:
		fact = factFromProviderSeparatedCacheUsage(usage, fields, providerFamily)
	default:
		fact = factFromOpenAICompatibleUsage(usage, fields, providerFamily)
	}
	measurement := usage.Measurement()
	fact.Measurement = &measurement
	if usage != nil {
		fact.ImageUsage = usage.ImageUsage
	}
	return fact
}

func FactFromRealtimeUsage(usage *dto.RealtimeUsage, fields map[string]interface{}, providerFamily ProviderFamily) Fact {
	providerFamily = normalizeProviderFamily(providerFamily)
	if providerFamily == ProviderFamilyUnknown {
		providerFamily = ProviderFamilyOpenAI
	}
	if usage == nil {
		fact := Fact{Version: FactVersion, ProviderFamily: string(providerFamily), ProviderRawUsage: RawUsageFields(fields)}
		fact.mergeFieldDimensions(fields)
		return fact.Normalize()
	}

	fact := FactFromSummary(SummaryFromRealtime(usage))
	fact.ProviderFamily = string(providerFamily)
	fact.ProviderRawUsage = RawUsageFields(fields)
	fact.mergeFieldDimensions(fields)
	return fact.Normalize()
}

func FactFromSummary(summary Summary) Fact {
	fact := Fact{
		Version:          FactVersion,
		PromptTokens:     summary.PromptTokens,
		CompletionTokens: summary.CompletionTokens,
		TotalTokens:      summary.TotalTokens,
		InputTokens:      summary.InputTokens,
		OutputTokens:     summary.OutputTokens,
		Dimensions:       append([]DimensionUsage(nil), summary.Dimensions...),
	}
	return fact.Normalize()
}

func FactFromLegacyLogFields(promptTokens int, completionTokens int, fields map[string]interface{}) Fact {
	return factFromLogFields(promptTokens, completionTokens, fields, LegacyFieldsUseSeparatedCacheInput(fields))
}

func FactFromLogFields(promptTokens int, completionTokens int, fields map[string]interface{}, providerSeparatedCacheInput bool) Fact {
	return factFromLogFields(promptTokens, completionTokens, fields, providerSeparatedCacheInput)
}

func factFromLogFields(promptTokens int, completionTokens int, fields map[string]interface{}, providerSeparatedCacheInput bool) Fact {
	cacheReadTokens := firstPositiveIntFromFields(fields,
		"cache_tokens",
		"cached_tokens",
		"prompt_cache_hit_tokens",
	)
	cacheWriteTotal := firstPositiveIntFromFields(fields,
		"cache_write_tokens",
		"cache_creation_input_tokens",
		"cache_creation_tokens",
	)
	cacheWrite5mTokens := firstPositiveIntFromFields(fields,
		"cache_write_5m_tokens",
		"cache_creation_5m_tokens",
	)
	cacheWrite1hTokens := firstPositiveIntFromFields(fields,
		"cache_write_1h_tokens",
		"cache_creation_1h_tokens",
	)
	cacheMissTokens := firstPositiveIntFromFields(fields,
		"cache_miss_tokens",
		"prompt_cache_miss_tokens",
	)
	audioInputTokens := firstPositiveIntFromFields(fields, "audio_input_tokens", "audio_input")
	audioOutputTokens := firstPositiveIntFromFields(fields, "audio_output_tokens", "audio_output")
	imageInputTokens := firstPositiveIntFromFields(fields, "image_input_tokens", "image_input", "image_output")

	inputTokens := promptTokens - audioInputTokens - imageInputTokens
	if !providerSeparatedCacheInput {
		inputTokens -= cacheReadTokens + cacheWriteTotal
	}
	if inputTokens < 0 {
		inputTokens = 0
	}
	outputTokens := completionTokens - audioOutputTokens
	if outputTokens < 0 {
		outputTokens = 0
	}

	cacheWriteUnknown := cacheWriteTotal - cacheWrite5mTokens - cacheWrite1hTokens
	if cacheWriteUnknown < 0 {
		cacheWriteUnknown = 0
	}

	fact := Fact{
		Version:          FactVersion,
		ProviderFamily:   string(providerFamilyFromFields(fields)),
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		TotalTokens:      promptTokens + completionTokens,
		InputTokens:      inputTokens,
		OutputTokens:     outputTokens,
		ProviderRawUsage: RawUsageFields(fields),
	}
	fact.addDimension(DimensionInput, int64(inputTokens), UnitToken)
	fact.addDimension(DimensionCacheRead, int64(cacheReadTokens), UnitToken)
	fact.addDimension(DimensionCacheWrite, int64(cacheWriteUnknown), UnitToken)
	fact.addDimension(DimensionCacheWrite5m, int64(cacheWrite5mTokens), UnitToken)
	fact.addDimension(DimensionCacheWrite1h, int64(cacheWrite1hTokens), UnitToken)
	fact.addDimension(DimensionCacheMiss, int64(cacheMissTokens), UnitToken)
	fact.addDimension(DimensionAudioInput, int64(audioInputTokens), UnitToken)
	fact.addDimension(DimensionAudioOutput, int64(audioOutputTokens), UnitToken)
	fact.addDimension(DimensionImageInput, int64(imageInputTokens), UnitToken)
	fact.addDimension(DimensionOutput, int64(outputTokens), UnitToken)
	fact.mergeFieldDimensions(fields)
	return fact.Normalize()
}

func factFromOpenAICompatibleUsage(usage *dto.Usage, fields map[string]interface{}, providerFamily ProviderFamily) Fact {
	fact := FactFromDTO(usage)
	fact.ProviderFamily = string(providerFamily)
	fact.mergeFieldDimensions(fields)
	fact.ProviderRawUsage = RawUsageFields(fields)
	return fact.Normalize()
}

func factFromProviderSeparatedCacheUsage(usage *dto.Usage, fields map[string]interface{}, providerFamily ProviderFamily) Fact {
	if usage == nil {
		fact := Fact{Version: FactVersion, ProviderFamily: string(providerFamily), ProviderRawUsage: RawUsageFields(fields)}
		fact.mergeFieldDimensions(fields)
		return fact.Normalize()
	}
	NormalizeDTOUsageFields(usage)
	details := usage.PromptTokensDetails
	cacheWrite5m := details.CachedCreation5mTokens
	cacheWrite1h := details.CachedCreation1hTokens
	cacheWriteTotal := details.CacheWriteTokens
	if cacheWriteTotal == 0 {
		cacheWriteTotal = details.CachedCreationTokens
	}
	if cacheWriteTotal == 0 {
		cacheWriteTotal = cacheWrite5m + cacheWrite1h
	}
	cacheWriteUnknown := cacheWriteTotal - cacheWrite5m - cacheWrite1h
	if cacheWriteUnknown < 0 {
		cacheWriteUnknown = 0
	}
	inputTokens := usage.PromptTokens - details.AudioTokens - details.ImageTokens
	if inputTokens < 0 {
		inputTokens = 0
	}
	outputTokens := usage.CompletionTokens - usage.CompletionTokenDetails.AudioTokens - usage.CompletionTokenDetails.ImageTokens
	if outputTokens < 0 {
		outputTokens = 0
	}
	totalTokens := usage.TotalTokens
	if totalTokens == 0 {
		totalTokens = usage.PromptTokens + usage.CompletionTokens
	}

	fact := Fact{
		Version:          FactVersion,
		ProviderFamily:   string(providerFamily),
		PromptTokens:     usage.PromptTokens,
		CompletionTokens: usage.CompletionTokens,
		TotalTokens:      totalTokens,
		InputTokens:      inputTokens,
		OutputTokens:     outputTokens,
		ProviderRawUsage: RawUsageFields(fields),
	}
	fact.addDimension(DimensionInput, int64(inputTokens), UnitToken)
	fact.addDimension(DimensionCacheRead, int64(details.CachedTokens), UnitToken)
	fact.addDimension(DimensionCacheWrite, int64(cacheWriteUnknown), UnitToken)
	fact.addDimension(DimensionCacheWrite5m, int64(cacheWrite5m), UnitToken)
	fact.addDimension(DimensionCacheWrite1h, int64(cacheWrite1h), UnitToken)
	fact.addDimension(DimensionCacheMiss, int64(details.CacheMissTokens), UnitToken)
	fact.addDimension(DimensionAudioInput, int64(details.AudioTokens), UnitToken)
	fact.addDimension(DimensionImageInput, int64(details.ImageTokens), UnitToken)
	fact.addDimension(DimensionOutput, int64(outputTokens), UnitToken)
	fact.addDimension(DimensionAudioOutput, int64(usage.CompletionTokenDetails.AudioTokens), UnitToken)
	fact.addDimension(DimensionImageOutput, int64(usage.CompletionTokenDetails.ImageTokens), UnitToken)
	fact.mergeFieldDimensions(fields)
	return fact.Normalize()
}

func ParseFact(raw string) (Fact, bool) {
	if strings.TrimSpace(raw) == "" {
		return Fact{}, false
	}
	var fact Fact
	if err := json.Unmarshal([]byte(raw), &fact); err != nil {
		return Fact{}, false
	}
	fact = fact.Normalize()
	if fact.IsZero() {
		return Fact{}, false
	}
	return fact, true
}

func (f Fact) JSONString() string {
	f = f.Normalize()
	if f.IsZero() {
		return ""
	}
	body, err := json.Marshal(f)
	if err != nil {
		return ""
	}
	return string(body)
}

func (f Fact) Normalize() Fact {
	if f.Version == 0 {
		f.Version = FactVersion
	}
	if f.ImageUsage != nil {
		f.ensureDimensionAtLeast(DimensionImageCount, int64(len(f.ImageUsage.Outputs)), UnitImage)
		f.ensureDimensionAtLeast("input_images", int64(f.ImageUsage.InputImages), UnitImage)
	}
	if f.MediaUnits != nil {
		if f.MediaUnits.ImageCount > 0 {
			f.ensureDimensionAtLeast(DimensionImageCount, f.MediaUnits.ImageCount, UnitImage)
			f.ensureDimensionAtLeast(DimensionRequest, f.MediaUnits.ImageCount, UnitRequest)
		}
		if f.MediaUnits.VideoSeconds > 0 {
			f.ensureDimensionAtLeast(DimensionVideoSeconds, f.MediaUnits.VideoSeconds, UnitSecond)
			f.ensureDimensionAtLeast(DimensionSecond, f.MediaUnits.VideoSeconds, UnitSecond)
		}
		if f.MediaUnits.OutputTokens > 0 {
			f.ensureDimensionAtLeast(DimensionVideoTokens, f.MediaUnits.OutputTokens, UnitToken)
		}
		if f.MediaUnits.ImageCount <= 0 && f.MediaUnits.VideoSeconds <= 0 && f.MediaUnits.ResolutionTier == "" &&
			f.MediaUnits.ProviderUnit == "" && f.MediaUnits.OutputTokens <= 0 && f.MediaUnits.ReportedCostUSD == nil {
			f.MediaUnits = nil
		}
	}
	if f.TotalTokens == 0 {
		f.TotalTokens = f.PromptTokens + f.CompletionTokens
	}
	return f
}

func (f Fact) IsZero() bool {
	return f.PromptTokens == 0 &&
		f.CompletionTokens == 0 &&
		f.TotalTokens == 0 &&
		f.InputTokens == 0 &&
		f.OutputTokens == 0 &&
		len(f.Dimensions) == 0 &&
		f.MediaUnits == nil && f.ImageUsage == nil
}

func (f Fact) UsageRequestUsage() dto.UsageRequestUsage {
	f = f.Normalize()
	dimensions := make([]dto.UsageDimension, 0, len(f.Dimensions))
	for _, dimension := range f.Dimensions {
		if dimension.Name == "" || dimension.Quantity <= 0 {
			continue
		}
		dimensions = append(dimensions, dto.UsageDimension{
			Name:     dimension.Name,
			Quantity: dimension.Quantity,
			Unit:     dimension.Unit,
		})
	}
	return dto.UsageRequestUsage{
		Images:           f.ImageUsage,
		Measurement:      f.Measurement,
		PromptTokens:     f.PromptTokens,
		CompletionTokens: f.CompletionTokens,
		TotalTokens:      f.TotalTokens,
		Dimensions:       dimensions,
	}
}

func (f *Fact) mergeFieldDimensions(fields map[string]interface{}) {
	if f == nil {
		return
	}
	f.addDimension(DimensionWebSearch, int64(intFromFields(fields, "web_search_call_count")), UnitRequest)
	f.addDimension(DimensionFileSearch, int64(intFromFields(fields, "file_search_call_count")), UnitRequest)
	if boolFromFields(fields, "image_generation_call") {
		f.addDimension(DimensionImageGeneration, 1, UnitRequest)
	}
}

func (f *Fact) addDimension(name string, quantity int64, unit string) {
	if f == nil || name == "" || quantity <= 0 {
		return
	}
	for idx := range f.Dimensions {
		if f.Dimensions[idx].Name == name {
			f.Dimensions[idx].Quantity += quantity
			if f.Dimensions[idx].Unit == "" {
				f.Dimensions[idx].Unit = unit
			}
			return
		}
	}
	f.Dimensions = append(f.Dimensions, DimensionUsage{Name: name, Quantity: quantity, Unit: unit})
}

func (f *Fact) ensureDimensionAtLeast(name string, quantity int64, unit string) {
	if f == nil || name == "" || quantity <= 0 {
		return
	}
	for idx := range f.Dimensions {
		if f.Dimensions[idx].Name == name {
			if f.Dimensions[idx].Quantity < quantity {
				f.Dimensions[idx].Quantity = quantity
			}
			if f.Dimensions[idx].Unit == "" {
				f.Dimensions[idx].Unit = unit
			}
			return
		}
	}
	f.Dimensions = append(f.Dimensions, DimensionUsage{Name: name, Quantity: quantity, Unit: unit})
}

func LegacyFieldsUseSeparatedCacheInput(fields map[string]interface{}) bool {
	if boolFromFields(fields, "claude") {
		return true
	}
	return providerFamilyFromFields(fields) == ProviderFamilyAnthropic
}

func UsesProviderSeparatedCacheInput(modelName string, fields map[string]interface{}) bool {
	return LegacyFieldsUseSeparatedCacheInput(fields)
}

func ProviderFamilyFromHint(hint ProviderHint) ProviderFamily {
	switch hint.APIType {
	case constant.APITypeAnthropic:
		return ProviderFamilyAnthropic
	case constant.APITypeGemini:
		return ProviderFamilyGemini
	case constant.APITypeXai:
		return ProviderFamilyXAI
	}

	raw := strings.ToLower(strings.Join([]string{
		hint.ProviderName,
		hint.BaseURL,
		hint.UpstreamModelName,
		hint.ModelName,
	}, " "))
	switch {
	case strings.Contains(raw, "deepseek"):
		return ProviderFamilyDeepSeek
	case strings.Contains(raw, "qwen") || strings.Contains(raw, "dashscope") || strings.Contains(raw, "aliyun") || strings.Contains(raw, "alibaba"):
		return ProviderFamilyQwen
	case strings.Contains(raw, "openrouter"):
		return ProviderFamilyOpenRouter
	case hint.APIType == constant.APITypeOpenAI || hint.ChannelType == constant.ChannelTypeOpenAI:
		return ProviderFamilyOpenAI
	default:
		return ProviderFamilyOpenAICompatible
	}
}

func ProviderFamilyFromFields(fields map[string]interface{}) ProviderFamily {
	return providerFamilyFromFields(fields)
}

func RawUsageFields(fields map[string]interface{}) map[string]any {
	if len(fields) == 0 {
		return nil
	}
	keys := []string{
		"cache_tokens",
		"cached_tokens",
		"cache_write_tokens",
		"cache_write_5m_tokens",
		"cache_write_1h_tokens",
		"cache_creation_input_tokens",
		"cache_creation_tokens",
		"cache_creation_5m_tokens",
		"cache_creation_1h_tokens",
		"cache_miss_tokens",
		"prompt_cache_hit_tokens",
		"prompt_cache_miss_tokens",
		"audio_input_tokens",
		"audio_output_tokens",
		"image_input_tokens",
		"audio_input",
		"audio_output",
		"image_input",
		"image_output",
		"text_input",
		"text_output",
		"web_search_call_count",
		"file_search_call_count",
		"image_generation_call",
	}
	result := make(map[string]any)
	for _, key := range keys {
		if value, ok := fields[key]; ok {
			result[key] = value
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func providerFamilyFromFields(fields map[string]interface{}) ProviderFamily {
	if fields == nil {
		return ProviderFamilyUnknown
	}
	for _, key := range []string{"provider_family", "usage_provider_family"} {
		if value, ok := fields[key]; ok {
			if family := normalizeProviderFamily(ProviderFamily(strings.TrimSpace(commonString(value)))); family != ProviderFamilyUnknown {
				return family
			}
		}
	}
	if boolFromFields(fields, "claude") {
		return ProviderFamilyAnthropic
	}
	return ProviderFamilyUnknown
}

func normalizeProviderFamily(providerFamily ProviderFamily) ProviderFamily {
	switch strings.ToLower(strings.TrimSpace(string(providerFamily))) {
	case "openai":
		return ProviderFamilyOpenAI
	case "anthropic", "claude":
		return ProviderFamilyAnthropic
	case "gemini", "google":
		return ProviderFamilyGemini
	case "qwen", "dashscope", "aliyun", "alibaba":
		return ProviderFamilyQwen
	case "deepseek":
		return ProviderFamilyDeepSeek
	case "openrouter":
		return ProviderFamilyOpenRouter
	case "typesafe":
		return ProviderFamilyTypeSafe
	case "cloudflare":
		return ProviderFamilyCloudflare
	case "xai", "x.ai", "grok":
		return ProviderFamilyXAI
	case "openai_compatible", "openai-compatible", "compatible":
		return ProviderFamilyOpenAICompatible
	default:
		return ProviderFamilyUnknown
	}
}

func commonString(value interface{}) string {
	switch typed := value.(type) {
	case string:
		return typed
	case []byte:
		return string(typed)
	default:
		return ""
	}
}

func firstPositiveIntFromFields(fields map[string]interface{}, keys ...string) int {
	for _, key := range keys {
		if value := intFromFields(fields, key); value > 0 {
			return value
		}
	}
	return 0
}

func intFromFields(fields map[string]interface{}, key string) int {
	if fields == nil {
		return 0
	}
	switch value := fields[key].(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	case json.Number:
		parsed, _ := value.Int64()
		return int(parsed)
	default:
		return 0
	}
}

func boolFromFields(fields map[string]interface{}, key string) bool {
	if fields == nil {
		return false
	}
	switch value := fields[key].(type) {
	case bool:
		return value
	case string:
		return strings.EqualFold(value, "true")
	default:
		return false
	}
}

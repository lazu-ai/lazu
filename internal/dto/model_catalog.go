package dto

import (
	"github.com/lazu-ai/lazu/pkg/billing"
	"time"

	"github.com/lazu-ai/lazu/internal/constant"
)

type ModelCatalogResponse struct {
	Object             string                      `json:"object"`
	Data               []ModelCatalogItem          `json:"data"`
	YourPricingContext *ModelCatalogPricingContext `json:"your_pricing_context,omitempty"`
}

type ModelCatalogItem struct {
	RequestModel           string                         `json:"request_model,omitempty"`
	DecisionCapabilities   *DecisionCapabilities          `json:"decision_capabilities,omitempty"`
	ID                     string                         `json:"id"`
	Object                 string                         `json:"object,omitempty"`
	Created                int64                          `json:"created,omitempty"`
	Name                   string                         `json:"name,omitempty"`
	Provider               string                         `json:"provider,omitempty"`
	Family                 string                         `json:"family,omitempty"`
	OwnedBy                string                         `json:"owned_by"`
	Description            string                         `json:"description,omitempty"`
	DescriptionEn          string                         `json:"description_en,omitempty"`
	DescriptionZh          string                         `json:"description_zh,omitempty"`
	Modality               ModelCatalogModality           `json:"modality"`
	InputModalities        []string                       `json:"input_modalities,omitempty"`
	OutputModalities       []string                       `json:"output_modalities,omitempty"`
	Capabilities           ModelCatalogCapabilities       `json:"capabilities"`
	SupportedEndpointTypes []constant.EndpointType        `json:"supported_endpoint_types"`
	SupportedEndpoints     []ModelCatalogEndpoint         `json:"supported_endpoints,omitempty"`
	DefaultEndpointType    constant.EndpointType          `json:"default_endpoint_type,omitempty"`
	ContextLength          int64                          `json:"context_length"`
	MaxOutputTokens        int64                          `json:"max_output_tokens"`
	Pricing                ModelCatalogPricing            `json:"pricing"`
	Lanes                  []ModelCatalogLane             `json:"lanes,omitempty"`
	YourPrice              *ModelCatalogPricing           `json:"your_price,omitempty"`
	YourPricesByGroup      map[string]ModelCatalogPricing `json:"your_prices_by_group,omitempty"`
	UsageCapabilities      ModelCatalogUsageCapabilities  `json:"usage_capabilities"`
	Accessible             *bool                          `json:"accessible,omitempty"`
	SupportedParameters    []string                       `json:"supported_parameters,omitempty"`
	ReleasedAt             *int64                         `json:"released_at,omitempty"`
	DeprecationDate        string                         `json:"deprecation_date,omitempty"`
	IsNew                  bool                           `json:"is_new,omitempty"`
	IsFeatured             bool                           `json:"is_featured,omitempty"`
	Popularity             float64                        `json:"popularity,omitempty"`
	Health                 *ModelCatalogHealth            `json:"health,omitempty"`
	Parameters             map[string]CatalogParam        `json:"parameters,omitempty"`
	Example                ModelCatalogExample            `json:"example"`
	Tags                   []string                       `json:"tags,omitempty"`
	OpenSource             bool                           `json:"open_source"`

	// Maker is the lab behind the model; items arrive sorted by maker order,
	// then newest release first.
	Maker             string                 `json:"maker,omitempty"`
	MakerRank         int                    `json:"maker_rank"`
	ReleaseDate       string                 `json:"release_date,omitempty"`
	IsNewRelease      bool                   `json:"is_new_release,omitempty"`
	KnowledgeCutoff   string                 `json:"knowledge_cutoff,omitempty"`
	InputLimit        int64                  `json:"input_limit,omitempty"`
	ReasoningOptions  *ModelCatalogReasoning `json:"reasoning_options,omitempty"`
	UnsupportedParams []string               `json:"unsupported_params,omitempty"`
}

// ModelCatalogReasoning is how a model's thinking can be controlled.
type ModelCatalogReasoning struct {
	Toggle  bool     `json:"toggle,omitempty"`
	Efforts []string `json:"efforts,omitempty"`
}

// ModelCatalogContextTier is a lane's long-context pricing: once a request's
// input (cached included) passes Threshold tokens, the whole request is
// billed at these prices per million tokens.
type ModelCatalogContextTier struct {
	Threshold    int64    `json:"threshold"`
	Input        *float64 `json:"input,omitempty"`
	Output       *float64 `json:"output,omitempty"`
	CacheRead    *float64 `json:"cache_read,omitempty"`
	CacheWrite5m *float64 `json:"cache_write_5m,omitempty"`
	CacheWrite1h *float64 `json:"cache_write_1h,omitempty"`
}

type ModelCatalogModality struct {
	Input  []string `json:"input"`
	Output []string `json:"output"`
}

type ModelCatalogEndpoint struct {
	Type   constant.EndpointType `json:"type"`
	Path   string                `json:"path"`
	Method string                `json:"method"`
	Mode   string                `json:"mode,omitempty"`
}

type ModelCatalogPricing struct {
	ImagePricing *billing.ImagePrices `json:"image_pricing,omitempty"`
	// VideoPricing is the public copy: per-second tiers, the audio
	// surcharge, and whether the charge follows the provider's report.
	VideoPricing           *billing.VideoPrices      `json:"video_pricing,omitempty"`
	ContextTiers           []ModelCatalogContextTier `json:"context_tiers,omitempty"`
	PeakPricing            *ModelCatalogPricing      `json:"peak_pricing,omitempty"`
	Currency               string                    `json:"currency"`
	BillingType            string                    `json:"billing_type,omitempty"`
	Input                  *float64                  `json:"input,omitempty"`
	Output                 *float64                  `json:"output,omitempty"`
	CacheRead              *float64                  `json:"cache_read,omitempty"`
	CacheWrite5m           *float64                  `json:"cache_write_5m,omitempty"`
	CacheWrite1h           *float64                  `json:"cache_write_1h,omitempty"`
	AudioInput             *float64                  `json:"audio_input,omitempty"`
	AudioOutput            *float64                  `json:"audio_output,omitempty"`
	ImageInput             *float64                  `json:"image_input,omitempty"`
	ImageOutput            *float64                  `json:"image_output,omitempty"`
	Request                *float64                  `json:"request,omitempty"`
	Second                 *float64                  `json:"second,omitempty"`
	InputPerMillion        *float64                  `json:"input_per_million,omitempty"`
	OutputPerMillion       *float64                  `json:"output_per_million,omitempty"`
	CacheReadPerMillion    *float64                  `json:"cache_read_per_million,omitempty"`
	CacheWrite5mPerMillion *float64                  `json:"cache_write_5m_per_million,omitempty"`
	CacheWrite1hPerMillion *float64                  `json:"cache_write_1h_per_million,omitempty"`
	RequestPrice           *float64                  `json:"request_price,omitempty"`
	SecondPrice            *float64                  `json:"second_price,omitempty"`
	PeakHoursUTC           string                    `json:"peak_hours_utc,omitempty"`
	PeakMultiplier         *float64                  `json:"peak_multiplier,omitempty"`
	PricingVersion         int64                     `json:"pricing_version,omitempty"`
	UpdatedAt              string                    `json:"updated_at,omitempty"`
	ContextTier            *ModelCatalogContextTier  `json:"context_tier,omitempty"`
}

// ModelCatalogLane is a published routing-price lane. Its standard price is
// safe to return publicly; YourPrice is populated only for authenticated,
// account-scoped catalog views.
type ModelCatalogLane struct {
	Key              string               `json:"key"`
	Available        bool                 `json:"available"`
	Pricing          ModelCatalogPricing  `json:"pricing"`
	InputPerMillion  *float64             `json:"input_per_million,omitempty"`
	OutputPerMillion *float64             `json:"output_per_million,omitempty"`
	PerCall          *float64             `json:"per_call,omitempty"`
	PerSecond        *float64             `json:"per_second,omitempty"`
	PeakHoursUTC     string               `json:"peak_hours_utc,omitempty"`
	PeakMultiplier   *float64             `json:"peak_multiplier,omitempty"`
	YourPrice        *ModelCatalogPricing `json:"your_price,omitempty"`
	// PlanDiscountPct is the viewer's 专属折扣 on this lane, percent off the
	// official price (40 = 6折), when their plan prices it.
	PlanDiscountPct float64 `json:"plan_discount_pct,omitempty"`
	// Health is measured per lane, not per model: the whole point of splitting
	// stable from eco is that the two can behave differently at the same
	// moment, so a single model-level number would hide the comparison the
	// lanes exist to enable.
	Health *ModelCatalogHealth `json:"health,omitempty"`
}

type ModelCatalogUsageCapabilities struct {
	ReportedDimensions []string            `json:"reported_dimensions"`
	BillableDimensions []string            `json:"billable_dimensions"`
	ResponseFields     map[string][]string `json:"response_fields,omitempty"`
}

type ModelCatalogCapabilities struct {
	SupportsFunctionCalling         bool `json:"supports_function_calling"`
	SupportsParallelFunctionCalling bool `json:"supports_parallel_function_calling"`
	SupportsVision                  bool `json:"supports_vision"`
	SupportsReasoning               bool `json:"supports_reasoning"`
	SupportsResponseSchema          bool `json:"supports_response_schema"`
	SupportsPromptCaching           bool `json:"supports_prompt_caching"`
	SupportsAudioInput              bool `json:"supports_audio_input"`
	SupportsAudioOutput             bool `json:"supports_audio_output"`
	SupportsWebSearch               bool `json:"supports_web_search"`
	SupportsPDFInput                bool `json:"supports_pdf_input"`
	SupportsComputerUse             bool `json:"supports_computer_use"`
}

type ModelCatalogPricingContext struct {
	Group string `json:"group,omitempty"`
	// PricePlan names the viewer's 专属折扣 plan, when they have one.
	PricePlan      string  `json:"price_plan,omitempty"`
	DiscountFactor float64 `json:"discount_factor"`
}

type ModelCatalogHealth struct {
	Status       string   `json:"status"`
	LatencyP50Ms *int     `json:"latency_p50_ms,omitempty"`
	LatencyP95Ms *int     `json:"latency_p95_ms,omitempty"`
	Availability *float64 `json:"availability,omitempty"`
	// OutputTPS is decoded output tokens per second over the window's samples.
	OutputTPS *float64 `json:"output_tps,omitempty"`
	// ObservedWindow says whether the figures rest on anything: "sample" when
	// the window holds measured requests — customer traffic and the gateway's
	// own streamed probes alike, which are not told apart outside the operator
	// console — "none" when it holds nothing, or "runtime" for figures taken
	// from live routing state.
	ObservedWindow string `json:"observed_window,omitempty"`
	// BucketSeconds describes the lane history published to the model plaza:
	// the health page's week, 84 two-hour buckets.
	BucketSeconds int64                      `json:"bucket_seconds,omitempty"`
	Buckets       []ModelCatalogHealthBucket `json:"buckets,omitempty"`
}

// ModelCatalogHealthBucket is the public, lane-level history used by the
// model detail table. It deliberately contains no upstream channel identity.
type ModelCatalogHealthBucket struct {
	Index       int       `json:"index"`
	StartAt     time.Time `json:"start_at"`
	Source      string    `json:"source"`
	SuccessRate *float64  `json:"success_rate,omitempty"`
	Tone        string    `json:"tone"`
}

type CatalogParam struct {
	Type        string    `json:"type"`
	Description string    `json:"description,omitempty"`
	Default     any       `json:"default,omitempty"`
	Required    bool      `json:"required,omitempty"`
	Range       []float64 `json:"range,omitempty"`
	Enum        []string  `json:"enum,omitempty"`
}

type ModelCatalogExample struct {
	Curl string `json:"curl,omitempty"`
}

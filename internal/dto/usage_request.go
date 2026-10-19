package dto

import "github.com/lazu-ai/lazu/pkg/billing"

type UsageRequestDetailResponse struct {
	Object    string               `json:"object"`
	ID        string               `json:"id"`
	CreatedAt int64                `json:"created_at"`
	Model     string               `json:"model"`
	Endpoint  string               `json:"endpoint,omitempty"`
	Usage     UsageRequestUsage    `json:"usage"`
	Billing   UsageRequestBilling  `json:"billing"`
	Routing   *UsageRequestRouting `json:"routing,omitempty"`
	LogID     string               `json:"log_id,omitempty"`
}

type UsageRequestUsage struct {
	Images           *billing.ImageUsage `json:"images,omitempty"`
	Measurement      *UsageMeasurement   `json:"measurement,omitempty"`
	PromptTokens     int                 `json:"prompt_tokens"`
	CompletionTokens int                 `json:"completion_tokens"`
	TotalTokens      int                 `json:"total_tokens"`
	Dimensions       []UsageDimension    `json:"dimensions"`
}

type UsageDimension struct {
	Name     string `json:"name"`
	Quantity int64  `json:"quantity"`
	Unit     string `json:"unit"`
}

type UsageRequestBilling struct {
	Currency               string             `json:"currency"`
	AmountMicrousd         int64              `json:"amount_microusd"`
	PricingVersion         int64              `json:"pricing_version,omitempty"`
	LineItems              []billing.LineItem `json:"line_items,omitempty"`
	MissingPriceDimensions []string           `json:"missing_price_dimensions,omitempty"`
	// ContextTier is set when the request's input passed the model's
	// long-context threshold and every line was billed at the long prices.
	ContextTier *billing.ContextTierApplied `json:"context_tier,omitempty"`
	// Peak is set when the request was billed inside the model's peak hours.
	Peak *billing.PeakApplied `json:"peak,omitempty"`
	// Discount is the whole-order discount; the internal rule that granted
	// it stays private.
	Discount *UsageRequestDiscount `json:"discount,omitempty"`
}

type UsageRequestDiscount struct {
	Pct            float64 `json:"pct"`
	AmountMicrousd int64   `json:"amount_microusd"`
}

type UsageRequestRouting struct {
	StatusCode      *int  `json:"status_code,omitempty"`
	TotalDurationMs *int  `json:"total_duration_ms,omitempty"`
	TTFTMs          *int  `json:"ttft_ms,omitempty"`
	WasStreaming    *bool `json:"was_streaming,omitempty"`
}

type UsageRequestProviderUsage struct {
	Family    string         `json:"family,omitempty"`
	RawFields map[string]any `json:"raw_fields,omitempty"`
}

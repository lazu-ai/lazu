package dto

import "encoding/json"

const (
	SearchBackendAuto = "auto"

	SearchCapabilityWebSearch = "web_search"
	SearchCapabilityWebFetch  = "web_fetch"
	SearchCapabilityAnswer    = "answer"
)

type SearchBackendRecord struct {
	ID              string   `json:"id,omitempty"`
	Name            string   `json:"name,omitempty"`
	Provider        string   `json:"provider,omitempty"`
	Type            int      `json:"type,omitempty"`
	MaskedKey       string   `json:"masked_key,omitempty"`
	LastUsedAt      int64    `json:"last_used_at,omitempty"`
	APIKey          string   `json:"api_key,omitempty"`
	BaseURL         string   `json:"base_url,omitempty"`
	Capabilities    []string `json:"capabilities,omitempty"`
	MaxResults      int      `json:"max_results,omitempty"`
	TimeoutMs       int      `json:"timeout_ms,omitempty"`
	Priority        int64    `json:"priority,omitempty"`
	Status          int      `json:"status,omitempty"`
	SearchPrice     float64  `json:"search_price,omitempty"`
	FetchPrice      float64  `json:"fetch_price,omitempty"`
	UseProviderCost bool     `json:"use_provider_cost,omitempty"`
}

type SearchRequest struct {
	Query                  string                     `json:"query"`
	Type                   string                     `json:"type,omitempty"`
	Region                 string                     `json:"region,omitempty"`
	Country                string                     `json:"country,omitempty"`
	Language               string                     `json:"language,omitempty"`
	TimeRange              string                     `json:"time_range,omitempty"`
	MaxResults             int                        `json:"max_results,omitempty"`
	IncludeAnswer          bool                       `json:"include_answer,omitempty"`
	IncludeRawContent      bool                       `json:"include_raw_content,omitempty"`
	IncludeProviderPayload bool                       `json:"include_provider_payload,omitempty"`
	Backend                string                     `json:"backend,omitempty"`
	IncludeDomains         []string                   `json:"include_domains,omitempty"`
	ExcludeDomains         []string                   `json:"exclude_domains,omitempty"`
	SearchDepth            string                     `json:"search_depth,omitempty"`
	ProviderOptions        map[string]json.RawMessage `json:"provider_options,omitempty"`
}

type SearchResult struct {
	Title       string   `json:"title"`
	URL         string   `json:"url"`
	Snippet     string   `json:"snippet,omitempty"`
	Content     *string  `json:"content,omitempty"`
	PublishedAt *string  `json:"published_at,omitempty"`
	Score       *float64 `json:"score,omitempty"`
	Source      string   `json:"source,omitempty"`
}

type SearchUsage struct {
	WebSearchRequests      int `json:"web_search_requests"`
	WebSearchBillableUnits int `json:"web_search_billable_units,omitempty"`
}

type SearchProviderTrace struct {
	Backend string `json:"backend"`
}

type SearchRoutingRejected struct {
	Backend string `json:"backend"`
	Reason  string `json:"reason"`
}

type SearchRouteReceipt struct {
	Selection       string                  `json:"selection"`
	SelectedBackend string                  `json:"selected_backend,omitempty"`
	ChoseProvider   string                  `json:"chose_provider,omitempty"`
	Candidates      []string                `json:"candidates,omitempty"`
	Rejected        []SearchRoutingRejected `json:"rejected,omitempty"`
	Downgraded      bool                    `json:"downgraded,omitempty"`
	FallbackCount   int                     `json:"fallback_count,omitempty"`
	DecisionReason  string                  `json:"decision_reason,omitempty"`
}

type SearchResponse struct {
	Query           string              `json:"query"`
	Results         []SearchResult      `json:"results"`
	Answer          *string             `json:"answer,omitempty"`
	Usage           SearchUsage         `json:"usage"`
	ProviderTrace   SearchProviderTrace `json:"provider_trace"`
	RouteReceipt    *SearchRouteReceipt `json:"route_receipt,omitempty"`
	ProviderPayload json.RawMessage     `json:"provider_payload,omitempty"`
}

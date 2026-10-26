package dto

import (
	"fmt"
	"regexp"
)

type ChannelSettings struct {
	DecisionDialect        string `json:"decision_dialect,omitempty"`
	CFAccountID            string `json:"cf_account_id,omitempty"`
	DecisionTimeoutMS      int    `json:"decision_timeout_ms,omitempty"`
	MaxRPS                 int    `json:"max_rps,omitempty"`
	MaxInFlight            int    `json:"max_in_flight,omitempty"`
	SharedLimitGroup       string `json:"shared_limit_group,omitempty"`
	SharedMaxRPS           int    `json:"shared_max_rps,omitempty"`
	SharedMaxInFlight      int    `json:"shared_max_in_flight,omitempty"`
	ForceFormat            bool   `json:"force_format,omitempty"`
	ThinkingToContent      bool   `json:"thinking_to_content,omitempty"`
	Proxy                  string `json:"proxy"`
	PassThroughBodyEnabled bool   `json:"pass_through_body_enabled,omitempty"`
}

func (s ChannelSettings) ValidateDecisionSettings() error {
	if s.MaxRPS < 0 || s.MaxInFlight < 0 || s.SharedMaxRPS < 0 || s.SharedMaxInFlight < 0 {
		return fmt.Errorf("channel capacity limits must be nonnegative")
	}
	if len(s.SharedLimitGroup) > 100 {
		return fmt.Errorf("shared limit group is too long")
	}
	if s.SharedLimitGroup == "" && (s.SharedMaxRPS > 0 || s.SharedMaxInFlight > 0) {
		return fmt.Errorf("shared limits require a group")
	}
	if s.DecisionTimeoutMS < 0 || s.DecisionTimeoutMS > 10000 {
		return fmt.Errorf("decision timeout must be between 0 and 10000ms")
	}
	switch s.DecisionDialect {
	case "", "typesafe", "openrouter", "openai":
	case "cloudflare_gateway", "cloudflare_workers_ai":
		if !regexp.MustCompile(`^[a-zA-Z0-9_-]{1,100}$`).MatchString(s.CFAccountID) {
			return fmt.Errorf("Cloudflare account ID is required")
		}
	default:
		return fmt.Errorf("unsupported decision dialect")
	}
	return nil
}

type ChannelOtherSettings struct {
	// CostMultiplier prices this source's upstream cost as the official list
	// price times the multiplier (0.3 = bought at 30% of list) whenever the
	// source has no cost row of its own for a model. 0 means unset (1.0).
	CostMultiplier float64 `json:"cost_multiplier,omitempty"`

	// Restrict this source to Codex Responses requests and forward their original session headers.
	CodexClientOnly bool `json:"codex_client_only,omitempty"`

	// Restrict this source to Claude Code requests and forward their original
	// client headers. Some Claude Code subscription pools admit nothing else.
	ClaudeCodeClientOnly bool `json:"claude_code_client_only,omitempty"`
	// ClaudeCodeUserAgent overrides, for this source only, the User-Agent of
	// the configured Claude Code probe identity (probe_identities.claude_code
	// in config.yaml). Customer traffic always carries the caller's own
	// User-Agent instead.
	ClaudeCodeUserAgent string `json:"claude_code_user_agent,omitempty"`

	// ObserveNewRoutes parks every route this source newly serves in inventory
	// and watches it for model.RouteObservationDays() (source_checks in
	// config.yaml) before it may carry traffic. It is on for sources created from the console.
	ObserveNewRoutes bool `json:"observe_new_routes,omitempty"`

	// Opt-in, exact upstream model IDs only. The relay removes the legacy beta
	// only when neither the original nor outbound request uses system turns.
	ClaudeOmitUnusedSystemBetaModels []string `json:"claude_omit_unused_system_beta_models,omitempty"`
	// Opt-in, exact upstream model IDs only. The relay removes the
	// prompt-caching-scope beta only when no cache_control asks for more than
	// a default ephemeral breakpoint.
	ClaudeOmitUnusedCacheScopeBetaModels []string `json:"claude_omit_unused_cache_scope_beta_models,omitempty"`
	// Opt-in, exact upstream model IDs only. The relay removes context_management
	// and its beta only when every edit keeps all content (clear_thinking, keep "all").
	ClaudeOmitKeepAllContextManagementModels []string `json:"claude_omit_keep_all_context_management_models,omitempty"`
	// Opt-in, exact upstream model IDs only. The relay folds mid-conversation
	// system turns into the user turn they follow (text only) and drops the
	// mid-conversation betas. Conflicts with ClaudeOmitUnusedSystemBetaModels.
	ClaudeFoldSystemTurnsModels []string `json:"claude_fold_system_turns_models,omitempty"`
	// Opt-in, exact upstream model IDs only. The relay drops the
	// thinking-token-count beta, which only changes the response usage breakdown.
	ClaudeOmitThinkingTokenCountBetaModels []string `json:"claude_omit_thinking_token_count_beta_models,omitempty"`
	// Opt-in restriction for Responses-only upstreams that accept one user text string.
	ResponsesSingleUserText bool   `json:"responses_single_user_text,omitempty"`
	ProtocolDialect         string `json:"protocol_dialect,omitempty"`
	AzureResponsesVersion   string `json:"azure_responses_version,omitempty"`
	OpenRouterEnterprise    *bool  `json:"openrouter_enterprise,omitempty"`
	AllowServiceTier        bool   `json:"allow_service_tier,omitempty"`        // 是否允许 service_tier 透传（默认过滤以避免额外计费）
	AllowSpeed              bool   `json:"allow_speed,omitempty"`               // 是否允许 speed 透传（仅 Claude，默认过滤以避免意外切换模式）
	AllowInferenceGeo       bool   `json:"allow_inference_geo,omitempty"`       // 是否允许 inference_geo 透传（仅 Claude，默认过滤以避免地域驻留配置意外透传）
	DisableStore            bool   `json:"disable_store,omitempty"`             // 是否禁用 store 透传（默认允许透传，禁用后可能导致 Codex 无法使用）
	ExplicitStreamRequired  bool   `json:"explicit_stream_required,omitempty"`  // 上游要求 stream 即使为 false 也必须显式发送
	AllowSafetyIdentifier   bool   `json:"allow_safety_identifier,omitempty"`   // 是否允许 safety_identifier 透传（默认过滤以保护用户隐私）
	AllowIncludeObfuscation bool   `json:"allow_include_obfuscation,omitempty"` // 是否允许 stream_options.include_obfuscation 透传（默认过滤以保留混淆保护）
	// Video upstream shape. VideoFamily picks the driver ("" = decide from
	// the channel type and model name; see internal/relay/channel/video).
	// VideoEnvelope configures the unified-envelope family (aggregators that
	// serve every video model through one task API). The two URL templates are the pre-family Custom-channel
	// form and still work: they map onto an envelope with OpenAI-shaped bodies.
	VideoFamily             string               `json:"video_family,omitempty"`
	VideoEnvelope           *VideoEnvelopeConfig `json:"video_envelope,omitempty"`
	VideoStatusURLTemplate  string               `json:"video_status_url_template,omitempty"`
	VideoContentURLTemplate string               `json:"video_content_url_template,omitempty"`
	Capabilities            *ChannelCapabilities `json:"capabilities,omitempty"` // 路由能力声明，nil = 视为全能；详见 ChannelCapabilities
	SearchBackend           *SearchBackendConfig `json:"search_backend,omitempty"`
}

var claudeCompatModelID = regexp.MustCompile(`^claude-[a-zA-Z0-9][a-zA-Z0-9._:-]*$`)

// ClaudeCompatibilityConfigError names the model list that failed validation.
type ClaudeCompatibilityConfigError struct {
	Field  string
	Reason string
}

func (e *ClaudeCompatibilityConfigError) Error() string {
	if e.Reason != "" {
		return fmt.Sprintf("%s %s", e.Field, e.Reason)
	}
	return fmt.Sprintf("%s requires unique, exact Claude model IDs (no wildcards)", e.Field)
}

func (s ChannelOtherSettings) ValidateClaudeCompatibility() error {
	for _, list := range []struct {
		field  string
		models []string
	}{
		{"claude_omit_unused_system_beta_models", s.ClaudeOmitUnusedSystemBetaModels},
		{"claude_omit_unused_cache_scope_beta_models", s.ClaudeOmitUnusedCacheScopeBetaModels},
		{"claude_omit_keep_all_context_management_models", s.ClaudeOmitKeepAllContextManagementModels},
		{"claude_fold_system_turns_models", s.ClaudeFoldSystemTurnsModels},
		{"claude_omit_thinking_token_count_beta_models", s.ClaudeOmitThinkingTokenCountBetaModels},
	} {
		seen := make(map[string]bool)
		for _, model := range list.models {
			if len(model) > 200 || !claudeCompatModelID.MatchString(model) || seen[model] {
				return &ClaudeCompatibilityConfigError{Field: list.field}
			}
			seen[model] = true
		}
	}
	// The unused-system action refuses a body with system turns before fold
	// could rewrite it, so one model cannot carry both.
	for _, model := range s.ClaudeFoldSystemTurnsModels {
		for _, other := range s.ClaudeOmitUnusedSystemBetaModels {
			if model == other {
				return &ClaudeCompatibilityConfigError{Field: "claude_fold_system_turns_models",
					Reason: "cannot share a model with claude_omit_unused_system_beta_models"}
			}
		}
	}
	return nil
}

type SearchBackendConfig struct {
	Provider        string   `json:"provider,omitempty"`
	Capabilities    []string `json:"capabilities,omitempty"`
	MaxResults      int      `json:"max_results,omitempty"`
	TimeoutMs       int      `json:"timeout_ms,omitempty"`
	SearchPrice     float64  `json:"search_price,omitempty"`
	FetchPrice      float64  `json:"fetch_price,omitempty"`
	UseProviderCost bool     `json:"use_provider_cost,omitempty"`
}

// ChannelCapabilities is the structured declaration of what a channel can
// actually serve, consumed by the router to filter and prefer candidates
// before scoring. All fields are optional — a Channel with no Capabilities
// set (the default for every channel that existed before this field was
// introduced) is treated as "no declared restrictions / fully capable",
// preserving baseline behaviour. Operators progressively fill these in
// per channel via the admin console or SQL.
//
// Field semantics:
//
//   - Numeric limits (ContextWindow, MaxOutputTokens, RPMLimit, TPMLimit,
//     MaxParallel) use 0 = "unknown / no limit". Routing filters interpret
//     0 as "do not filter on this dimension".
//   - SupportsX flags use *bool because the difference between "nobody
//     told us" (nil → assume yes, retry-safe) and "explicitly false"
//     (skip this channel) matters for routing decisions. We never
//     compare against bare false.
//   - PreferredFor is a free-form list of capability tags the operator
//     wants this channel to be the preferred destination for. The
//     router chooses a matching compatible channel before applying the
//     normal priority/weight policy. Suggested vocabulary: "code", "math",
//     "long_context", "vision", "multi_image", "tools", "cheap_short",
//     "audio", "creative". Unknown tags are silently ignored.
type ChannelCapabilities struct {
	ContextWindow           int      `json:"context_window,omitempty"`            // 0 = unknown / unlimited; e.g. 8192, 32768, 128000, 1000000
	MaxOutputTokens         int      `json:"max_output_tokens,omitempty"`         // 0 = unknown
	SupportsChatCompletions *bool    `json:"supports_chat_completions,omitempty"` // nil → use dialect baseline; *false → endpoint unavailable
	SupportsResponses       *bool    `json:"supports_responses,omitempty"`        // nil → use dialect baseline; *false → endpoint unavailable
	SupportsTools           *bool    `json:"supports_tools,omitempty"`            // nil → assume yes (compat); *false → skip for tool-using requests
	SupportsVision          *bool    `json:"supports_vision,omitempty"`           // nil → assume yes (compat)
	SupportsAudio           *bool    `json:"supports_audio,omitempty"`            // nil → assume yes (compat)
	SupportsJSONSchema      *bool    `json:"supports_json_schema,omitempty"`      // nil → use the model catalog; *false → the channel takes no response_format json_schema (a degradation rule may stand in; textgen/degrade.go)
	RPMLimit                int      `json:"rpm_limit,omitempty"`                 // 0 = unlimited
	TPMLimit                int      `json:"tpm_limit,omitempty"`                 // 0 = unlimited
	MaxParallel             int      `json:"max_parallel,omitempty"`              // 0 = unlimited in-flight
	PreferredFor            []string `json:"preferred_for,omitempty"`             // capability tags this channel is the preferred destination for
}

// HasJSONSchema reports whether the channel may be sent a JSON-schema
// response format. nil means "the catalog decides"; only an explicit false
// denies it.
func (c *ChannelCapabilities) HasJSONSchema() bool {
	return c == nil || c.SupportsJSONSchema == nil || *c.SupportsJSONSchema
}

func (c *ChannelCapabilities) HasChatCompletionsEndpoint() bool {
	return c == nil || c.SupportsChatCompletions == nil || *c.SupportsChatCompletions
}

func (c *ChannelCapabilities) HasResponsesEndpoint() bool {
	return c == nil || c.SupportsResponses == nil || *c.SupportsResponses
}

// HasTools reports whether the channel supports function/tool calling.
// nil receiver or nil flag both mean "yes, assume so" — the caller is
// asking "can I route a tool-using request here?" and the safe default
// (matches behaviour for every channel that existed before this struct
// was added) is to allow it.
func (c *ChannelCapabilities) HasTools() bool {
	if c == nil || c.SupportsTools == nil {
		return true
	}
	return *c.SupportsTools
}

// HasVision reports whether the channel can accept image inputs.
func (c *ChannelCapabilities) HasVision() bool {
	if c == nil || c.SupportsVision == nil {
		return true
	}
	return *c.SupportsVision
}

// HasAudio reports whether the channel can accept audio inputs.
func (c *ChannelCapabilities) HasAudio() bool {
	if c == nil || c.SupportsAudio == nil {
		return true
	}
	return *c.SupportsAudio
}

// FitsContext reports whether a request with the given prompt-token count
// is small enough for this channel's declared context window. Returns
// true when no window is declared (0) — preserves the legacy "no limits"
// behaviour for un-configured channels.
func (c *ChannelCapabilities) FitsContext(promptTokens int) bool {
	if c == nil || c.ContextWindow <= 0 {
		return true
	}
	return promptTokens <= c.ContextWindow
}

// PrefersIntent reports whether the channel was tagged as preferred for
// the given intent (typically produced by the router from request
// features). Returns false on nil / empty list — preferred_for is purely
// a positive boost, never a negative filter.
func (c *ChannelCapabilities) PrefersIntent(intent string) bool {
	if c == nil || len(c.PreferredFor) == 0 || intent == "" {
		return false
	}
	for _, tag := range c.PreferredFor {
		if tag == intent {
			return true
		}
	}
	return false
}

func (s *ChannelOtherSettings) IsOpenRouterEnterprise() bool {
	if s == nil || s.OpenRouterEnterprise == nil {
		return false
	}
	return *s.OpenRouterEnterprise
}

// GetCapabilities returns the embedded ChannelCapabilities, or nil if the
// channel has not declared any. Callers should always go through this
// rather than dereferencing the field directly so future changes (e.g.
// caching, default lookups) stay in one place.
func (s *ChannelOtherSettings) GetCapabilities() *ChannelCapabilities {
	if s == nil {
		return nil
	}
	return s.Capabilities
}

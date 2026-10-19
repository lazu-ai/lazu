package router

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"strconv"
	"strings"

	"github.com/lazu-ai/lazu/internal/protocol/textgen"
)

// Features is what routing reads from a request body: the caller's hints,
// provider preferences and fallback models, how many images it carries (the
// priority router prefers vision-capable sources past one image), and a
// prompt hash that the retry / model-switch backfill compares.
type Features struct {
	PromptHash       string
	HasVision        bool
	VisionImageCount int
	// Hints carries lazu-specific routing hints the caller passed in
	// the request body's metadata field. Optional: nil for callers using
	// the plain OpenAI protocol. Routing decisions
	// consume Hints as positive scoring boosts; absence never excludes
	// a channel — the legacy "no hints" code path stays identical.
	Hints *RoutingHints
	// Provider holds the structured provider-preference object the
	// caller passed at request body top level (OpenRouter-style
	// `provider: {...}`). Optional: nil for callers that don't opt in.
	// Unlike Hints, Provider fields can act as HARD filters: only /
	// ignore prune the candidate set, require_parameters drops
	// capability-incomplete channels. sort changes scoring weights.
	Provider *ProviderPrefs
	// ModelsFallback is the ordered list of model names the caller will
	// accept (OpenRouter-style `models: [...]` top-level field). The
	// router uses model[0] as primary and falls through the rest only
	// when the current model has no satisfiable channels. Empty for
	// callers using the plain `model` field.
	ModelsFallback []string
}

// RoutingHints is the lazu-specific hint vocabulary. All fields are
// optional. The fields live in `request.metadata.lazu` (preferred) or
// can be promoted from a few OpenAI-standard fields where the semantics
// overlap (e.g. `request.user` → SessionID, `request.metadata.user_id`).
//
// Compatibility rule: every hint must be **purely additive**. The router
// must produce a sensible decision when Hints is nil, and any hint a
// caller sends must only *promote* candidates, never exclude — clients
// that drop hints get the same baseline-good experience.
//
// Vocabulary:
//
//	Intent             "code" | "math" | "vision" | "long_context" |
//	                   "tools" | "cheap_short" | "creative" | "audio"
//	BudgetTier         "cheap" | "balanced" | "premium"
//	PreferredProvider  free-form lowercase substring matched against
//	                   channel.GetTag(): "openai" | "anthropic" |
//	                   "google" | "deepseek" | ...
//	FallbackModels     ordered list of model names to try if the primary
//	                   model is unavailable (OpenRouter-style).
//	SessionID          free-form string used by the quality-signal
//	                   backfill worker to detect retries and model
//	                   switches across requests from the same caller.
type RoutingHints struct {
	Intent            string   `json:"intent,omitempty"`
	BudgetTier        string   `json:"budget_tier,omitempty"`
	PreferredProvider string   `json:"preferred_provider,omitempty"`
	FallbackModels    []string `json:"fallback_models,omitempty"` // deprecated: use top-level `models` instead
	SessionID         string   `json:"session_id,omitempty"`
}

// ProviderPrefs is the OpenRouter-aligned provider-preference object.
// Lives at request body top level (not under metadata) so it matches
// the OpenRouter `provider: {...}` shape exactly — callers migrating
// from OpenRouter can paste the same payload.
//
// Field semantics:
//
//   - Sort           one of "price" / "throughput" / "latency". Reweights
//     the composite router's scoring components. Empty =
//     keep default weights.
//   - Only           allow-list of provider identifiers; if non-empty,
//     channels outside the list are HARD-filtered unless
//     AllowFallbacks is true AND the list yielded zero
//     candidates (then we treat it as a soft preference
//     and fall back to the full set, logged in Meta).
//   - Ignore         deny-list of provider identifiers; matching channels
//     are HARD-filtered, no fallback override.
//   - AllowFallbacks default true. When false, an empty Only-filtered set
//     fails the request rather than degrading.
//   - RequireParams  when true, the router additionally drops channels
//     whose declared Capabilities can't satisfy every
//     feature the request needs (tools / vision / audio /
//     context window). The same information is retained in decision facts.
//     applies for those features — RequireParams just
//     makes the gate explicit + non-silent.
//   - MaxPricePerMTok  USD/1M tokens upper bound. Reserved for v1.1
//     (needs pricing-catalog plumbing); accepted and
//     echoed back via Decision.Meta today, not scored.
//   - DataCollection "allow" (default) or "deny". Reserved for v1.1
//     (needs channel `data_retention` schema); accepted
//     and echoed back today, not scored.
//
// Provider identifier matching is case-insensitive substring against
// BOTH the channel-type-name (constant.GetChannelTypeName, e.g.
// "OpenAI" / "Anthropic" / "Gemini") and the channel's operator tag
// (channel.GetTag()). Either match counts. This mirrors OpenRouter's
// "we accept either the canonical name or operator-defined alias".
type ProviderPrefs struct {
	Sort            string   `json:"sort,omitempty"`
	Only            []string `json:"only,omitempty"`
	Ignore          []string `json:"ignore,omitempty"`
	AllowFallbacks  *bool    `json:"allow_fallbacks,omitempty"`
	RequireParams   bool     `json:"require_parameters,omitempty"`
	MaxPricePerMTok float64  `json:"max_price_per_mtok,omitempty"`
	DataCollection  string   `json:"data_collection,omitempty"`
}

// AllowsFallback reports whether AllowFallbacks is on; defaults to true
// (the conservative choice — degrade rather than fail) when the caller
// didn't explicitly set the field.
func (p *ProviderPrefs) AllowsFallback() bool {
	if p == nil || p.AllowFallbacks == nil {
		return true
	}
	return *p.AllowFallbacks
}

func ExtractFeatures(ctx context.Context, req *RequestContext, raw []byte) *Features {
	_ = ctx
	f := &Features{PromptHash: sha256Hex(canonicalize(raw))}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err == nil {
		if messages, ok := body["messages"].([]any); ok {
			countImages(messages, f)
		}
		f.Hints = extractHints(body)
		f.Provider = extractProviderPrefs(body)
		f.ModelsFallback = extractModelsFallback(body, f.Hints)
	}
	if req != nil {
		req.Features = f
	}
	return f
}

// ExtractSemanticFeatures projects an already decoded text-generation request
// into routing features. The request body has already been parsed by
// TextgenParse, so this path deliberately never scans or canonicalizes the
// raw body. In particular, inline image Base64 remains media metadata and is
// not treated as prompt text or token input.
func ExtractSemanticFeatures(ctx context.Context, req *RequestContext, request *textgen.Request) *Features {
	_ = ctx
	f := &Features{PromptHash: sha256Hex([]byte(semanticFeatureHash(request)))}
	if request != nil {
		for _, item := range request.Conversation {
			for _, part := range item.Content {
				if part.Kind == textgen.ContentImage {
					f.HasVision = true
					f.VisionImageCount++
				}
			}
		}
		body := decodeRoutingFields(request.Raw.TopLevel)
		f.Hints = extractHints(body)
		f.Provider = extractProviderPrefs(body)
		f.ModelsFallback = extractModelsFallback(body, f.Hints)
	}
	if req != nil {
		req.Features = f
	}
	return f
}

// semanticFeatureHash is the retry identity of a decoded request: its text,
// media fingerprints, output constraints and server-side state.
func semanticFeatureHash(request *textgen.Request) string {
	if request == nil {
		return ""
	}
	var hash strings.Builder
	appendText := func(value string) {
		if value == "" {
			return
		}
		hash.WriteString(value)
		hash.WriteByte('\n')
	}
	appendMediaMarker := func(part textgen.ContentPart) {
		hash.WriteString("[media:")
		hash.WriteString(string(part.Kind))
		hash.WriteByte(':')
		hash.WriteString(sha256Hex([]byte(mediaIdentity(part))))
		hash.WriteString("]\n")
	}
	for _, instruction := range request.Instructions {
		appendText(instruction.Role)
		appendText(instruction.Text)
	}
	for _, item := range request.Conversation {
		appendText(item.Role)
		appendText(item.Name)
		appendText(item.FunctionName)
		appendText(item.Arguments)
		appendText(item.Output)
		if item.PlainReasoning.Set {
			appendText(item.PlainReasoning.Value)
		}
		for _, part := range item.Content {
			switch part.Kind {
			case textgen.ContentText:
				appendText(part.Text)
			case textgen.ContentImage, textgen.ContentAudio, textgen.ContentFile:
				appendMediaMarker(part)
			}
		}
	}
	for _, tool := range request.Tools {
		appendText(tool.Type)
		appendText(tool.Name)
		appendText(tool.Description)
		appendText(string(tool.Parameters))
		appendText(string(tool.Hosted))
	}
	// Output constraints affect the semantic request and therefore retry
	// identity, but they are not input text and must not inflate admission
	// token estimates. Keep them in the hash-only projection.
	hash.WriteString("[output:")
	hash.WriteString(request.Output.Type)
	hash.WriteByte(':')
	hash.WriteString(request.Output.Name)
	hash.WriteByte(':')
	hash.WriteString(request.Output.Description)
	hash.WriteByte(':')
	hash.WriteString(string(request.Output.Schema))
	if request.Output.Strict.Set {
		hash.WriteString(":strict=")
		if request.Output.Strict.Value {
			hash.WriteString("true")
		} else {
			hash.WriteString("false")
		}
	}
	hash.WriteString("]\n")
	// Server-side state changes the prompt even when visible input is the
	// same. Keep identifiers/templates out of text/token classification.
	state, _ := json.Marshal(struct {
		PreviousResponseID string          `json:"previous_response_id,omitempty"`
		Conversation       json.RawMessage `json:"conversation,omitempty"`
		Prompt             json.RawMessage `json:"prompt,omitempty"`
	}{request.Lifecycle.PreviousResponseID, request.Lifecycle.Conversation, request.Lifecycle.PromptTemplate})
	hash.WriteString("[state:")
	hash.Write(canonicalize(state))
	hash.WriteString("]\n")
	return hash.String()
}

// mediaIdentity distinguishes media for routing-quality retry signals without
// putting the payload into the text projection or hashing the whole Base64
// string. URLs and file IDs are preferred; inline data uses a bounded
// length/prefix/suffix fingerprint, which is cheap and stable for retries.
func mediaIdentity(part textgen.ContentPart) string {
	const edge = 96
	value := part.URL
	source := "url"
	if value == "" {
		value = part.FileID
		source = "file"
	}
	if value == "" {
		value = part.Data
		source = "data"
	}
	if value == "" {
		return source + ":empty"
	}
	if len(value) <= edge*2 {
		return source + ":" + strconv.Itoa(len(value)) + ":" + value
	}
	return source + ":" + strconv.Itoa(len(value)) + ":" + value[:edge] + ":" + value[len(value)-edge:]
}

func decodeRoutingFields(raw map[string]json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	body := make(map[string]any, 4)
	for _, key := range []string{"metadata", "provider", "models", "user"} {
		value, ok := raw[key]
		if !ok {
			continue
		}
		var decoded any
		if err := json.Unmarshal(value, &decoded); err == nil {
			body[key] = decoded
		}
	}
	return body
}

// extractHints reads lazu-specific routing hints out of the request
// body. The supported locations, in priority order:
//
//   - body["metadata"]["lazu"]   ← preferred; nested object keeps the
//     lazu namespace separate from any other metadata the caller may
//     attach (e.g. Helicone properties).
//   - body["metadata"]              ← fallback; if the caller put the
//     hints at the top of metadata (intent / budget_tier / …), we
//     accept them there too.
//
// Returns nil when nothing relevant is found, so the rest of the
// pipeline can keep using `Features.Hints == nil` as the "legacy
// caller" sentinel.
func extractHints(body map[string]any) *RoutingHints {
	if body == nil {
		return nil
	}
	hints := &RoutingHints{}
	hasHint := false

	pullFromMap := func(m map[string]any) {
		if v, ok := m["intent"].(string); ok && v != "" {
			hints.Intent = v
			hasHint = true
		}
		if v, ok := m["budget_tier"].(string); ok && v != "" {
			hints.BudgetTier = v
			hasHint = true
		}
		if v, ok := m["preferred_provider"].(string); ok && v != "" {
			hints.PreferredProvider = v
			hasHint = true
		}
		if v, ok := m["session_id"].(string); ok && v != "" {
			hints.SessionID = v
			hasHint = true
		}
		if arr, ok := m["fallback_models"].([]any); ok && len(arr) > 0 {
			out := make([]string, 0, len(arr))
			for _, item := range arr {
				if s, ok := item.(string); ok && s != "" {
					out = append(out, s)
				}
			}
			if len(out) > 0 {
				hints.FallbackModels = out
				hasHint = true
			}
		}
	}

	if md, ok := body["metadata"].(map[string]any); ok {
		// Read fallback location first (top-level metadata.*) so the
		// preferred lazu-namespace can overwrite same-key collisions
		// — the namespace is authoritative.
		pullFromMap(md)
		if sub, ok := md["lazu"].(map[string]any); ok {
			pullFromMap(sub)
		}
	}

	if !hasHint {
		return nil
	}
	return hints
}

func canonicalize(raw []byte) []byte {
	var decoded any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		return bytes.TrimSpace(raw)
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		return bytes.TrimSpace(raw)
	}
	encoded, err := json.Marshal(decoded)
	if err != nil {
		return bytes.TrimSpace(raw)
	}
	return encoded
}

func sha256Hex(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func countImages(messages []any, f *Features) {
	for _, msg := range messages {
		m, ok := msg.(map[string]any)
		if !ok {
			continue
		}
		content, ok := m["content"].([]any)
		if !ok {
			continue
		}
		for _, part := range content {
			pm, ok := part.(map[string]any)
			if !ok {
				continue
			}
			if partType, _ := pm["type"].(string); partType == "image_url" || partType == "input_image" {
				f.HasVision = true
				f.VisionImageCount++
			}
		}
	}
}

// extractProviderPrefs parses the OpenRouter-style top-level
// `provider: {...}` object. Returns nil when the field is absent or
// malformed — never panics.
func extractProviderPrefs(body map[string]any) *ProviderPrefs {
	if body == nil {
		return nil
	}
	raw, ok := body["provider"].(map[string]any)
	if !ok {
		return nil
	}
	prefs := &ProviderPrefs{}
	hasField := false

	if v, ok := raw["sort"].(string); ok && v != "" {
		prefs.Sort = v
		hasField = true
	}
	if arr, ok := raw["only"].([]any); ok && len(arr) > 0 {
		prefs.Only = stringSliceFromAny(arr)
		if len(prefs.Only) > 0 {
			hasField = true
		}
	}
	if arr, ok := raw["ignore"].([]any); ok && len(arr) > 0 {
		prefs.Ignore = stringSliceFromAny(arr)
		if len(prefs.Ignore) > 0 {
			hasField = true
		}
	}
	if v, ok := raw["allow_fallbacks"].(bool); ok {
		b := v
		prefs.AllowFallbacks = &b
		hasField = true
	}
	if v, ok := raw["require_parameters"].(bool); ok && v {
		prefs.RequireParams = true
		hasField = true
	}
	if v, ok := raw["max_price_per_mtok"].(float64); ok && v > 0 {
		prefs.MaxPricePerMTok = v
		hasField = true
	}
	if v, ok := raw["data_collection"].(string); ok && v != "" {
		prefs.DataCollection = v
		hasField = true
	}
	if !hasField {
		return nil
	}
	return prefs
}

// extractModelsFallback reads the OpenRouter-style top-level `models`
// array. For backward compatibility we also accept the deprecated
// `metadata.lazu.fallback_models` location (PR #32 reserved field);
// the top-level path wins on collision.
//
// Returns nil for callers using only `model`.
func extractModelsFallback(body map[string]any, hints *RoutingHints) []string {
	if arr, ok := body["models"].([]any); ok && len(arr) > 0 {
		out := stringSliceFromAny(arr)
		if len(out) > 0 {
			return out
		}
	}
	// Fall back to the deprecated location.
	if hints != nil && len(hints.FallbackModels) > 0 {
		return hints.FallbackModels
	}
	return nil
}

// stringSliceFromAny filters a JSON-decoded array down to its non-empty
// string elements. Tolerates mixed-type arrays (numbers / bools get
// silently dropped) so a malformed caller doesn't kill the whole
// feature-extraction pass.
func stringSliceFromAny(arr []any) []string {
	out := make([]string, 0, len(arr))
	for _, item := range arr {
		if s, ok := item.(string); ok {
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
	}
	return out
}

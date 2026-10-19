package channel

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"

	"github.com/gin-gonic/gin"
	appcommon "github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/logger"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/internal/types"
)

const (
	legacySystemBeta      = "mid-conversation-system-2026-04-07"
	systemBetaRule        = "omit_unused_mid_conversation_system_beta"
	cacheScopeBeta        = "prompt-caching-scope-2026-01-05"
	cacheScopeRule        = "omit_unused_prompt_caching_scope_beta"
	contextManagementBeta = "context-management-2025-06-27"
	contextManagementRule = "omit_keep_all_context_management"
	maxCompatBodyBytes    = 32 << 20
)

// compatFeature names what one compatibility action removes, for errors and logs.
type compatFeature struct {
	rule, field, feature string
}

var (
	systemBetaFeature        = compatFeature{systemBetaRule, "anthropic-beta", legacySystemBeta}
	cacheScopeFeature        = compatFeature{cacheScopeRule, "anthropic-beta", cacheScopeBeta}
	contextManagementFeature = compatFeature{contextManagementRule, "context_management", contextManagementBeta}
)

// applyClaudeSystemBetaCompatibility is deliberately one opt-in action, not a
// general field-dropping engine. It never changes the caller's body or headers.
func applyClaudeSystemBetaCompatibility(c *gin.Context, req *http.Request, info *relaycommon.RelayInfo) error {
	if info == nil || info.ChannelMeta == nil || info.RelayFormat != types.RelayFormatClaude ||
		info.ChannelType != constant.ChannelTypeAnthropic || len(info.ChannelOtherSettings.ClaudeOmitUnusedSystemBetaModels) == 0 {
		return nil
	}
	if !hasBeta(c.Request.Header, legacySystemBeta) && !hasBeta(req.Header, legacySystemBeta) {
		return nil
	}
	models := info.ChannelOtherSettings.ClaudeOmitUnusedSystemBetaModels
	if err := info.ChannelOtherSettings.ValidateClaudeCompatibility(); err != nil {
		return claudeCompatibilityError(systemBetaFeature, types.ErrorCodeChannelCompatibilityInvalid, "invalid_model_scope")
	}
	// A channel-bound list is normally tiny. Only inspect a body when the
	// planned or raw (passthrough) model could match it.
	original, err := appcommon.GetRequestBody(c)
	if err != nil {
		return claudeCompatibilityError(systemBetaFeature, types.ErrorCodeChannelCompatibilityUnsupported, "unreadable_original_body")
	}
	originalObject, originalErr := compatJSONObject(original)
	var originalModel string
	if originalErr == nil {
		_ = json.Unmarshal(originalObject["model"], &originalModel)
	}
	if !slices.Contains(models, info.UpstreamModelName) && !slices.Contains(models, originalModel) {
		return nil
	}
	if originalErr != nil {
		return claudeCompatibilityError(systemBetaFeature, types.ErrorCodeChannelCompatibilityUnsupported, "ambiguous_original_body")
	}
	for key, value := range info.HeadersOverride {
		if strings.EqualFold(key, "anthropic-beta") {
			if text, ok := value.(string); ok && containsBeta(text, legacySystemBeta) {
				return claudeCompatibilityError(systemBetaFeature, types.ErrorCodeChannelCompatibilityInvalid, "header_override_conflict")
			}
		}
	}
	if req.Body == nil {
		return claudeCompatibilityError(systemBetaFeature, types.ErrorCodeChannelCompatibilityUnsupported, "missing_outbound_body")
	}
	outbound, err := io.ReadAll(io.LimitReader(req.Body, maxCompatBodyBytes+1))
	_ = req.Body.Close()
	if err != nil || len(outbound) > maxCompatBodyBytes {
		return claudeCompatibilityError(systemBetaFeature, types.ErrorCodeChannelCompatibilityUnsupported, "unreadable_outbound_body")
	}
	req.Body = io.NopCloser(bytes.NewReader(outbound))
	outboundObject, err := compatJSONObject(outbound)
	if err != nil {
		return claudeCompatibilityError(systemBetaFeature, types.ErrorCodeChannelCompatibilityUnsupported, "ambiguous_outbound_body")
	}
	var outboundModel string
	if json.Unmarshal(outboundObject["model"], &outboundModel) != nil || outboundModel != info.UpstreamModelName {
		return claudeCompatibilityError(systemBetaFeature, types.ErrorCodeChannelCompatibilityInvalid, "outbound_model_mismatch")
	}
	// Exact scope is based on the final model, not a client alias.
	if !slices.Contains(models, outboundModel) {
		return nil
	}
	for _, object := range []map[string]json.RawMessage{originalObject, outboundObject} {
		if reason := systemFeatureUse(object); reason != "" {
			return claudeCompatibilityError(systemBetaFeature, types.ErrorCodeChannelCompatibilityUnsupported, reason)
		}
	}
	dropBetas(req.Header, legacySystemBeta)
	canonicalModels := slices.Clone(models)
	slices.Sort(canonicalModels)
	fingerprint := sha256.Sum256([]byte(strings.Join(canonicalModels, "\n")))
	logger.LogInfo(c, fmt.Sprintf("compatibility rule=%s channel_id=%d model=%s config=%x version=%s", systemBetaRule, info.ChannelId, outboundModel, fingerprint[:8], appcommon.Version))
	return nil
}

func containsBeta(value, target string) bool {
	for _, flag := range strings.Split(value, ",") {
		if strings.TrimSpace(flag) == target {
			return true
		}
	}
	return false
}

func hasBeta(headers http.Header, target string) bool {
	for _, value := range headers.Values("anthropic-beta") {
		if containsBeta(value, target) {
			return true
		}
	}
	return false
}

func claudeCompatibilityError(feature compatFeature, code types.ErrorCode, reason string) *types.LazuError {
	message := types.CatalogMessage(code, nil)
	return types.NewErrorWithStatusCode(errors.New(message+" ("+reason+")"), code, http.StatusBadRequest,
		types.ErrOptionWithSkipRetry(), types.ErrOptionWithDetails(map[string]any{
			"origin": "gateway_compatibility", "attempt_provider_attempted": false,
			"rule": feature.rule, "field": feature.field, "feature": feature.feature,
			"compatibility_reason": reason, "retryable": false,
		}))
}

// Mid-conversation instructions, per-message effort and tool changes all use
// role=system: https://platform.claude.com/docs/en/build-with-claude/mid-conversation-system-messages
// Unknown message shapes are not proof that the feature is unused. Top-level
// system and opaque content values do not change a message's role.
func systemFeatureUse(object map[string]json.RawMessage) string {
	var messages []json.RawMessage
	if json.Unmarshal(object["messages"], &messages) != nil || len(messages) == 0 {
		return "unknown_message_shape"
	}
	for _, raw := range messages {
		message, err := compatJSONObject(raw)
		if err != nil {
			return "ambiguous_message_shape"
		}
		var role string
		if json.Unmarshal(message["role"], &role) != nil {
			return "unknown_message_role"
		}
		if role == "system" {
			return "mid_conversation_system_used"
		}
		if role != "user" && role != "assistant" {
			return "unknown_message_role"
		}
		for key := range message {
			if key != "role" && key != "content" {
				return "unknown_message_field"
			}
		}
		content := bytes.TrimSpace(message["content"])
		if len(content) == 0 || (content[0] != '"' && content[0] != '[') {
			return "unknown_message_content"
		}
	}
	return ""
}

type compatMember struct {
	key   string
	value json.RawMessage
}

// Decode the relevant object without losing duplicate-key evidence (including
// escaped spellings). RawMessage preserves nested unknown extensions as-is.
func compatJSONObject(body []byte) (map[string]json.RawMessage, error) {
	members, err := compatJSONMembers(body)
	if err != nil {
		return nil, err
	}
	object := make(map[string]json.RawMessage, len(members))
	for _, member := range members {
		object[member.key] = member.value
	}
	return object, nil
}

// compatJSONMembers is compatJSONObject in wire order, so a rewrite can keep
// every other member byte-for-byte.
func compatJSONMembers(body []byte) ([]compatMember, error) {
	if len(body) > maxCompatBodyBytes {
		return nil, errors.New("body exceeds compatibility inspection limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, errors.New("expected object")
	}
	var members []compatMember
	seen := make(map[string]bool)
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		key, ok := token.(string)
		if !ok || seen[key] {
			return nil, errors.New("ambiguous object key")
		}
		seen[key] = true
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		members = append(members, compatMember{key, value})
	}
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, errors.New("trailing JSON")
	}
	return members, nil
}

package channel

import (
	"bytes"
	"encoding/json"
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
	thinkingCountBeta = "thinking-token-count-2026-05-13"
	thinkingCountRule = "omit_thinking_token_count_beta"
	toolChangesBeta   = "mid-conversation-tool-changes-2026-07-01"
	foldSystemRule    = "fold_system_turns_into_user_turns"
)

var (
	thinkingCountFeature = compatFeature{thinkingCountRule, "anthropic-beta", thinkingCountBeta}
	foldSystemFeature    = compatFeature{foldSystemRule, "messages", legacySystemBeta}
)

// applyClaudeBedrockCompatibility runs the opt-in actions that Bedrock-backed
// Anthropic upstreams need for current Claude Code requests. Depending on the
// backend, Bedrock rejects the prompt-caching-scope, thinking-token-count and
// mid-conversation betas, role "system" turns, or context_management. Each
// action is scoped to exact upstream models, changes only what the request
// does not rely on, and refuses the request when use is certain or unclear.
func applyClaudeBedrockCompatibility(c *gin.Context, req *http.Request, info *relaycommon.RelayInfo) error {
	if info == nil || info.ChannelMeta == nil || info.RelayFormat != types.RelayFormatClaude ||
		info.ChannelType != constant.ChannelTypeAnthropic {
		return nil
	}
	settings := info.ChannelOtherSettings
	model := info.UpstreamModelName
	scopeOn := slices.Contains(settings.ClaudeOmitUnusedCacheScopeBetaModels, model) && hasBeta(req.Header, cacheScopeBeta)
	contextOn := slices.Contains(settings.ClaudeOmitKeepAllContextManagementModels, model)
	foldOn := slices.Contains(settings.ClaudeFoldSystemTurnsModels, model)
	countOn := slices.Contains(settings.ClaudeOmitThinkingTokenCountBetaModels, model) && hasBeta(req.Header, thinkingCountBeta)
	active := []struct {
		on      bool
		feature compatFeature
		betas   []string
	}{
		{scopeOn, cacheScopeFeature, []string{cacheScopeBeta}},
		{contextOn, contextManagementFeature, []string{contextManagementBeta}},
		{foldOn, foldSystemFeature, []string{legacySystemBeta, toolChangesBeta}},
		{countOn, thinkingCountFeature, []string{thinkingCountBeta}},
	}
	var first *compatFeature
	for i := range active {
		if active[i].on && first == nil {
			first = &active[i].feature
		}
	}
	if first == nil {
		return nil
	}
	if err := settings.ValidateClaudeCompatibility(); err != nil {
		return claudeCompatibilityError(*first, types.ErrorCodeChannelCompatibilityInvalid, "invalid_model_scope")
	}
	for key, value := range info.HeadersOverride {
		if !strings.EqualFold(key, "anthropic-beta") {
			continue
		}
		text, _ := value.(string)
		for _, action := range active {
			for _, beta := range action.betas {
				if action.on && containsBeta(text, beta) {
					return claudeCompatibilityError(action.feature, types.ErrorCodeChannelCompatibilityInvalid, "header_override_conflict")
				}
			}
		}
	}
	if scopeOn || contextOn || foldOn {
		if err := applyClaudeBedrockBodyActions(c, req, info, scopeOn, contextOn, foldOn, *first); err != nil {
			return err
		}
	}
	// thinking-token-count only adds a thinking breakdown to response usage;
	// nothing in the request depends on it and billing does not read it.
	if countOn {
		dropBetas(req.Header, thinkingCountBeta)
		logCompatibility(c, thinkingCountRule, info.ChannelId, model)
	}
	return nil
}

func applyClaudeBedrockBodyActions(c *gin.Context, req *http.Request, info *relaycommon.RelayInfo, scopeOn, contextOn, foldOn bool, feature compatFeature) error {
	model := info.UpstreamModelName
	// Check the caller's body as well as the wire body, so a field the DTO path
	// dropped is never mistaken for an unused one.
	original, err := appcommon.GetRequestBody(c)
	if err != nil {
		return claudeCompatibilityError(feature, types.ErrorCodeChannelCompatibilityUnsupported, "unreadable_original_body")
	}
	originalObject, err := compatJSONObject(original)
	if err != nil {
		return claudeCompatibilityError(feature, types.ErrorCodeChannelCompatibilityUnsupported, "ambiguous_original_body")
	}
	if req.Body == nil {
		return claudeCompatibilityError(feature, types.ErrorCodeChannelCompatibilityUnsupported, "missing_outbound_body")
	}
	outbound, err := io.ReadAll(io.LimitReader(req.Body, maxCompatBodyBytes+1))
	_ = req.Body.Close()
	if err != nil || len(outbound) > maxCompatBodyBytes {
		return claudeCompatibilityError(feature, types.ErrorCodeChannelCompatibilityUnsupported, "unreadable_outbound_body")
	}
	setCompatBody(req, outbound)
	members, err := compatJSONMembers(outbound)
	if err != nil {
		return claudeCompatibilityError(feature, types.ErrorCodeChannelCompatibilityUnsupported, "ambiguous_outbound_body")
	}
	outboundObject := make(map[string]json.RawMessage, len(members))
	for _, member := range members {
		outboundObject[member.key] = member.value
	}
	var outboundModel string
	if json.Unmarshal(outboundObject["model"], &outboundModel) != nil || outboundModel != model {
		return claudeCompatibilityError(feature, types.ErrorCodeChannelCompatibilityInvalid, "outbound_model_mismatch")
	}

	if scopeOn {
		for _, object := range []map[string]json.RawMessage{originalObject, outboundObject} {
			if reason := cacheScopeUse(object); reason != "" {
				return claudeCompatibilityError(cacheScopeFeature, types.ErrorCodeChannelCompatibilityUnsupported, reason)
			}
		}
	}
	if contextOn {
		for _, object := range []map[string]json.RawMessage{originalObject, outboundObject} {
			if raw, ok := object["context_management"]; ok {
				if reason := contextManagementUse(raw); reason != "" {
					return claudeCompatibilityError(contextManagementFeature, types.ErrorCodeChannelCompatibilityUnsupported, reason)
				}
			}
		}
	}

	var folded json.RawMessage
	if foldOn {
		if _, _, reason := foldSystemTurns(originalObject["messages"]); reason != "" {
			return claudeCompatibilityError(foldSystemFeature, types.ErrorCodeChannelCompatibilityUnsupported, reason)
		}
		messages, changed, reason := foldSystemTurns(outboundObject["messages"])
		if reason != "" {
			return claudeCompatibilityError(foldSystemFeature, types.ErrorCodeChannelCompatibilityUnsupported, reason)
		}
		if changed {
			folded = messages
		}
	}

	_, contextInBody := outboundObject["context_management"]
	contextInBody = contextOn && contextInBody
	if folded != nil || contextInBody {
		replace := map[string]json.RawMessage{}
		if folded != nil {
			replace["messages"] = folded
		}
		setCompatBody(req, compatJSONRebuild(members, contextInBody, "context_management", replace))
	}
	if scopeOn {
		dropBetas(req.Header, cacheScopeBeta)
		logCompatibility(c, cacheScopeRule, info.ChannelId, model)
	}
	if contextOn && (contextInBody || hasBeta(req.Header, contextManagementBeta)) {
		dropBetas(req.Header, contextManagementBeta)
		logCompatibility(c, contextManagementRule, info.ChannelId, model)
	}
	if foldOn {
		// No system turn is left on the wire, so both mid-conversation betas are unused.
		dropBetas(req.Header, legacySystemBeta, toolChangesBeta)
		if folded != nil {
			logCompatibility(c, foldSystemRule, info.ChannelId, model)
		}
	}
	return nil
}

// foldSystemTurns moves each mid-conversation system turn into the user turn it
// directly follows, which is where Claude Code places them. Only plain text is
// moved; per-turn fields, tool changes, or any other position are refused
// because a user turn cannot carry them. The instructions keep their text and
// cache markers but lose system authority; Claude Code already wraps them in
// <system-reminder> tags.
func foldSystemTurns(raw json.RawMessage) (json.RawMessage, bool, string) {
	var messages []json.RawMessage
	if json.Unmarshal(raw, &messages) != nil {
		return nil, false, "unknown_message_shape"
	}
	type turn struct {
		raw    json.RawMessage
		role   string
		blocks []json.RawMessage
		folded bool
	}
	var out []*turn
	changed := false
	for _, message := range messages {
		object, err := compatJSONObject(message)
		if err != nil {
			return nil, false, "ambiguous_message_shape"
		}
		var role string
		if json.Unmarshal(object["role"], &role) != nil {
			return nil, false, "unknown_message_role"
		}
		if role != "system" {
			out = append(out, &turn{raw: message, role: role})
			continue
		}
		for key := range object {
			if key != "role" && key != "content" {
				return nil, false, "system_turn_field"
			}
		}
		blocks, ok := textBlocks(object["content"], true)
		if !ok {
			return nil, false, "system_turn_content"
		}
		if len(out) == 0 || out[len(out)-1].role != "user" {
			return nil, false, "system_turn_position"
		}
		previous := out[len(out)-1]
		if !previous.folded {
			userObject, err := compatJSONObject(previous.raw)
			if err != nil {
				return nil, false, "ambiguous_message_shape"
			}
			for key := range userObject {
				if key != "role" && key != "content" {
					return nil, false, "unknown_message_field"
				}
			}
			userBlocks, ok := textBlocks(userObject["content"], false)
			if !ok {
				return nil, false, "unknown_message_content"
			}
			previous.blocks, previous.folded = userBlocks, true
		}
		previous.blocks = append(previous.blocks, blocks...)
		changed = true
	}
	if !changed {
		return raw, false, ""
	}
	var buf bytes.Buffer
	buf.WriteByte('[')
	for i, t := range out {
		if i > 0 {
			buf.WriteByte(',')
		}
		if !t.folded {
			buf.Write(t.raw)
			continue
		}
		buf.WriteString(`{"role":"user","content":[`)
		for j, block := range t.blocks {
			if j > 0 {
				buf.WriteByte(',')
			}
			buf.Write(block)
		}
		buf.WriteString(`]}`)
	}
	buf.WriteByte(']')
	return buf.Bytes(), true, ""
}

// textBlocks returns message content as a block list, turning a string into one
// text block. With onlyText, every block must be a text block with nothing but
// text and an optional cache_control; otherwise any block is kept as-is.
func textBlocks(raw json.RawMessage, onlyText bool) ([]json.RawMessage, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, false
	}
	if trimmed[0] == '"' {
		var text string
		if json.Unmarshal(trimmed, &text) != nil {
			return nil, false
		}
		return []json.RawMessage{compatTextBlock(text)}, true
	}
	var blocks []json.RawMessage
	if trimmed[0] != '[' || json.Unmarshal(trimmed, &blocks) != nil {
		return nil, false
	}
	if !onlyText {
		return blocks, true
	}
	for _, block := range blocks {
		object, err := compatJSONObject(block)
		if err != nil {
			return nil, false
		}
		var kind, text string
		if json.Unmarshal(object["type"], &kind) != nil || kind != "text" || json.Unmarshal(object["text"], &text) != nil {
			return nil, false
		}
		for key := range object {
			if key != "type" && key != "text" && key != "cache_control" {
				return nil, false
			}
		}
	}
	return blocks, true
}

func compatTextBlock(text string) json.RawMessage {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}{"text", text})
	return bytes.TrimRight(buf.Bytes(), "\n")
}

// cacheScopeUse reports whether any cache_control asks for more than a default
// ephemeral breakpoint (type plus optional ttl); only such markers could need
// the scope beta. Tool inputs and schemas are caller data and are not scanned.
func cacheScopeUse(object map[string]json.RawMessage) string {
	for _, key := range []string{"system", "tools", "messages"} {
		raw, ok := object[key]
		if !ok {
			continue
		}
		var value any
		if json.Unmarshal(raw, &value) != nil {
			return "ambiguous_cache_control"
		}
		if reason := cacheControlUse(value); reason != "" {
			return reason
		}
	}
	return ""
}

func cacheControlUse(value any) string {
	switch v := value.(type) {
	case []any:
		for _, item := range v {
			if reason := cacheControlUse(item); reason != "" {
				return reason
			}
		}
	case map[string]any:
		for key, item := range v {
			switch key {
			case "input", "input_schema":
				continue
			case "cache_control":
				control, ok := item.(map[string]any)
				if !ok || control["type"] != "ephemeral" {
					return "ambiguous_cache_control"
				}
				for field := range control {
					if field != "type" && field != "ttl" {
						return "cache_scope_used"
					}
				}
			default:
				if reason := cacheControlUse(item); reason != "" {
					return reason
				}
			}
		}
	}
	return ""
}

// contextManagementUse accepts only edits that keep everything, which is what
// Claude Code sends by default: clear_thinking with keep "all". Any other edit
// asks the upstream to clear or compact context and cannot be skipped silently.
func contextManagementUse(raw json.RawMessage) string {
	object, err := compatJSONObject(raw)
	if err != nil {
		return "ambiguous_context_management"
	}
	for key := range object {
		if key != "edits" {
			return "context_management_used"
		}
	}
	editsRaw, ok := object["edits"]
	if !ok {
		return ""
	}
	var edits []json.RawMessage
	if json.Unmarshal(editsRaw, &edits) != nil {
		return "ambiguous_context_management"
	}
	for _, editRaw := range edits {
		edit, err := compatJSONObject(editRaw)
		if err != nil {
			return "ambiguous_context_management"
		}
		var editType, keep string
		if json.Unmarshal(edit["type"], &editType) != nil {
			return "ambiguous_context_management"
		}
		if !strings.HasPrefix(editType, "clear_thinking_") || json.Unmarshal(edit["keep"], &keep) != nil || keep != "all" {
			return "context_management_used"
		}
		for key := range edit {
			if key != "type" && key != "keep" {
				return "context_management_used"
			}
		}
	}
	return ""
}

// compatJSONRebuild rebuilds an object, optionally dropping one top-level member
// and replacing others. Every untouched member keeps its order and exact bytes.
func compatJSONRebuild(members []compatMember, dropOn bool, drop string, replace map[string]json.RawMessage) []byte {
	var out bytes.Buffer
	out.WriteByte('{')
	first := true
	for _, member := range members {
		if dropOn && member.key == drop {
			continue
		}
		if !first {
			out.WriteByte(',')
		}
		first = false
		key, _ := json.Marshal(member.key)
		out.Write(key)
		out.WriteByte(':')
		if value, ok := replace[member.key]; ok {
			out.Write(value)
		} else {
			out.Write(member.value)
		}
	}
	out.WriteByte('}')
	return out.Bytes()
}

func setCompatBody(req *http.Request, body []byte) {
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.ContentLength = int64(len(body))
	req.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(body)), nil }
}

// dropBetas removes the given flags from every anthropic-beta value and keeps
// the rest in order.
func dropBetas(headers http.Header, drop ...string) {
	var kept []string
	for _, value := range headers.Values("anthropic-beta") {
		for _, flag := range strings.Split(value, ",") {
			flag = strings.TrimSpace(flag)
			if flag != "" && !slices.Contains(drop, flag) {
				kept = append(kept, flag)
			}
		}
	}
	headers.Del("anthropic-beta")
	if len(kept) > 0 {
		headers.Set("anthropic-beta", strings.Join(kept, ","))
	}
}

func logCompatibility(c *gin.Context, rule string, channelID int, model string) {
	logger.LogInfo(c, fmt.Sprintf("compatibility rule=%s channel_id=%d model=%s version=%s", rule, channelID, model, appcommon.Version))
}

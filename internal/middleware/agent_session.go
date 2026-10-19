package middleware

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
	relayrouter "github.com/lazu-ai/lazu/internal/relay/router"
	"github.com/tidwall/gjson"
)

// agentSession is how a request belongs to a task. Coding agents already send
// their own conversation id with every model call; recognising it lets the
// gateway group a task's requests, count its sub-agents and show the task in
// the console. See memory/reference-agent-session-ids for the source-verified
// table this follows.
type agentSession struct {
	// ID groups every request of one task, including its sub-agents.
	ID string
	// Source names where ID came from, so views can tell a real agent session
	// from the per-Key fallback.
	Source string
	// ThreadID is the conversation thread inside the task (a sub-agent has its
	// own); ParentThreadID is the thread that spawned it.
	ThreadID       string
	ParentThreadID string
	// Kind is main / subagent / compact / review.
	Kind string
}

const (
	SessionSourceCodex         = "codex"
	SessionSourceClaudeCode    = "claude-code"
	SessionSourceOpenCode      = "opencode"
	SessionSourceKilo          = "kilo"
	SessionSourceHeader        = "x-session-id"
	SessionSourceGoose         = "goose"
	SessionSourceCline         = "cline"
	SessionSourceMetadata      = "metadata"
	SessionSourcePromptCache   = "prompt_cache_key"
	SessionSourceClientRequest = "client-request-id"
	SessionSourceToken         = "token"

	RequestKindMain     = "main"
	RequestKindSubagent = "subagent"
	RequestKindCompact  = "compact"
	RequestKindReview   = "review"
)

const sessionIDLimit = 64

func headerValue(c *gin.Context, names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(c.GetHeader(name)); value != "" {
			return clipSessionValue(value)
		}
	}
	return ""
}

func clipSessionValue(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > sessionIDLimit {
		return value[:sessionIDLimit]
	}
	return value
}

var legacyClaudeCodeSession = regexp.MustCompile(`_session_([0-9a-fA-F-]{36})`)

// resolveAgentSession picks the most specific task identifier the request
// carries. Headers an agent sets itself win over body fields, which win over
// the per-Key fallback.
func resolveAgentSession(c *gin.Context, body []byte, features *relayrouter.Features, tokenID int, client string) agentSession {
	if c == nil {
		return agentSession{}
	}
	kind := RequestKindMain

	// Codex (codex-rs): session-id is the root thread and is shared by every
	// sub-agent; thread-id is the thread making this call. Older releases
	// spelled them session_id / conversation_id.
	if id := headerValue(c, "session-id", "session_id", "conversation_id"); id != "" && looksLikeCodex(c, client) {
		session := agentSession{ID: id, Source: SessionSourceCodex, Kind: kind,
			ThreadID:       headerValue(c, "thread-id", "thread_id"),
			ParentThreadID: headerValue(c, "x-codex-parent-thread-id"),
		}
		switch strings.ToLower(headerValue(c, "x-openai-subagent")) {
		case "":
		case "review":
			session.Kind = RequestKindReview
		case "compact":
			session.Kind = RequestKindCompact
		default:
			session.Kind = RequestKindSubagent
		}
		if meta := strings.TrimSpace(c.GetHeader("x-codex-turn-metadata")); meta != "" && session.Kind == RequestKindMain {
			switch strings.ToLower(gjson.Get(meta, "request_kind").String()) {
			case "compact", "compaction":
				session.Kind = RequestKindCompact
			case "review":
				session.Kind = RequestKindReview
			}
		}
		if session.Kind == RequestKindMain && session.ParentThreadID != "" {
			session.Kind = RequestKindSubagent
		}
		if session.ThreadID == session.ID {
			session.ThreadID = ""
		}
		return session
	}

	// Claude Code: one session per CLI process; Task sub-agents share it and
	// add their own agent id.
	if id := headerValue(c, "X-Claude-Code-Session-Id"); id != "" {
		return claudeCodeSession(c, id)
	}
	if id := claudeCodeMetadataSessionFromContext(c, body); id != "" {
		return claudeCodeSession(c, id)
	}

	// Kilo Code's own task headers, then the OpenCode family: a sub-agent is a
	// child session, so the parent is the task and the child its thread.
	if id := headerValue(c, "X-KILOCODE-TASKID"); id != "" {
		if parent := headerValue(c, "X-KILOCODE-PARENT-TASKID"); parent != "" {
			return agentSession{ID: parent, Source: SessionSourceKilo, ThreadID: id, Kind: RequestKindSubagent}
		}
		return agentSession{ID: id, Source: SessionSourceKilo, Kind: kind}
	}
	if id := headerValue(c, "X-Session-Id", "x-session-affinity", "x-opencode-session"); id != "" {
		source := SessionSourceHeader
		if client == "opencode" || client == "kilo-code" || strings.HasPrefix(id, "ses_") {
			source = SessionSourceOpenCode
		}
		if parent := headerValue(c, "x-parent-session-id"); parent != "" {
			return agentSession{ID: parent, Source: source, ThreadID: id, ParentThreadID: parent, Kind: RequestKindSubagent}
		}
		return agentSession{ID: id, Source: source, Kind: kind}
	}
	if id := headerValue(c, "agent-session-id"); id != "" {
		return agentSession{ID: id, Source: SessionSourceGoose, Kind: kind}
	}
	if id := headerValue(c, "X-Task-ID"); id != "" {
		return agentSession{ID: id, Source: SessionSourceCline, Kind: kind}
	}

	// Body fields: an explicit metadata.session_id, then prompt_cache_key,
	// which Codex, pi and several SDK callers set to their session.
	if features != nil && features.Hints != nil && features.Hints.SessionID != "" {
		return agentSession{ID: clipSessionValue(features.Hints.SessionID), Source: SessionSourceMetadata, Kind: kind}
	}
	if key := promptCacheSessionFromContext(c, body); key != "" {
		return agentSession{ID: key, Source: SessionSourcePromptCache, Kind: kind}
	}
	if client == "pi" || client == "openclaw" {
		if id := headerValue(c, "x-client-request-id"); id != "" {
			return agentSession{ID: id, Source: SessionSourceClientRequest, Kind: kind}
		}
	}
	if tokenID > 0 {
		// No id at all: group the Key's requests per UTC day, so one Key's
		// traffic reads as one row per day rather than one endless session.
		return agentSession{ID: fmt.Sprintf("token-%d-%s", tokenID, time.Now().UTC().Format("20060102")), Source: SessionSourceToken, Kind: kind}
	}
	return agentSession{}
}

func looksLikeCodex(c *gin.Context, client string) bool {
	if client == "codex" {
		return true
	}
	originator := strings.ToLower(c.GetHeader("originator"))
	return strings.HasPrefix(originator, "codex") || c.GetHeader("thread-id") != "" || c.GetHeader("x-codex-turn-metadata") != ""
}

func claudeCodeSession(c *gin.Context, id string) agentSession {
	session := agentSession{ID: id, Source: SessionSourceClaudeCode, Kind: RequestKindMain,
		ThreadID:       headerValue(c, "x-claude-code-agent-id"),
		ParentThreadID: headerValue(c, "x-claude-code-parent-agent-id"),
	}
	if session.ThreadID != "" {
		session.Kind = RequestKindSubagent
	}
	switch strings.ToLower(headerValue(c, "x-claude-code-request-class")) {
	case "compaction":
		session.Kind = RequestKindCompact
	case "subagent", "workflow":
		session.Kind = RequestKindSubagent
	}
	return session
}

// claudeCodeMetadataSession reads the session Claude Code writes into
// metadata.user_id: a JSON string {"device_id","account_uuid","session_id"}
// in current releases, "user_<hash>_account_<uuid>_session_<uuid>" before.
func claudeCodeMetadataSession(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	raw := gjson.GetBytes(body, "metadata.user_id").String()
	return parseClaudeCodeMetadataSession(raw)
}

func parseClaudeCodeMetadataSession(raw string) string {
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(strings.TrimSpace(raw), "{") {
		var parsed struct {
			SessionID string `json:"session_id"`
		}
		if json.Unmarshal([]byte(raw), &parsed) == nil {
			return clipSessionValue(parsed.SessionID)
		}
		return ""
	}
	if match := legacyClaudeCodeSession.FindStringSubmatch(raw); len(match) == 2 {
		return match[1]
	}
	return ""
}

// firstUserMessage returns the first thing the person typed in this task.
// Agents resend the whole history on every call, so any request of a task
// carries it. Context the agent injects into the user turn (system reminders,
// environment blocks, AGENTS.md) is skipped.
func firstUserMessage(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	pick := func(items gjson.Result) string {
		found := ""
		items.ForEach(func(_, item gjson.Result) bool {
			role := item.Get("role").String()
			if role != "user" {
				return true
			}
			text := userText(item.Get("content"))
			if text != "" {
				found = text
				return false
			}
			return true
		})
		return found
	}
	if messages := gjson.GetBytes(body, "messages"); messages.IsArray() {
		return pick(messages)
	}
	input := gjson.GetBytes(body, "input")
	if input.Type == gjson.String {
		return strings.TrimSpace(input.String())
	}
	if input.IsArray() {
		return pick(input)
	}
	return ""
}

func userText(content gjson.Result) string {
	if content.Type == gjson.String {
		text := strings.TrimSpace(content.String())
		if isInjectedContext(text) {
			return ""
		}
		return text
	}
	var parts []string
	content.ForEach(func(_, part gjson.Result) bool {
		switch part.Get("type").String() {
		case "text", "input_text":
			text := strings.TrimSpace(part.Get("text").String())
			if text != "" && !isInjectedContext(text) {
				parts = append(parts, text)
			}
		}
		return true
	})
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

func isInjectedContext(text string) bool {
	lower := strings.ToLower(text)
	for _, prefix := range []string{
		"<system-reminder>", "<environment_context>", "<user_instructions>",
		"<command-", "<local-command", "caveat:", "# agents.md instructions",
		"<permissions instructions>", "<ide_",
	} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

// firstMessageLimit caps what one session stores (the person's own opening
// message, not pasted files); 64 KiB is far past any typed prompt.
const firstMessageLimit = 64 << 10

func clipFirstMessage(text string) string {
	if len(text) <= firstMessageLimit {
		return text
	}
	cut := text[:firstMessageLimit]
	for len(cut) > 0 && !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	return cut
}

// seenSessions remembers which sessions this process has already sent a
// first message for, so the history is parsed once per task rather than on
// every call. The session table ignores a repeat anyway; this only saves work.
var (
	seenSessions     sync.Map
	seenSessionCount atomic.Int64
)

const seenSessionLimit = 50000

func markSessionSeen(key string) bool {
	if _, loaded := seenSessions.LoadOrStore(key, struct{}{}); loaded {
		return false
	}
	if seenSessionCount.Add(1) > seenSessionLimit {
		seenSessions.Clear()
		seenSessionCount.Store(0)
	}
	return true
}

// applyAgentSession resolves the request's client and task and publishes them
// on the context for the log writer. It is idempotent per request.
func applyAgentSession(c *gin.Context, features *relayrouter.Features, tokenID int) agentSession {
	if c == nil {
		return agentSession{}
	}
	if existing := common.GetContextKeyString(c, constant.ContextKeySessionID); existing != "" {
		return agentSession{ID: existing, Source: common.GetContextKeyString(c, constant.ContextKeySessionSource)}
	}
	clientInfo := common.ParseClientRequest(c.GetHeader("User-Agent"), c.GetHeader("originator"), c.GetHeader("x-app"))
	if clientInfo.Raw != "" {
		common.SetContextKey(c, constant.ContextKeyClientUserAgent, clientInfo.Raw)
	}
	if clientInfo.Framework != "" {
		common.SetContextKey(c, constant.ContextKeyClientFramework, clientInfo.Framework)
	}
	if clientInfo.Version != "" {
		common.SetContextKey(c, constant.ContextKeyClientVersion, clientInfo.Version)
	}
	var body []byte
	if strings.Contains(strings.ToLower(c.GetHeader("Content-Type")), "json") {
		body, _ = common.GetRequestBody(c)
	}
	session := resolveAgentSession(c, body, features, tokenID, clientInfo.Framework)
	if session.ID == "" {
		return session
	}
	common.SetContextKey(c, constant.ContextKeySessionID, session.ID)
	common.SetContextKey(c, constant.ContextKeySessionSource, session.Source)
	common.SetContextKey(c, constant.ContextKeyThreadID, session.ThreadID)
	common.SetContextKey(c, constant.ContextKeyParentThreadID, session.ParentThreadID)
	common.SetContextKey(c, constant.ContextKeyRequestKind, session.Kind)
	if common.ModelLogEnabled && session.Source != SessionSourceToken && len(body) > 0 {
		key := fmt.Sprintf("%d:%s", c.GetInt("id"), session.ID)
		if markSessionSeen(key) {
			if text := clipFirstMessage(firstUserMessageFromContext(c, body)); text != "" {
				common.SetContextKey(c, constant.ContextKeySessionFirstMessage, text)
			}
		}
	}
	return session
}

// Parsed text-generation requests already retain session fields and user text.
// Keep legacy wire readers only for Claude/Gemini routes without an IR yet.
func claudeCodeMetadataSessionFromContext(c *gin.Context, body []byte) string {
	if parsed, ok := common.GetContextKeyType[*textgen.ParsedRequest](c, constant.ContextKeyTextgenParsed); ok && parsed != nil && parsed.Semantic != nil {
		return parseClaudeCodeMetadataSession(gjson.GetBytes(parsed.Semantic.Metadata.Metadata, "user_id").String())
	}
	return claudeCodeMetadataSession(body)
}
func promptCacheSessionFromContext(c *gin.Context, body []byte) string {
	if parsed, ok := common.GetContextKeyType[*textgen.ParsedRequest](c, constant.ContextKeyTextgenParsed); ok && parsed != nil && parsed.Semantic != nil {
		return clipSessionValue(gjson.ParseBytes(parsed.Semantic.Metadata.PromptCacheKey).String())
	}
	return clipSessionValue(gjson.GetBytes(body, "prompt_cache_key").String())
}
func firstUserMessageFromContext(c *gin.Context, body []byte) string {
	if parsed, ok := common.GetContextKeyType[*textgen.ParsedRequest](c, constant.ContextKeyTextgenParsed); ok && parsed != nil && parsed.Semantic != nil {
		for _, item := range parsed.Semantic.Conversation {
			if item.Kind != textgen.InputMessage || item.Role != "user" {
				continue
			}
			var parts []string
			for _, part := range item.Content {
				text := strings.TrimSpace(part.Text)
				if part.Kind == textgen.ContentText && text != "" && !isInjectedContext(text) {
					parts = append(parts, text)
				}
			}
			if len(parts) > 0 {
				return strings.Join(parts, "\n")
			}
		}
		return ""
	}
	return firstUserMessage(body)
}

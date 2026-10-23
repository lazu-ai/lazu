// Package telegram talks to the Telegram Bot API for this deployment's bot
// (common.AppConfig.Telegram). It imports only common, so any layer can use
// it; nothing here runs when no bot token is configured.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
)

// apiBase is the default Bot API origin; telegram.api_base overrides it.
var apiBase = "https://api.telegram.org"

func botAPIBase() string {
	if v := strings.TrimRight(strings.TrimSpace(common.AppConfig.Telegram.APIBase), "/"); v != "" {
		return v
	}
	return apiBase
}

// MaxMessageRunes is Telegram's limit on a message's text after entity
// parsing. Text is cut below it so the HTML markup still fits.
const MaxMessageRunes = 4096

var httpClient = &http.Client{Timeout: 10 * time.Second}

// Configured reports whether a bot token is set.
func Configured() bool { return token() != "" }

func token() string { return strings.TrimSpace(common.AppConfig.Telegram.BotToken) }

// Chat names where a message goes: a chat and, in a forum group, one topic.
type Chat struct {
	ID       string
	ThreadID int64
}

// ParseChat reads "<chat id>" or "<chat id>:<topic id>". Chat ids are
// numeric (-100… for groups) or an @username for public channels.
func ParseChat(raw string) (Chat, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Chat{}, false
	}
	id, thread, hasThread := strings.Cut(raw, ":")
	id = strings.TrimSpace(id)
	if id == "" {
		return Chat{}, false
	}
	if !strings.HasPrefix(id, "@") {
		if _, err := strconv.ParseInt(id, 10, 64); err != nil {
			return Chat{}, false
		}
	}
	chat := Chat{ID: id}
	if hasThread {
		n, err := strconv.ParseInt(strings.TrimSpace(thread), 10, 64)
		if err != nil || n <= 0 {
			return Chat{}, false
		}
		chat.ThreadID = n
	}
	return chat, true
}

// APIError is a request Telegram answered with ok=false. Description is
// Telegram's own sentence ("Bad Request: chat not found") and carries no
// secret, so an operator may see it.
type APIError struct {
	Code        int
	Description string
	RetryAfter  int
}

func (e *APIError) Error() string {
	return fmt.Sprintf("telegram: %d %s", e.Code, e.Description)
}

// ErrNotConfigured is returned when no bot token is set.
var ErrNotConfigured = errors.New("telegram: no bot token configured")

type apiResponse struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result"`
	ErrorCode   int             `json:"error_code"`
	Description string          `json:"description"`
	Parameters  struct {
		RetryAfter int `json:"retry_after"`
	} `json:"parameters"`
}

// Call invokes one Bot API method with a JSON body and decodes its result
// into out (which may be nil). Errors never contain the bot token: the
// token is part of the request URL, and net/http puts that URL into its
// errors, so transport errors are reduced to their cause.
func Call(ctx context.Context, method string, body any, out any) error {
	tok := token()
	if tok == "" {
		return ErrNotConfigured
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("telegram %s: encode: %w", method, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, botAPIBase()+"/bot"+tok+"/"+method, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("telegram %s: build request", method)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("telegram %s: %w", method, scrubTransportError(err))
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("telegram %s: read response: %w", method, scrubTransportError(err))
	}
	var decoded apiResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return fmt.Errorf("telegram %s: HTTP %d with a body that is not JSON", method, resp.StatusCode)
	}
	if !decoded.OK {
		return &APIError{Code: decoded.ErrorCode, Description: decoded.Description, RetryAfter: decoded.Parameters.RetryAfter}
	}
	if out != nil && len(decoded.Result) > 0 {
		if err := json.Unmarshal(decoded.Result, out); err != nil {
			return fmt.Errorf("telegram %s: decode result: %w", method, err)
		}
	}
	return nil
}

func scrubTransportError(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return urlErr.Err
	}
	return err
}

// Message is a sendMessage request. Text is HTML (parse_mode HTML); build it
// with Escape for anything that is not markup of our own.
type Message struct {
	Chat             Chat
	Text             string
	ReplyTo          int64
	DisableNotify    bool
	DisableLinkCards bool
}

// SentMessage is the part of Telegram's Message object callers use.
type SentMessage struct {
	MessageID int64 `json:"message_id"`
}

// Send delivers one HTML message.
func Send(ctx context.Context, msg Message) (SentMessage, error) {
	body := map[string]any{
		"chat_id":    msg.Chat.ID,
		"text":       Truncate(msg.Text, MaxMessageRunes),
		"parse_mode": "HTML",
	}
	if msg.Chat.ThreadID > 0 {
		body["message_thread_id"] = msg.Chat.ThreadID
	}
	if msg.ReplyTo > 0 {
		body["reply_parameters"] = map[string]any{"message_id": msg.ReplyTo, "allow_sending_without_reply": true}
	}
	if msg.DisableNotify {
		body["disable_notification"] = true
	}
	if msg.DisableLinkCards {
		body["link_preview_options"] = map[string]any{"is_disabled": true}
	}
	var sent SentMessage
	err := Call(ctx, "sendMessage", body, &sent)
	return sent, err
}

// Escape makes text safe inside an HTML-mode message.
func Escape(s string) string { return html.EscapeString(s) }

// Truncate cuts s to at most limit runes, ending with an ellipsis when cut.
func Truncate(s string, limit int) string {
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit-1]) + "…"
}

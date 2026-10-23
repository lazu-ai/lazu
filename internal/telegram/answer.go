package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
)

// The documentation answerer: Dispatch runs it after every registered
// handler. In a private chat it
// answers every question; in a listed group it answers when mentioned, when
// replied to, or on /ask. It never sees anyone's account: answers come from
// the public reference only (account-aware handlers run before it).

const (
	defaultDailyLimit = 300
	userWindow        = 10 * time.Minute
	userWindowLimit   = 6
	groupWindow       = time.Minute
	groupWindowLimit  = 15
	answerTimeout     = 75 * time.Second
	answerMaxTokens   = 900
	answerMaxRunes    = 3200
	questionMaxRunes  = 2000
)

var answerHTTP = &http.Client{Timeout: answerTimeout}

// AnswerConfigured reports whether the documentation answerer is on.
func AnswerConfigured() bool {
	a := common.AppConfig.Telegram.Answer
	return Configured() && strings.TrimSpace(a.Model) != "" && strings.TrimSpace(a.APIKey) != ""
}

func answersInChat(chatID int64) bool {
	id := strconv.FormatInt(chatID, 10)
	for _, c := range common.AppConfig.Telegram.Answer.Chats {
		if chat, ok := ParseChat(c); ok && chat.ID == id {
			return true
		}
	}
	return false
}

// Command is a leading /command addressed to this bot, with its arguments.
// ok is false when the message is not a command or names another bot.
func Command(text, botUsername string) (name, args string, ok bool) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "/") {
		return "", "", false
	}
	head, rest, _ := strings.Cut(text, " ")
	head = strings.TrimPrefix(head, "/")
	cmd, target, hasTarget := strings.Cut(head, "@")
	if hasTarget && !strings.EqualFold(target, botUsername) {
		return "", "", false
	}
	return strings.ToLower(cmd), strings.TrimSpace(rest), cmd != ""
}

// question decides whether a message asks the bot something and returns
// the question with the bot's mention removed.
func question(msg *IncomingMessage, me botIdentity) (string, bool) {
	text := strings.TrimSpace(msg.Text)
	if name, args, ok := Command(text, me.Username); ok {
		if name == "ask" {
			return args, args != ""
		}
		return "", false
	}
	if msg.Private() {
		return text, true
	}
	mention := "@" + me.Username
	if me.Username != "" && strings.Contains(strings.ToLower(text), strings.ToLower(mention)) {
		re := regexp.MustCompile(`(?i)` + regexp.QuoteMeta(mention))
		q := strings.TrimSpace(re.ReplaceAllString(text, " "))
		return q, q != ""
	}
	if msg.ReplyTo != nil && msg.ReplyTo.From != nil && msg.ReplyTo.From.ID == me.ID {
		return text, true
	}
	return "", false
}

var apiKeyPattern = regexp.MustCompile(`\bsk-[A-Za-z0-9_\-]{16,}`)

func answerHandler(ctx context.Context, msg *IncomingMessage) bool {
	me, err := Me(ctx)
	if err != nil {
		common.SysError("telegram: getMe failed: " + err.Error())
		return false
	}
	if !msg.Private() && !answersInChat(msg.Chat.ID) {
		return false
	}
	if name, _, ok := Command(msg.Text, me.Username); ok && (name == "start" || name == "help") {
		reply(ctx, msg, phrase(msg, phraseWelcome))
		return true
	}
	q, ok := question(msg, me)
	if !ok {
		return false
	}
	if apiKeyPattern.MatchString(q) {
		reply(ctx, msg, phrase(msg, phraseKeyLeaked))
		// Works only where the bot may delete messages; harmless otherwise.
		_ = Call(ctx, "deleteMessage", map[string]any{"chat_id": msg.Chat.ID, "message_id": msg.MessageID}, nil)
		return true
	}
	if !AnswerConfigured() {
		if msg.Private() {
			reply(ctx, msg, phrase(msg, phraseOff))
		}
		return true
	}
	if !msg.Private() && countWithin(ctx, fmt.Sprintf("answer:chat:%d", msg.Chat.ID), groupWindow) > groupWindowLimit {
		return true
	}
	if n := countWithin(ctx, fmt.Sprintf("answer:user:%d", msg.From.ID), userWindow); n > userWindowLimit {
		if n == userWindowLimit+1 {
			reply(ctx, msg, phrase(msg, phraseSlowDown))
		}
		return true
	}
	limit := common.AppConfig.Telegram.Answer.DailyLimit
	if limit <= 0 {
		limit = defaultDailyLimit
	}
	if countWithin(ctx, "answer:day:"+time.Now().UTC().Format("2006-01-02"), 25*time.Hour) > int64(limit) {
		reply(ctx, msg, phrase(msg, phraseDailyLimit))
		return true
	}

	_ = Call(ctx, "sendChatAction", chatActionBody(msg), nil)
	var previous string
	if msg.ReplyTo != nil && msg.ReplyTo.From != nil && msg.ReplyTo.From.ID == me.ID {
		previous = msg.ReplyTo.Text
	}
	answer, err := Answer(ctx, AnswerRequest{
		Question: Truncate(q, questionMaxRunes), Previous: previous, Private: msg.Private(),
	})
	if err != nil {
		common.SysError("telegram: answer failed: " + err.Error())
		reply(ctx, msg, phrase(msg, phraseFailed))
		return true
	}
	reply(ctx, msg, MarkdownToHTML(Truncate(answer, answerMaxRunes)))
	return true
}

func chatActionBody(msg *IncomingMessage) map[string]any {
	body := map[string]any{"chat_id": msg.Chat.ID, "action": "typing"}
	if msg.IsTopicMessage && msg.MessageThreadID > 0 {
		body["message_thread_id"] = msg.MessageThreadID
	}
	return body
}

// reply answers msg in its chat and topic. Telegram refuses HTML it cannot
// parse; the text is then resent with the markup escaped.
func reply(ctx context.Context, msg *IncomingMessage, html string) {
	out := Message{Chat: Chat{ID: strconv.FormatInt(msg.Chat.ID, 10)}, Text: html, ReplyTo: msg.MessageID, DisableLinkCards: true}
	if msg.IsTopicMessage && msg.MessageThreadID > 0 {
		out.Chat.ThreadID = msg.MessageThreadID
	}
	_, err := Send(ctx, out)
	var apiErr *APIError
	if errors.As(err, &apiErr) && strings.Contains(strings.ToLower(apiErr.Description), "parse entities") {
		out.Text = Escape(stripTags(html))
		_, err = Send(ctx, out)
	}
	if err != nil {
		common.SysError("telegram: reply not delivered: " + err.Error())
	}
}

var tagPattern = regexp.MustCompile(`</?[a-zA-Z][^>]*>`)

func stripTags(s string) string {
	s = tagPattern.ReplaceAllString(s, "")
	r := strings.NewReplacer("&lt;", "<", "&gt;", ">", "&quot;", `"`, "&amp;", "&")
	return r.Replace(s)
}

// AnswerRequest is one question for the model.
type AnswerRequest struct {
	Question string
	// Previous is the bot's message the question replies to, for follow-ups.
	Previous string
	Private  bool
	// Account is private context about the asker (a linked account), added
	// only in private chats by the caller.
	Account string
}

// Answer asks the configured model, grounded in Reference.
func Answer(ctx context.Context, req AnswerRequest) (string, error) {
	cfg := common.AppConfig.Telegram.Answer
	messages := []map[string]string{{"role": "system", "content": systemPrompt(ctx, req)}}
	if strings.TrimSpace(req.Previous) != "" {
		messages = append(messages, map[string]string{"role": "assistant", "content": req.Previous})
	}
	messages = append(messages, map[string]string{"role": "user", "content": req.Question})
	payload, _ := json.Marshal(map[string]any{
		"model":      strings.TrimSpace(cfg.Model),
		"messages":   messages,
		"max_tokens": answerMaxTokens,
	})
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, answerBaseURL()+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+strings.TrimSpace(cfg.APIKey))
	resp, err := answerHTTP.Do(httpReq)
	if err != nil {
		return "", scrubTransportError(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var decoded struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Error *struct {
			Message string `json:"message"`
			Lazu    string `json:"lazu"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return "", fmt.Errorf("answer: HTTP %d, body is not JSON", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK || decoded.Error != nil {
		if decoded.Error != nil {
			return "", fmt.Errorf("answer: HTTP %d %s %s", resp.StatusCode, decoded.Error.Lazu, decoded.Error.Message)
		}
		return "", fmt.Errorf("answer: HTTP %d", resp.StatusCode)
	}
	if len(decoded.Choices) == 0 || strings.TrimSpace(decoded.Choices[0].Message.Content) == "" {
		return "", errors.New("answer: empty reply")
	}
	return strings.TrimSpace(decoded.Choices[0].Message.Content), nil
}

// answerBaseURL is telegram.answer.base_url, or this process over loopback.
func answerBaseURL() string {
	if v := strings.TrimRight(strings.TrimSpace(common.AppConfig.Telegram.Answer.BaseURL), "/"); v != "" {
		return v
	}
	port := strings.TrimSpace(os.Getenv("PORT"))
	if port == "" && common.Port != nil {
		port = strconv.Itoa(*common.Port)
	}
	if port == "" {
		port = "3000"
	}
	return "http://127.0.0.1:" + port + "/v1"
}

// supportEmail is the public support address, never the alert address.
func supportEmail() string { return strings.TrimSpace(common.AppConfig.Site.SupportEmail) }

func systemPrompt(ctx context.Context, req AnswerRequest) string {
	brand := strings.TrimSpace(common.SystemName)
	if brand == "" {
		brand = "this gateway"
	}
	where := "a Telegram group"
	if req.Private {
		where = "a private Telegram chat"
	}
	contact := "the operators"
	if email := supportEmail(); email != "" {
		contact = email
	}
	var b strings.Builder
	fmt.Fprintf(&b, `You are the support assistant of %s (%s), an OpenAI-compatible AI gateway, answering in %s.
Answer from the reference below. If it does not cover the question, say you are not sure and suggest writing to %s; never guess prices, limits, model names, dates or features.
`, brand, common.SiteWebOrigin(), where, contact)
	if req.Account == "" {
		fmt.Fprintf(&b, "You cannot see anyone's account, balance, keys or requests. For an account-specific problem, ask them to write to %s with the request ID (req_...) and the LZ error number, or to link their account in a private chat with you.\n", contact)
	} else {
		b.WriteString("This private chat belongs to the linked account described in <account>. Use it only to answer this person's own question; never mention it otherwise.\n")
	}
	b.WriteString(`Never ask for an API key or repeat one. If a message contains one, tell them to revoke it in the console at once.
Reply in the language of the question. Be brief: a few sentences or a short list, at most about 12 lines. Use Markdown only for ` + "`code`" + `, fenced code blocks, **bold** and [links](https://...).

`)
	if req.Account != "" {
		fmt.Fprintf(&b, "<account>\n%s\n</account>\n\n", req.Account)
	}
	b.WriteString("<reference>\n")
	b.WriteString(Reference(ctx, req.Question))
	b.WriteString("</reference>")
	return b.String()
}

var (
	fencePattern  = regexp.MustCompile("(?s)```[a-zA-Z0-9_+-]*\\n?(.*?)```")
	inlineCode    = regexp.MustCompile("`([^`\\n]+)`")
	boldPattern   = regexp.MustCompile(`\*\*([^*\n]+)\*\*`)
	linkPattern   = regexp.MustCompile(`\[([^\]\n]+)\]\((https?://[^)\s]+)\)`)
	headingPrefix = regexp.MustCompile(`(?m)^#{1,6}\s+(.+)$`)
	bulletPrefix  = regexp.MustCompile(`(?m)^(\s*)[-*]\s+`)
)

// MarkdownToHTML turns the small Markdown subset answers use into the HTML
// Telegram accepts. Everything else is escaped.
func MarkdownToHTML(md string) string {
	var out strings.Builder
	last := 0
	for _, loc := range fencePattern.FindAllStringSubmatchIndex(md, -1) {
		out.WriteString(inlineMarkdown(md[last:loc[0]]))
		out.WriteString("<pre>")
		out.WriteString(Escape(strings.TrimRight(md[loc[2]:loc[3]], "\n")))
		out.WriteString("</pre>")
		last = loc[1]
	}
	out.WriteString(inlineMarkdown(md[last:]))
	return strings.TrimSpace(out.String())
}

func inlineMarkdown(s string) string {
	// Code spans first, so their content is not read as other markup.
	var codes []string
	s = inlineCode.ReplaceAllStringFunc(s, func(m string) string {
		codes = append(codes, inlineCode.FindStringSubmatch(m)[1])
		return fmt.Sprintf("\x00%d\x00", len(codes)-1)
	})
	s = Escape(s)
	s = headingPrefix.ReplaceAllString(s, "<b>$1</b>")
	s = bulletPrefix.ReplaceAllString(s, "$1• ")
	s = boldPattern.ReplaceAllString(s, "<b>$1</b>")
	s = linkPattern.ReplaceAllString(s, `<a href="$2">$1</a>`)
	for i, code := range codes {
		s = strings.Replace(s, fmt.Sprintf("\x00%d\x00", i), "<code>"+Escape(code)+"</code>", 1)
	}
	return s
}

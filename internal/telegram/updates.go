package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
)

// The bot receives messages either by webhook (Telegram posts each update
// to /api/telegram/webhook) or by long polling in one process. Both hand the
// same Update to the same dispatcher. Updates are answered asynchronously so
// a webhook call returns at once; Telegram retries a webhook that does not
// answer in time, and every update id is handled only once.

// User is a Telegram account.
type User struct {
	ID           int64  `json:"id"`
	IsBot        bool   `json:"is_bot"`
	FirstName    string `json:"first_name"`
	Username     string `json:"username"`
	LanguageCode string `json:"language_code"`
}

// ChatInfo is the chat a message was posted in.
type ChatInfo struct {
	ID    int64  `json:"id"`
	Type  string `json:"type"` // private, group, supergroup, channel
	Title string `json:"title"`
}

// IncomingMessage is the part of Telegram's Message the bot reads.
type IncomingMessage struct {
	MessageID       int64            `json:"message_id"`
	MessageThreadID int64            `json:"message_thread_id"`
	IsTopicMessage  bool             `json:"is_topic_message"`
	From            *User            `json:"from"`
	Chat            ChatInfo         `json:"chat"`
	Date            int64            `json:"date"`
	Text            string           `json:"text"`
	ReplyTo         *IncomingMessage `json:"reply_to_message"`
}

// Private reports whether the message is in a one-to-one chat with the bot.
func (m *IncomingMessage) Private() bool { return m.Chat.Type == "private" }

// Update is one Telegram update. Only messages are requested.
type Update struct {
	UpdateID int64            `json:"update_id"`
	Message  *IncomingMessage `json:"message"`
}

// allowedUpdates is what setWebhook and getUpdates ask Telegram for.
var allowedUpdates = []string{"message"}

// Mode is the configured way of receiving updates.
func Mode() string {
	switch mode := strings.ToLower(strings.TrimSpace(common.AppConfig.Telegram.Updates)); mode {
	case "webhook", "polling":
		return mode
	default:
		return ""
	}
}

// Receiving reports whether this deployment takes updates at all.
func Receiving() bool { return Configured() && Mode() != "" }

// --- the bot's own identity ---

type botIdentity struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
}

var identity = struct {
	sync.Mutex
	bot       botIdentity
	triedAt   time.Time
	forTokens string
}{}

// Me returns the bot's id and username (getMe), cached per token. A failed
// lookup is retried at most once a minute.
func Me(ctx context.Context) (botIdentity, error) {
	identity.Lock()
	defer identity.Unlock()
	tok := token()
	if identity.forTokens == tok && identity.bot.ID != 0 {
		return identity.bot, nil
	}
	if identity.forTokens == tok && time.Since(identity.triedAt) < time.Minute {
		return botIdentity{}, errors.New("telegram: getMe failed recently")
	}
	identity.forTokens, identity.triedAt = tok, time.Now()
	var me botIdentity
	if err := Call(ctx, "getMe", map[string]any{}, &me); err != nil {
		return botIdentity{}, err
	}
	identity.bot = me
	return me, nil
}

// cachedUsername is the bot's username if getMe already answered.
func cachedUsername() string {
	identity.Lock()
	defer identity.Unlock()
	if identity.forTokens == token() {
		return identity.bot.Username
	}
	return ""
}

// --- dispatch ---

// Handler answers one message. It returns true when it handled the message,
// which stops the next handlers from seeing it.
type Handler func(ctx context.Context, msg *IncomingMessage) bool

var handlers []Handler

// RegisterHandler adds a message handler; register from init(). Handlers run
// in registration order, and the documentation answerer always runs after
// all of them.
func RegisterHandler(h Handler) { handlers = append(handlers, h) }

const (
	workerCount    = 4
	queueSize      = 64
	updateDeadline = 2 * time.Minute
)

var queue = struct {
	once sync.Once
	ch   chan Update
}{}

// Accept takes one raw update from the webhook or the poller and queues it.
// It returns an error only for a body that is not an update; a full queue
// drops the update with a log line, because Telegram must get its answer.
func Accept(raw []byte) error {
	var update Update
	if err := json.Unmarshal(raw, &update); err != nil || update.UpdateID == 0 {
		return errors.New("telegram: not an update")
	}
	enqueue(update)
	return nil
}

func enqueue(update Update) {
	queue.once.Do(func() {
		queue.ch = make(chan Update, queueSize)
		for i := 0; i < workerCount; i++ {
			common.SafeGo(fmt.Sprintf("telegram-update-%d", i), func() {
				for u := range queue.ch {
					handleUpdate(u)
				}
			})
		}
	})
	select {
	case queue.ch <- update:
	default:
		common.SysError(fmt.Sprintf("telegram: update queue full, dropped update %d", update.UpdateID))
	}
}

// handleUpdate runs the handlers for one update once, whichever replica or
// retry delivered it.
func handleUpdate(update Update) {
	ctx, cancel := context.WithTimeout(context.Background(), updateDeadline)
	defer cancel()
	if !claim(ctx, fmt.Sprintf("update:%d", update.UpdateID), 24*time.Hour) {
		return
	}
	Dispatch(ctx, update)
}

// Dispatch hands a message update to the handlers. Bots, channels and
// messages without text are ignored.
func Dispatch(ctx context.Context, update Update) {
	msg := update.Message
	if msg == nil || msg.From == nil || msg.From.IsBot || strings.TrimSpace(msg.Text) == "" {
		return
	}
	if msg.Chat.Type == "channel" {
		return
	}
	for _, h := range handlers {
		if h(ctx, msg) {
			return
		}
	}
	answerHandler(ctx, msg)
}

// --- receiving ---

func init() { common.RegisterBackgroundLoop("telegram-updates", runUpdates) }

func runUpdates() {
	if !Receiving() {
		return
	}
	switch Mode() {
	case "webhook":
		registerWebhookWithRetry()
	case "polling":
		pollForever()
	}
}

// WebhookURL is where Telegram is told to post updates.
func WebhookURL() string {
	if v := strings.TrimSpace(common.AppConfig.Telegram.WebhookURL); v != "" {
		return v
	}
	origin := common.SiteAPIOrigin()
	if origin == "" {
		origin = common.SiteWebOrigin()
	}
	return origin + "/api/telegram/webhook"
}

// WebhookSecret is the shared secret Telegram echoes back.
func WebhookSecret() string { return strings.TrimSpace(common.AppConfig.Telegram.WebhookSecret) }

func registerWebhookWithRetry() {
	target := WebhookURL()
	if parsed, err := url.Parse(target); err != nil || parsed.Scheme != "https" {
		common.SysError("telegram: webhook mode needs an https URL, got " + target + "; set telegram.webhook_url or site.api_origin")
		return
	}
	if WebhookSecret() == "" {
		common.SysError("telegram: webhook mode needs telegram.webhook_secret (TELEGRAM_WEBHOOK_SECRET); not registering")
		return
	}
	delay := 10 * time.Second
	for attempt := 0; attempt < 8; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		err := Call(ctx, "setWebhook", map[string]any{
			"url":             target,
			"secret_token":    WebhookSecret(),
			"allowed_updates": allowedUpdates,
			"max_connections": 10,
		}, nil)
		cancel()
		if err == nil {
			common.SysLog("telegram: webhook registered at " + target)
			return
		}
		common.SysError("telegram: setWebhook failed: " + err.Error())
		time.Sleep(delay)
		delay *= 2
	}
}

const pollLease = "poll:lease"

// pollForever long-polls getUpdates. Only the process holding the lease
// polls, so two replicas never fight over the same updates.
func pollForever() {
	var offset int64
	cleared := false
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
		if !holdPollLease(ctx) {
			cancel()
			time.Sleep(30 * time.Second)
			continue
		}
		if !cleared {
			// A webhook left from an earlier deployment makes getUpdates fail.
			if err := Call(ctx, "deleteWebhook", map[string]any{"drop_pending_updates": false}, nil); err != nil {
				common.SysError("telegram: deleteWebhook failed: " + err.Error())
			} else {
				cleared = true
			}
		}
		var updates []json.RawMessage
		err := Call(ctx, "getUpdates", map[string]any{
			"offset":          offset,
			"timeout":         25,
			"allowed_updates": allowedUpdates,
		}, &updates)
		cancel()
		if err != nil {
			common.SysError("telegram: getUpdates failed: " + err.Error())
			time.Sleep(5 * time.Second)
			continue
		}
		for _, raw := range updates {
			var head struct {
				UpdateID int64 `json:"update_id"`
			}
			if json.Unmarshal(raw, &head) == nil && head.UpdateID >= offset {
				offset = head.UpdateID + 1
			}
			_ = Accept(raw)
		}
	}
}

var pollOwner = fmt.Sprintf("%d", time.Now().UnixNano())

// holdPollLease takes or renews the poller lease for this process.
func holdPollLease(ctx context.Context) bool {
	if !redisReady() {
		return true
	}
	key := keyPrefix + pollLease
	ok, err := common.RDB.SetNX(ctx, key, pollOwner, time.Minute).Result()
	if err != nil {
		return false
	}
	if ok {
		return true
	}
	owner, err := common.RDB.Get(ctx, key).Result()
	if err != nil || owner != pollOwner {
		return false
	}
	return common.RDB.Expire(ctx, key, time.Minute).Err() == nil
}

// resetIdentity forgets the cached getMe answer (tests).
func resetIdentity() {
	identity.Lock()
	identity.bot, identity.triedAt, identity.forTokens = botIdentity{}, time.Time{}, ""
	identity.Unlock()
}

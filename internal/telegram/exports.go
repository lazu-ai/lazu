package telegram

import (
	"context"
	"time"
)

// Pieces other layers use to build handlers (account linking lives with
// the models, outside this package).

// AccountContext, when set, describes the linked account of a Telegram
// user for answers in a private chat. It returns "" for a user who has not
// linked one. Group chats never call it.
var AccountContext func(ctx context.Context, telegramUserID int64) string

// LinkedAccount, when set, reports whether a Telegram user has linked an
// account. Unlinked people get a smaller daily allowance in private chats.
var LinkedAccount func(ctx context.Context, telegramUserID int64) bool

// Reply answers msg in its chat and topic with HTML-mode text.
func Reply(ctx context.Context, msg *IncomingMessage, html string) { reply(ctx, msg, html) }

// Lang is the asker's language for fixed replies: en, zh or ja.
func Lang(msg *IncomingMessage) string { return lang(msg) }

// PutState stores a short-lived value shared by every process (Redis, or
// memory without it).
func PutState(ctx context.Context, key, value string, ttl time.Duration) error {
	return putValue(ctx, "state:"+key, value, ttl)
}

// TakeState returns a value stored with PutState and deletes it, so it can
// be used once.
func TakeState(ctx context.Context, key string) string {
	v := getValue(ctx, "state:"+key)
	if v != "" {
		_ = putValue(ctx, "state:"+key, "", 0)
	}
	return v
}

// SendToUser sends an HTML message to a user's private chat with the bot.
// It works only after the user has started a chat with the bot.
func SendToUser(ctx context.Context, telegramUserID int64, html string) error {
	_, err := Send(ctx, Message{Chat: Chat{ID: formatInt(telegramUserID)}, Text: html, DisableLinkCards: true})
	return err
}

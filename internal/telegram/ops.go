package telegram

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
)

// Operational alerts are copied to telegram.ops_chat. Mail stays the record;
// the chat is the nudge, so a failure here is logged and never returned.

const (
	// OpsAlertRepeat is how long the same subject stays quiet after it was
	// pushed once. Groups accept about 20 messages a minute, and a flapping
	// check must not use them up.
	OpsAlertRepeat = time.Hour
	opsMuteKey     = "ops:muted_until"
	opsSendTimeout = 5 * time.Second
)

func init() { common.RegisterOpsAlertSink(PushOpsAlert) }

// OpsChat is the configured alert chat, if any.
func OpsChat() (Chat, bool) { return ParseChat(common.AppConfig.Telegram.OpsChat) }

// OpsConfigured reports whether alerts reach Telegram at all.
func OpsConfigured() bool {
	_, ok := OpsChat()
	return Configured() && ok
}

// PushOpsAlert sends an alert to the ops chat unless Telegram is not set
// up, alerts are muted, or the same subject was pushed within the last hour.
func PushOpsAlert(alert common.OpsAlert) {
	if !OpsConfigured() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), opsSendTimeout)
	defer cancel()
	if OpsMutedUntil(ctx).After(time.Now()) {
		return
	}
	sum := sha256.Sum256([]byte(strings.TrimSpace(alert.Subject)))
	key := "ops:sent:" + hex.EncodeToString(sum[:12])
	if !claim(ctx, key, OpsAlertRepeat) {
		return
	}
	if err := sendOpsText(ctx, FormatOpsAlert(alert)); err != nil {
		// Let the next occurrence try again instead of staying silent for
		// an hour behind a send that never arrived.
		release(ctx, key)
		common.SysError("telegram: ops alert not delivered: " + err.Error())
	}
}

// SendOpsTest sends a test message to the ops chat, bypassing mute and
// dedupe. It returns the error so the console can show it.
func SendOpsTest(ctx context.Context, text string) error {
	if !Configured() {
		return ErrNotConfigured
	}
	if _, ok := OpsChat(); !ok {
		return ErrNoOpsChat
	}
	return sendOpsText(ctx, text)
}

// ErrNoOpsChat is returned when telegram.ops_chat is empty or malformed.
var ErrNoOpsChat = errors.New("telegram: telegram.ops_chat is empty or not a chat id")

func sendOpsText(ctx context.Context, text string) error {
	chat, _ := OpsChat()
	_, err := Send(ctx, Message{Chat: chat, Text: text, DisableLinkCards: true})
	return err
}

// FormatOpsAlert renders an alert as an HTML-mode message: the subject in
// bold, then the plain-text body.
func FormatOpsAlert(alert common.OpsAlert) string {
	var b strings.Builder
	b.WriteString("<b>")
	b.WriteString(Escape(strings.TrimSpace(alert.Subject)))
	b.WriteString("</b>")
	if text := strings.TrimSpace(alert.Text); text != "" {
		b.WriteString("\n\n")
		// Leave room for the subject and markup inside Telegram's limit.
		b.WriteString(Escape(Truncate(text, MaxMessageRunes-600)))
	}
	return b.String()
}

// OpsMutedUntil is when the current mute ends, or the zero time.
func OpsMutedUntil(ctx context.Context) time.Time {
	raw := getValue(ctx, opsMuteKey)
	if raw == "" {
		return time.Time{}
	}
	unix, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.Unix(unix, 0)
}

// MuteOps silences ops alerts in Telegram for d; d <= 0 lifts the mute.
// Mail is never muted.
func MuteOps(ctx context.Context, d time.Duration) (time.Time, error) {
	if d <= 0 {
		return time.Time{}, putValue(ctx, opsMuteKey, "", 0)
	}
	until := time.Now().Add(d).Truncate(time.Second)
	return until, putValue(ctx, opsMuteKey, strconv.FormatInt(until.Unix(), 10), d)
}

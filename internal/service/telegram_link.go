package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/telegram"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Linking a Telegram account to an account here. The console issues a
// one-time deep link (t.me/<bot>?start=link_<token>); opening it starts a
// private chat in which the bot receives /start link_<token> and stores the
// Telegram user id as a user_identities row with provider "telegram". No
// sign-in path reads that provider, so a link never becomes a way to log in.
// A linked account can receive its balance reminders in Telegram, and the
// bot may use the account's own facts when answering in that private chat.

const (
	TelegramIdentityProvider = model.IdentityProviderTelegram
	telegramLinkTTL          = 10 * time.Minute
	telegramLinkPrefix       = "link_"
	telegramRecentErrorDays  = 7
	telegramRecentErrorCount = 5
)

var (
	// ErrTelegramUnavailable: the deployment's bot cannot receive /start.
	ErrTelegramUnavailable = errors.New("telegram linking is not available on this deployment")
	// ErrTelegramLinkedElsewhere: the Telegram account belongs to another user.
	ErrTelegramLinkedElsewhere = errors.New("this Telegram account is linked to another account")
	// ErrTelegramNotLinked: the account has no Telegram link.
	ErrTelegramNotLinked = errors.New("no Telegram account is linked")
)

func init() {
	telegram.RegisterHandler(handleTelegramStart)
	telegram.AccountContext = telegramAccountContext
	telegram.LinkedAccount = func(_ context.Context, telegramUserID int64) bool {
		user, err := telegramUserFor(telegramUserID)
		return err == nil && user != nil
	}
}

// TelegramLinkAvailable reports whether accounts can be linked here: the bot
// must receive updates to see /start.
func TelegramLinkAvailable() bool { return telegram.Receiving() }

// TelegramLinkStatus is what the console shows about a user's link.
type TelegramLinkStatus struct {
	Available bool  `json:"available"`
	Linked    bool  `json:"linked"`
	LinkedAt  int64 `json:"linked_at,omitempty"`
}

// GetTelegramLinkStatus reads a user's link.
func GetTelegramLinkStatus(userID int) (TelegramLinkStatus, error) {
	status := TelegramLinkStatus{Available: TelegramLinkAvailable()}
	identity, err := telegramIdentityOf(model.CoreDB(), userID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return status, nil
	}
	if err != nil {
		return status, err
	}
	status.Linked, status.LinkedAt = true, identity.UpdatedAt.Unix()
	return status, nil
}

// TelegramUserID is the linked Telegram user id of an account.
func TelegramUserID(userID int) (int64, bool, error) {
	identity, err := telegramIdentityOf(model.CoreDB(), userID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	id, err := strconv.ParseInt(identity.Subject, 10, 64)
	if err != nil {
		return 0, false, fmt.Errorf("stored telegram id %q: %w", identity.Subject, err)
	}
	return id, true, nil
}

func telegramIdentityOf(db *gorm.DB, userID int) (model.UserIdentity, error) {
	var identity model.UserIdentity
	err := db.Where("user_id = ? AND provider = ?", userID, TelegramIdentityProvider).First(&identity).Error
	return identity, err
}

// IssueTelegramLink returns a deep link that links the opener's Telegram
// account to userID. It works once, within ten minutes.
func IssueTelegramLink(ctx context.Context, userID int) (string, time.Duration, error) {
	if !TelegramLinkAvailable() {
		return "", 0, ErrTelegramUnavailable
	}
	me, err := telegram.Me(ctx)
	if err != nil || me.Username == "" {
		return "", 0, fmt.Errorf("%w: the bot's username is unknown", ErrTelegramUnavailable)
	}
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", 0, err
	}
	token := base64.RawURLEncoding.EncodeToString(raw) // 32 characters of A-Z a-z 0-9 _ -
	if err := telegram.PutState(ctx, "link:"+token, strconv.Itoa(userID), telegramLinkTTL); err != nil {
		return "", 0, err
	}
	return "https://t.me/" + me.Username + "?start=" + telegramLinkPrefix + token, telegramLinkTTL, nil
}

// LinkTelegram binds a Telegram user id to an account, replacing the
// account's earlier link. A Telegram account linked to someone else is
// refused rather than moved.
func LinkTelegram(userID int, telegramUserID int64) error {
	subject := strconv.FormatInt(telegramUserID, 10)
	return model.CoreDB().Transaction(func(tx *gorm.DB) error {
		var holder model.UserIdentity
		err := tx.Where("provider = ? AND subject = ?", TelegramIdentityProvider, subject).First(&holder).Error
		if err == nil && holder.UserID != userID {
			return ErrTelegramLinkedElsewhere
		}
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err == nil {
			return tx.Model(&holder).Update("updated_at", time.Now()).Error
		}
		existing, err := telegramIdentityOf(tx, userID)
		if err == nil {
			return tx.Model(&existing).Updates(map[string]any{"subject": subject, "updated_at": time.Now()}).Error
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		return tx.Create(&model.UserIdentity{UserID: userID, Provider: TelegramIdentityProvider, Subject: subject}).Error
	})
}

// UnlinkTelegram removes an account's link. Balance reminders that went to
// Telegram go back to mail.
func UnlinkTelegram(userID int) error {
	err := model.CoreDB().Transaction(func(tx *gorm.DB) error {
		result := tx.Where("user_id = ? AND provider = ?", userID, TelegramIdentityProvider).Delete(&model.UserIdentity{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrTelegramNotLinked
		}
		return resetTelegramNotifyTx(tx, userID)
	})
	if err == nil {
		_ = model.InvalidateUserCache(userID)
	}
	return err
}

// resetTelegramNotifyTx moves notify_type from telegram back to email,
// keeping every other stored setting.
func resetTelegramNotifyTx(tx *gorm.DB, userID int) error {
	var current model.User
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id", "setting").First(&current, userID).Error; err != nil {
		return err
	}
	settings := map[string]json.RawMessage{}
	if strings.TrimSpace(current.Setting) != "" {
		if err := json.Unmarshal([]byte(current.Setting), &settings); err != nil {
			return fmt.Errorf("invalid stored user setting: %w", err)
		}
	}
	var notifyType string
	_ = json.Unmarshal(settings["notify_type"], &notifyType)
	if notifyType != dto.NotifyTypeTelegram {
		return nil
	}
	settings["notify_type"] = json.RawMessage(strconv.Quote(dto.NotifyTypeEmail))
	encoded, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	return tx.Model(&model.User{}).Where("id = ?", userID).Update("setting", string(encoded)).Error
}

// telegramUserFor finds the account a Telegram user linked, or nil.
func telegramUserFor(telegramUserID int64) (*model.User, error) {
	var identity model.UserIdentity
	err := model.CoreDB().Where("provider = ? AND subject = ?", TelegramIdentityProvider, strconv.FormatInt(telegramUserID, 10)).First(&identity).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return model.GetUserById(identity.UserID, false)
}

var telegramLinkPhrases = map[string]map[string]string{
	"linked": {
		"en": "Linked to the account %s. Balance reminders can now go to Telegram (Settings › Notifications), and in this chat I can take your account into account.",
		"zh": "已绑定到账户 %s。现在可以在 设置 › 通知 里把余额提醒改为 Telegram；在这个私聊里提问时我会参考你的账户。",
		"ja": "アカウント %s と連携しました。残高のお知らせを Telegram で受け取れます（設定 › 通知）。このチャットではアカウントの状況を踏まえて回答します。",
	},
	"expired": {
		"en": "This link has expired or was already used. Create a new one in the console.",
		"zh": "这个绑定链接已失效或已用过，请回到控制台重新生成。",
		"ja": "このリンクは期限切れか使用済みです。コンソールで新しいリンクを作成してください。",
	},
	"elsewhere": {
		"en": "This Telegram account is already linked to another account. Unlink it there first.",
		"zh": "这个 Telegram 账号已绑定另一个账户，请先在那个账户里解绑。",
		"ja": "この Telegram アカウントは別のアカウントと連携済みです。先にそちらで連携を解除してください。",
	},
	"failed": {
		"en": "Linking did not work just now. Please try again in a moment.",
		"zh": "绑定没有成功，请稍后再试。",
		"ja": "連携できませんでした。少し時間をおいてもう一度お試しください。",
	},
	"groupOnly": {
		"en": "Open this link in a private chat with me to link your account.",
		"zh": "请在和我的私聊里打开绑定链接。",
		"ja": "連携リンクは私とのプライベートチャットで開いてください。",
	},
}

func telegramLinkPhrase(msg *telegram.IncomingMessage, key string, args ...any) string {
	text := telegramLinkPhrases[key][telegram.Lang(msg)]
	if len(args) > 0 {
		text = fmt.Sprintf(text, args...)
	}
	return telegram.Escape(text)
}

// handleTelegramStart completes a link from /start link_<token>.
func handleTelegramStart(ctx context.Context, msg *telegram.IncomingMessage) bool {
	me, err := telegram.Me(ctx)
	if err != nil {
		return false
	}
	name, args, ok := telegram.Command(msg.Text, me.Username)
	if !ok || name != "start" || !strings.HasPrefix(args, telegramLinkPrefix) {
		return false
	}
	if !msg.Private() {
		telegram.Reply(ctx, msg, telegramLinkPhrase(msg, "groupOnly"))
		return true
	}
	token := strings.TrimPrefix(args, telegramLinkPrefix)
	userID, _ := strconv.Atoi(telegram.TakeState(ctx, "link:"+token))
	if userID <= 0 {
		telegram.Reply(ctx, msg, telegramLinkPhrase(msg, "expired"))
		return true
	}
	err = LinkTelegram(userID, msg.From.ID)
	switch {
	case errors.Is(err, ErrTelegramLinkedElsewhere):
		telegram.Reply(ctx, msg, telegramLinkPhrase(msg, "elsewhere"))
	case err != nil:
		common.SysError(fmt.Sprintf("telegram: link for user %d failed: %v", userID, err))
		telegram.Reply(ctx, msg, telegramLinkPhrase(msg, "failed"))
	default:
		name := ""
		if user, err := model.GetUserById(userID, false); err == nil {
			name = user.Username
		}
		telegram.Reply(ctx, msg, telegramLinkPhrase(msg, "linked", name))
	}
	return true
}

// telegramAccountContext is what the answer bot may know about the asker in
// a private chat: the account's name and balance and its latest failed
// requests, each with its request id.
func telegramAccountContext(ctx context.Context, telegramUserID int64) string {
	user, err := telegramUserFor(telegramUserID)
	if err != nil || user == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Username: %s\nBalance: %s\n", user.Username, common.MailUSD(user.Quota.Dollars()))
	if model.TelemetryDB() == nil {
		return b.String()
	}
	since := time.Now().Add(-telegramRecentErrorDays * 24 * time.Hour).Unix()
	logs, _, _, err := model.GetAllLogs(model.LogTypeError, since, 0, "", "", user.Id, "", 0, telegramRecentErrorCount, 0, "")
	if err != nil {
		// Not knowing is not the same as none.
		b.WriteString("Recent failed requests could not be read just now.\n")
		return b.String()
	}
	if len(logs) == 0 {
		fmt.Fprintf(&b, "Failed requests in the last %d days: none recorded.\n", telegramRecentErrorDays)
		return b.String()
	}
	fmt.Fprintf(&b, "Latest failed requests (last %d days, newest first):\n", telegramRecentErrorDays)
	for _, l := range logs {
		fmt.Fprintf(&b, "- %s UTC · %s · %s · %s\n",
			time.Unix(l.CreatedAt, 0).UTC().Format("2006-01-02 15:04"), l.ModelName, l.RequestID, telegram.Truncate(strings.TrimSpace(l.Content), 240))
	}
	return b.String()
}

// sendTelegramNotify delivers a user notification to the linked Telegram
// account as plain text.
func sendTelegramNotify(userID int, data dto.Notify) error {
	if !telegram.Configured() {
		return errors.New("telegram is not configured on this deployment")
	}
	telegramUserID, linked, err := TelegramUserID(userID)
	if err != nil {
		return err
	}
	if !linked {
		return ErrTelegramNotLinked
	}
	content := data.Content
	for _, value := range data.Values {
		content = strings.Replace(content, dto.ContentValueParam, fmt.Sprintf("%v", value), 1)
	}
	text := "<b>" + telegram.Escape(data.Title) + "</b>\n" + telegram.Escape(content)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return telegram.SendToUser(ctx, telegramUserID, text)
}

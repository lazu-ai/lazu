package service

import (
	"crypto/hmac"
	"encoding/base64"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
)

// Mail a reader can turn off from the mail itself.
const (
	MailOptOutWeekly  = "weekly"
	MailOptOutProduct = "product"
)

var mailOptOutSettingKey = map[string]string{
	MailOptOutWeekly:  "weekly_digest_off",
	MailOptOutProduct: "product_updates_off",
}

// MailOptOutToken signs "user|kind" so the link works without signing in and
// cannot be edited to switch off someone else's mail.
func MailOptOutToken(userID int, kind string) string {
	payload := base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(userID) + "|" + kind))
	return payload + "." + mailOptOutSignature(payload)
}

func mailOptOutSignature(payload string) string {
	return common.GenerateHMAC("lazu/mail-optout/v1|" + payload)[:32]
}

// ParseMailOptOutToken returns the user and mail kind a link names.
func ParseMailOptOutToken(token string) (int, string, bool) {
	payload, signature, ok := strings.Cut(strings.TrimSpace(token), ".")
	if !ok || payload == "" || !hmac.Equal([]byte(signature), []byte(mailOptOutSignature(payload))) {
		return 0, "", false
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return 0, "", false
	}
	idText, kind, ok := strings.Cut(string(raw), "|")
	userID, err := strconv.Atoi(idText)
	if !ok || err != nil || userID <= 0 || mailOptOutSettingKey[kind] == "" {
		return 0, "", false
	}
	return userID, kind, true
}

// MailUnsubscribeURL is the link printed in the mail. Opening it shows a
// confirmation; only the button (a POST) switches the mail off, so a mail
// scanner that follows every link does not unsubscribe anyone.
func MailUnsubscribeURL(userID int, kind string) string {
	return common.MailSiteURL("/api/mail/unsubscribe?t=" + url.QueryEscape(MailOptOutToken(userID, kind)))
}

// MailUnsubscribeHeaders are the RFC 8058 one-click headers: the mail
// client's own "Unsubscribe" button POSTs to the same address.
func MailUnsubscribeHeaders(userID int, kind string) map[string]string {
	return map[string]string{
		"List-Unsubscribe":      "<" + MailUnsubscribeURL(userID, kind) + ">",
		"List-Unsubscribe-Post": "List-Unsubscribe=One-Click",
	}
}

// ApplyMailOptOut switches one kind of mail off (or back on) for a user.
func ApplyMailOptOut(userID int, kind string, off bool) error {
	key := mailOptOutSettingKey[kind]
	if key == "" {
		return fmt.Errorf("unknown mail kind %q", kind)
	}
	return model.SetUserSettingValues(userID, map[string]any{key: off})
}

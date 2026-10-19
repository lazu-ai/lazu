package model

import (
	"bytes"
	"compress/gzip"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Delivery statuses. generating → sent → delivered | bounced | complained;
// failed when the provider refused it; skipped for a claimed period that was
// deliberately not sent (the weekly digest on a monthly day).
const (
	MailStatusGenerating = "generating"
	MailStatusSent       = "sent"
	MailStatusDelivered  = "delivered"
	MailStatusDelayed    = "delayed"
	MailStatusBounced    = "bounced"
	MailStatusComplained = "complained"
	MailStatusFailed     = "failed"
	MailStatusSkipped    = "skipped"
	// MailStatusDraft is a digest built for review only; it never goes to
	// its reader.
	MailStatusDraft = "draft"
)

// How long the archive keeps a mail's body and the row itself
// (brand/foundations/usage-digest-emails.md § 3).
const (
	MailBodyRetention = 180 * 24 * time.Hour
	MailRowRetention  = 365 * 24 * time.Hour
)

// MailDelivery is one mail Lazu sent: who, what kind, what happened to it and,
// unless it carried a credential, exactly what the reader got.
type MailDelivery struct {
	ID         uint64  `json:"id" gorm:"primaryKey"`
	Kind       string  `json:"kind" gorm:"size:32;index;not null"`
	UserID     int     `json:"user_id" gorm:"index;not null;default:0"`
	Receiver   string  `json:"receiver" gorm:"size:255;index;not null;default:''"`
	Subject    string  `json:"subject" gorm:"size:512;not null;default:''"`
	RefKey     *string `json:"ref_key,omitempty" gorm:"size:191;uniqueIndex"`
	Period     string  `json:"period" gorm:"size:32;index;not null;default:''"`
	Template   string  `json:"template" gorm:"size:64;not null;default:''"`
	Lang       string  `json:"lang" gorm:"size:8;not null;default:''"`
	Timezone   string  `json:"timezone" gorm:"size:64;not null;default:''"`
	Status     string  `json:"status" gorm:"size:24;index;not null"`
	ProviderID string  `json:"provider_id" gorm:"size:64;index;not null;default:''"`
	LastEvent  string  `json:"last_event" gorm:"size:32;not null;default:''"`
	Error      string  `json:"error" gorm:"type:text"`
	Sensitive  bool    `json:"sensitive" gorm:"not null;default:false"`
	// Body and Snapshot are gzip + AES-GCM sealed; empty once purged.
	Body        string `json:"-" gorm:"type:text"`
	Snapshot    string `json:"-" gorm:"type:text"`
	HasBody     bool   `json:"has_body" gorm:"not null;default:false"`
	CreatedAt   int64  `json:"created_at" gorm:"index;not null"`
	GeneratedAt int64  `json:"generated_at"`
	SentAt      int64  `json:"sent_at"`
	EventAt     int64  `json:"event_at"`
	CheckedAt   int64  `json:"checked_at" gorm:"index"`
	Checks      int    `json:"-" gorm:"not null;default:0"`
}

func mailArchiveCipher() (cipher.AEAD, error) {
	if common.CryptoSecret == "" {
		return nil, errors.New("mail archive encryption secret is missing")
	}
	key := sha256.Sum256([]byte("lazu/mail-archive/v1/" + common.CryptoSecret))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// SealMailArchive compresses and encrypts a body or snapshot for storage.
func SealMailArchive(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	var compressed bytes.Buffer
	zw := gzip.NewWriter(&compressed)
	if _, err := zw.Write([]byte(plain)); err != nil {
		return "", err
	}
	if err := zw.Close(); err != nil {
		return "", err
	}
	aead, err := mailArchiveCipher()
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := aead.Seal(nonce, nonce, compressed.Bytes(), []byte("mail-archive"))
	return base64.RawStdEncoding.EncodeToString(sealed), nil
}

// OpenMailArchive reverses SealMailArchive.
func OpenMailArchive(sealed string) (string, error) {
	if sealed == "" {
		return "", nil
	}
	raw, err := base64.RawStdEncoding.DecodeString(sealed)
	if err != nil {
		return "", err
	}
	aead, err := mailArchiveCipher()
	if err != nil {
		return "", err
	}
	if len(raw) < aead.NonceSize() {
		return "", errors.New("sealed mail archive is too short")
	}
	compressed, err := aead.Open(nil, raw[:aead.NonceSize()], raw[aead.NonceSize():], []byte("mail-archive"))
	if err != nil {
		return "", err
	}
	zr, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return "", err
	}
	defer zr.Close()
	plain, err := io.ReadAll(io.LimitReader(zr, 16<<20))
	return string(plain), err
}

// HTML is the archived body, or "" when it was never kept or was purged.
func (d MailDelivery) HTML() (string, error) { return OpenMailArchive(d.Body) }

// SnapshotJSON is the archived data snapshot of a usage digest.
func (d MailDelivery) SnapshotJSON() (string, error) { return OpenMailArchive(d.Snapshot) }

func init() {
	common.MailRecorder = RecordMailDelivery
}

// RecordMailDelivery archives one send attempt. It never fails the send.
func RecordMailDelivery(rec common.MailRecord) {
	if TelemetryDB() == nil {
		return
	}
	if err := recordMailDelivery(rec, time.Now()); err != nil {
		common.SysError("record mail delivery: " + err.Error())
	}
}

func recordMailDelivery(rec common.MailRecord, now time.Time) error {
	status, errText := MailStatusSent, ""
	if rec.Err != nil {
		status = MailStatusFailed
		// Provider errors can echo the recipient; keep them short.
		errText = truncateMailError(rec.Err.Error())
	}
	meta := rec.Meta
	if meta.DeliveryID != 0 {
		updates := map[string]any{"status": status, "provider_id": rec.ProviderID, "error": errText, "sent_at": now.Unix(), "subject": truncateString(rec.Subject, 512)}
		return TelemetryDB().Model(&MailDelivery{}).Where("id = ?", meta.DeliveryID).Updates(updates).Error
	}
	userID := meta.UserID
	receiver := strings.TrimSpace(rec.Receiver)
	if userID == 0 && receiver != "" && !strings.Contains(receiver, ";") {
		var user User
		if err := CoreDB().Select("id").Where("email = ?", receiver).Limit(1).Find(&user).Error; err == nil {
			userID = user.Id
		}
	}
	row := MailDelivery{
		Kind: meta.Kind, UserID: userID, Receiver: truncateString(receiver, 255), Subject: truncateString(rec.Subject, 512),
		Period: meta.Period, Template: meta.Template, Lang: meta.Lang, Status: status, ProviderID: rec.ProviderID,
		Error: errText, Sensitive: meta.Sensitive, CreatedAt: now.Unix(), GeneratedAt: now.Unix(), SentAt: now.Unix(),
	}
	if !meta.Sensitive {
		sealed, err := SealMailArchive(rec.HTML)
		if err != nil {
			return err
		}
		row.Body, row.HasBody = sealed, sealed != ""
	}
	return TelemetryDB().Create(&row).Error
}

func truncateMailError(text string) string {
	return truncateString(strings.TrimSpace(text), 300)
}

func truncateString(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	cut := limit
	for cut > 0 && !isRuneStart(text[cut]) {
		cut--
	}
	return text[:cut]
}

func isRuneStart(b byte) bool { return b&0xC0 != 0x80 }

// ClaimMailDelivery inserts the archive row for a mail that must go out at
// most once (a digest period). It reports false when the key already exists.
func ClaimMailDelivery(row *MailDelivery) (bool, error) {
	if row.RefKey == nil || *row.RefKey == "" {
		return false, errors.New("claim needs a ref key")
	}
	if row.CreatedAt == 0 {
		row.CreatedAt = time.Now().Unix()
	}
	if row.Status == "" {
		row.Status = MailStatusGenerating
	}
	// ON CONFLICT DO NOTHING makes the claim safe across instances: the
	// unique ref_key lets exactly one of them insert.
	result := TelemetryDB().Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "ref_key"}}, DoNothing: true}).Create(row)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected == 1, nil
}

// MailDeliveryExists reports whether a ref key was already claimed.
func MailDeliveryExists(refKey string) (bool, error) {
	var count int64
	err := TelemetryDB().Model(&MailDelivery{}).Where("ref_key = ?", refKey).Count(&count).Error
	return count > 0, err
}

// SaveMailDeliveryContent stores what a claimed digest will send.
func SaveMailDeliveryContent(id uint64, subject string, html string, snapshot string) error {
	body, err := SealMailArchive(html)
	if err != nil {
		return err
	}
	snap, err := SealMailArchive(snapshot)
	if err != nil {
		return err
	}
	return TelemetryDB().Model(&MailDelivery{}).Where("id = ?", id).Updates(map[string]any{
		"subject": truncateString(subject, 512), "body": body, "snapshot": snap, "has_body": body != "", "generated_at": time.Now().Unix(),
	}).Error
}

// FailMailDelivery marks a claimed row failed before anything was sent.
func FailMailDelivery(id uint64, err error) error {
	return TelemetryDB().Model(&MailDelivery{}).Where("id = ?", id).Updates(map[string]any{"status": MailStatusFailed, "error": truncateMailError(err.Error())}).Error
}

// MailDeliveryQuery filters the ops list.
type MailDeliveryQuery struct {
	Kind   string
	Status string
	Search string
	Period string
	Offset int
	Limit  int
}

func ListMailDeliveries(q MailDeliveryQuery) ([]MailDelivery, int64, error) {
	tx := TelemetryDB().Model(&MailDelivery{})
	if q.Kind != "" {
		if q.Kind == "digest" {
			tx = tx.Where("kind IN ?", []string{common.MailKindWeeklyDigest, common.MailKindMonthlyDigest})
		} else {
			tx = tx.Where("kind = ?", q.Kind)
		}
	}
	if q.Status != "" {
		tx = tx.Where("status = ?", q.Status)
	}
	if q.Period != "" {
		tx = tx.Where("period = ?", q.Period)
	}
	if search := strings.TrimSpace(q.Search); search != "" {
		if id, ok := parsePositiveInt(search); ok {
			tx = tx.Where("user_id = ? OR receiver = ?", id, search)
		} else {
			tx = tx.Where("receiver LIKE ?", "%"+strings.ToLower(search)+"%")
		}
	}
	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []MailDelivery
	err := tx.Omit("body", "snapshot").Order("id DESC").Offset(q.Offset).Limit(q.Limit).Find(&rows).Error
	return rows, total, err
}

func parsePositiveInt(text string) (int, bool) {
	n := 0
	for _, r := range text {
		if r < '0' || r > '9' {
			return 0, false
		}
		n = n*10 + int(r-'0')
		if n > 1<<30 {
			return 0, false
		}
	}
	return n, n > 0
}

func GetMailDelivery(id uint64) (*MailDelivery, error) {
	var row MailDelivery
	if err := TelemetryDB().First(&row, id).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

// MailDeliveriesToCheck are recent sends whose delivery Resend has not
// settled yet. Checks back off: 2 min, 10 min, 1 h, 6 h, then stop.
func MailDeliveriesToCheck(now time.Time, limit int) ([]MailDelivery, error) {
	var rows []MailDelivery
	err := TelemetryDB().Select("id", "provider_id", "sent_at", "checked_at", "checks", "status").
		Where("provider_id <> '' AND status IN ? AND sent_at > ? AND checks < 5", []string{MailStatusSent, MailStatusDelayed}, now.Add(-72*time.Hour).Unix()).
		Order("checked_at ASC").Limit(limit * 4).Find(&rows).Error
	if err != nil {
		return nil, err
	}
	backoff := []time.Duration{2 * time.Minute, 10 * time.Minute, time.Hour, 6 * time.Hour, 24 * time.Hour}
	due := rows[:0]
	for _, row := range rows {
		last := row.CheckedAt
		if last == 0 {
			last = row.SentAt
		}
		if now.Unix()-last >= int64(backoff[min(row.Checks, len(backoff)-1)].Seconds()) {
			due = append(due, row)
		}
		if len(due) >= limit {
			break
		}
	}
	return due, nil
}

// MailStatusFromEvent maps a Resend last_event to an archive status.
func MailStatusFromEvent(event string) string {
	switch event {
	case "delivered", "opened", "clicked":
		return MailStatusDelivered
	case "bounced":
		return MailStatusBounced
	case "complained":
		return MailStatusComplained
	case "delivery_delayed":
		return MailStatusDelayed
	case "failed", "canceled", "suppressed":
		return MailStatusFailed
	}
	return MailStatusSent
}

func UpdateMailDeliveryEvent(id uint64, event string, now time.Time) error {
	updates := map[string]any{"checked_at": now.Unix(), "checks": gorm.Expr("checks + 1")}
	if event != "" {
		updates["last_event"] = event
		if status := MailStatusFromEvent(event); status != MailStatusSent {
			updates["status"] = status
			updates["event_at"] = now.Unix()
		}
	}
	return TelemetryDB().Model(&MailDelivery{}).Where("id = ?", id).Updates(updates).Error
}

// PurgeMailArchive drops bodies after 180 days and rows after a year.
func PurgeMailArchive(now time.Time) (int64, int64, error) {
	bodies := TelemetryDB().Model(&MailDelivery{}).Where("created_at < ? AND (body <> '' OR snapshot <> '')", now.Add(-MailBodyRetention).Unix()).
		Updates(map[string]any{"body": "", "snapshot": "", "has_body": false})
	if bodies.Error != nil {
		return 0, 0, bodies.Error
	}
	rows := TelemetryDB().Where("created_at < ?", now.Add(-MailRowRetention).Unix()).Delete(&MailDelivery{})
	return bodies.RowsAffected, rows.RowsAffected, rows.Error
}

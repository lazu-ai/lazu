package model

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ProjectNotification is a durable outbox. Payloads may contain invitation
// links and are encrypted at rest; neither models nor logs expose the payload.
type ProjectNotification struct {
	ID              uint   `gorm:"primaryKey"`
	ProjectID       int    `gorm:"index;not null"`
	EventKey        string `gorm:"size:191;not null;uniqueIndex"`
	RecipientUserID int    `gorm:"index"`
	Payload         string `gorm:"type:text;not null" json:"-"`
	Status          string `gorm:"size:16;not null;default:'pending';index"`
	Attempts        int
	AvailableAt     int64 `gorm:"index"`
	ClaimedAt       *int64
	FirstFailedAt   *int64 `gorm:"index"`
	DeliveredAt     *int64
	CreatedAt       int64 `gorm:"autoCreateTime"`
}

type ProjectEmail struct {
	Receiver string `json:"receiver"`
	Subject  string `json:"subject"`
	HTML     string `json:"html"`
}

func projectNotificationCipher() (cipher.AEAD, error) {
	if common.CryptoSecret == "" {
		return nil, errors.New("notification encryption secret is missing")
	}
	key := sha256.Sum256([]byte("lazu/project-notification/v1/" + common.CryptoSecret))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func sealProjectEmail(eventKey string, email ProjectEmail) (string, error) {
	plaintext, err := json.Marshal(email)
	if err != nil {
		return "", err
	}
	aead, err := projectNotificationCipher()
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	return base64.RawStdEncoding.EncodeToString(aead.Seal(nonce, nonce, plaintext, []byte(eventKey))), nil
}

func QueueProjectEmailTx(tx *gorm.DB, projectID, recipientUserID int, eventKey string, email ProjectEmail) error {
	return queueProjectEmailAtTx(tx, projectID, recipientUserID, eventKey, email, time.Now().Unix())
}

// queueProjectEmailAtTx queues an e-mail that the dispatcher will not pick up
// before availableAt. An existing row with the same event key wins.
func queueProjectEmailAtTx(tx *gorm.DB, projectID, recipientUserID int, eventKey string, email ProjectEmail, availableAt int64) error {
	payload, err := sealProjectEmail(eventKey, email)
	if err != nil {
		return err
	}
	row := ProjectNotification{ProjectID: projectID, RecipientUserID: recipientUserID, EventKey: eventKey, Payload: payload, Status: "pending", AvailableAt: availableAt}
	return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "event_key"}}, DoNothing: true}).Create(&row).Error
}

// replacePendingProjectEmailTx swaps the content of an e-mail that has not
// been claimed for delivery yet and makes it due now; with no row it queues
// one. A row already being sent or delivered is left alone, so a reader never
// receives a second copy of the same event.
func replacePendingProjectEmailTx(tx *gorm.DB, projectID, recipientUserID int, eventKey string, email ProjectEmail) error {
	payload, err := sealProjectEmail(eventKey, email)
	if err != nil {
		return err
	}
	now := time.Now().Unix()
	result := tx.Model(&ProjectNotification{}).Where("event_key = ? AND status = ?", eventKey, "pending").
		Updates(map[string]interface{}{"payload": payload, "available_at": now})
	if result.Error != nil || result.RowsAffected > 0 {
		return result.Error
	}
	row := ProjectNotification{ProjectID: projectID, RecipientUserID: recipientUserID, EventKey: eventKey, Payload: payload, Status: "pending", AvailableAt: now}
	return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "event_key"}}, DoNothing: true}).Create(&row).Error
}

func (row ProjectNotification) Email() (ProjectEmail, error) {
	var result ProjectEmail
	ciphertext, err := base64.RawStdEncoding.DecodeString(row.Payload)
	if err != nil {
		return result, errors.New("invalid notification payload")
	}
	aead, err := projectNotificationCipher()
	if err != nil {
		return result, err
	}
	if len(ciphertext) < aead.NonceSize() {
		return result, errors.New("invalid notification payload")
	}
	plain, err := aead.Open(nil, ciphertext[:aead.NonceSize()], ciphertext[aead.NonceSize():], []byte(row.EventKey))
	if err != nil {
		return result, errors.New("unable to decrypt notification payload")
	}
	err = json.Unmarshal(plain, &result)
	return result, err
}

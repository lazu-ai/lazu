package model

import (
	"testing"
	"time"

	"gorm.io/gorm"
)

func TestSessionContentRetentionPreservesAccountingAndBoundary(t *testing.T) {
	db := withPricingTestDB(t, "session_content_retention")
	if err := db.AutoMigrate(&AgentSession{}, &AgentSessionMessage{}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	cutoff := now.AddDate(0, 0, -180)
	for i, at := range []time.Time{cutoff.Add(-time.Second), cutoff, now} {
		s := AgentSession{PublicID: string(rune('a' + i)), UserID: i + 1, SessionID: string(rune('a' + i)), StartedAt: at.Unix(), LastAt: now.Unix(), FirstMessagePreview: "private title", RequestCount: 3, AmountMicrousd: 500}
		if err := db.Create(&s).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&AgentSessionMessage{SessionPK: s.ID, Content: "private prompt", CreatedAt: at}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := PurgeAgentSessionContent(cutoff); err != nil {
		t.Fatal(err)
	}
	var sessions []AgentSession
	if err := db.Order("user_id").Find(&sessions).Error; err != nil {
		t.Fatal(err)
	}
	for i, s := range sessions {
		if s.AmountMicrousd != 500 || s.RequestCount != 3 {
			t.Fatalf("accounting changed: %+v", s)
		}
		if (s.FirstMessagePreview == "") != (i == 0) {
			t.Fatalf("preview boundary: %+v", s)
		}
	}
	var messages []AgentSessionMessage
	if err := db.Find(&messages).Error; err != nil || len(messages) != 2 {
		t.Fatalf("messages=%+v err=%v", messages, err)
	}
	if err := PurgeAgentSessionContent(cutoff); err != nil {
		t.Fatal(err)
	}
}

func TestAccountDeletionClearsOnlyOwnersSessionContent(t *testing.T) {
	db := withPricingTestDB(t, "delete_session_content")
	if err := db.AutoMigrate(&AgentSession{}, &AgentSessionMessage{}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int{10, 20} {
		s := AgentSession{UserID: id, PublicID: string(rune(id)), SessionID: string(rune(id)), FirstMessagePreview: "title", AmountMicrousd: 100}
		if err := db.Create(&s).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&AgentSessionMessage{SessionPK: s.ID, Content: "prompt"}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Transaction(func(tx *gorm.DB) error { return finishUserProjectDeletionTx(tx, 10, false) }); err != nil {
		t.Fatal(err)
	}
	var owner, other AgentSession
	db.Where("user_id = ?", 10).First(&owner)
	db.Where("user_id = ?", 20).First(&other)
	var count int64
	db.Model(&AgentSessionMessage{}).Count(&count)
	if owner.FirstMessagePreview != "" || owner.AmountMicrousd != 100 || other.FirstMessagePreview != "title" || count != 1 {
		t.Fatalf("owner=%+v other=%+v messages=%d", owner, other, count)
	}
}

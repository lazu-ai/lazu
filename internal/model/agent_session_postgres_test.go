package model

import (
	"os"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// Optional integration check. Only a disposable, explicitly named database is
// accepted; never migrate a developer's application database or production.
func TestAgentSessionPostgresIntegrity(t *testing.T) {
	dsn := os.Getenv("LAZU_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("LAZU_TEST_POSTGRES_DSN not set")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	var name string
	if err := db.Raw("SELECT current_database()").Scan(&name).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(name, "lazu_audit_test_") {
		t.Fatal("refusing a non-disposable database")
	}
	previous, previousLog := coreDB, telemetryDB
	coreDB, telemetryDB = db, db
	t.Cleanup(func() { coreDB, telemetryDB = previous, previousLog })
	if err := db.AutoMigrate(&AgentSession{}, &AgentSessionMessage{}, &AgentSessionThread{}, &AgentSessionModel{}, &Log{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	old := now.AddDate(0, 0, -181)
	rows := []AgentSession{
		{PublicID: "pg-one", UserID: 42, SessionID: "pg-1", Source: "codex", RequestCount: 2, AmountMicrousd: 100, StartedAt: old.Unix(), LastAt: old.Unix() + 10, FirstMessagePreview: "expired"},
		{PublicID: "pg-two", UserID: 42, SessionID: "pg-2", Source: "codex", RequestCount: 4, AmountMicrousd: 300, StartedAt: now.Unix(), LastAt: now.Unix() + 30},
		{PublicID: "pg-other", UserID: 43, SessionID: "pg-other", Source: "codex", RequestCount: 100, AmountMicrousd: 9999, StartedAt: now.Unix(), LastAt: now.Unix()},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	message := AgentSessionMessage{SessionPK: rows[0].ID, Content: "expired", CreatedAt: old}
	if err := db.Create(&message).Error; err != nil {
		t.Fatal(err)
	}
	_, total, summary, err := ListAgentSessions(AgentSessionQuery{UserID: 42, Num: 1})
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || summary.AmountMicrousd != 400 || summary.MedianAmount == nil || *summary.MedianAmount != 200 || *summary.MedianRequests != 3 || *summary.MedianDuration != 20 {
		t.Fatalf("incorrect aggregate: %+v", summary)
	}
	if err := PurgeAgentSessionContent(now.AddDate(0, 0, -180)); err != nil {
		t.Fatal(err)
	}
	var count int64
	db.Model(&AgentSessionMessage{}).Count(&count)
	if count != 0 {
		t.Fatal("expired content retained")
	}
	var row AgentSession
	if err := db.First(&row, rows[0].ID).Error; err != nil {
		t.Fatal(err)
	}
	if row.FirstMessagePreview != "" || row.AmountMicrousd != 100 {
		t.Fatal("retention did not preserve only accounting")
	}
}

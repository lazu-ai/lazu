package service

import (
	"context"
	"strings"
	"testing"
	"time"

	gormsqlite "github.com/glebarez/sqlite"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
	"gorm.io/gorm"
)

func TestDigestArchiveUsesSeparateTelemetryStore(t *testing.T) {
	core := setupSpendBudgetServiceTestDB(t)
	if err := core.AutoMigrate(&model.Ability{}, &model.Channel{}); err != nil {
		t.Fatal(err)
	}
	telemetry, err := gorm.Open(gormsqlite.Open("file:digest-split-store?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	model.SetTelemetryDB(telemetry)
	t.Cleanup(func() { raw, _ := telemetry.DB(); _ = raw.Close() })
	if err := telemetry.AutoMigrate(model.TelemetrySchemaModels()...); err != nil {
		t.Fatal(err)
	}
	// Keep a stale legacy archive to prove no write, delete or interrupted
	// claim cleanup crosses back into Core after the split.
	stale := model.MailDelivery{Kind: common.MailKindWeeklyDigest, UserID: 7, Status: model.MailStatusGenerating, CreatedAt: 1}
	if err := core.Create(&stale).Error; err != nil {
		t.Fatal(err)
	}
	user := model.User{Id: 7, Username: "digest-split", Email: "reader@example.test", Status: common.UserStatusEnabled, HasToppedUp: true, Setting: `{"timezone":"UTC","language":"en"}`}
	if err := core.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	oldOptions, oldEnabled, oldSecret := common.OptionMap, common.UsageDigestEnabled, common.CryptoSecret
	oldResend, oldSMTP, oldFrom, oldAccount, oldRecorder := common.AppConfig.Resend.APIKey, common.SMTPServer, common.SMTPFrom, common.SMTPAccount, common.MailRecorder
	common.OptionMap = map[string]string{model.DigestRollupSinceOption: "1", DigestMinSpendOption: "0"}
	common.UsageDigestEnabled, common.CryptoSecret = true, "digest-split-fixture"
	// No transport is configured: exercise rendering and encrypted archive
	// writes, then stop before any network send. Record the local failed attempt.
	common.AppConfig.Resend.APIKey, common.SMTPServer, common.SMTPAccount, common.SMTPFrom = "", "", "", "fixture@example.test"
	common.MailRecorder = model.RecordMailDelivery
	t.Cleanup(func() {
		common.OptionMap, common.UsageDigestEnabled, common.CryptoSecret = oldOptions, oldEnabled, oldSecret
		common.AppConfig.Resend.APIKey, common.SMTPServer, common.SMTPFrom, common.SMTPAccount, common.MailRecorder = oldResend, oldSMTP, oldFrom, oldAccount, oldRecorder
	})
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC) // outside scheduled send windows
	t.Run("copy", func(t *testing.T) {
		err := SendDigestCopy(user.Id, DigestWeekly, "operator@example.test", now)
		if err == nil || !strings.Contains(err.Error(), "email service not configured") {
			t.Fatalf("copy did not reach disabled transport: %v", err)
		}
		var row model.MailDelivery
		if err := telemetry.Where("kind = ?", common.MailKindCopy).First(&row).Error; err != nil {
			t.Fatal(err)
		}
		if html, err := row.HTML(); err != nil || html == "" || row.Subject == "" {
			t.Fatalf("copy archive missing: %v", err)
		}
	})
	t.Run("regenerate", func(t *testing.T) {
		old := model.MailDelivery{Kind: common.MailKindWeeklyDigest, UserID: user.Id, Period: "2026-W40", Timezone: "UTC", Status: model.MailStatusSent}
		if err := telemetry.Create(&old).Error; err != nil {
			t.Fatal(err)
		}
		err := RegenerateDigest(old.ID, now)
		if err == nil || !strings.Contains(err.Error(), "email service not configured") {
			t.Fatalf("regenerate did not reach disabled transport: %v", err)
		}
		var row model.MailDelivery
		if err := telemetry.Where("id > ? AND kind = ?", old.ID, old.Kind).First(&row).Error; err != nil || row.Snapshot == "" {
			t.Fatalf("regenerated archive missing: %+v %v", row, err)
		}
	})
	t.Run("replace drafts", func(t *testing.T) {
		for range 2 {
			if n, err := GenerateDigestDrafts(DigestWeekly, now); err != nil || n != 1 {
				t.Fatalf("drafts count=%d err=%v", n, err)
			}
		}
		var count int64
		if err := telemetry.Model(&model.MailDelivery{}).Where("status = ?", model.MailStatusDraft).Count(&count).Error; err != nil || count != 1 {
			t.Fatalf("draft replacement count=%d err=%v", count, err)
		}
	})
	t.Run("interrupted claim", func(t *testing.T) {
		row := model.MailDelivery{Kind: common.MailKindWeeklyDigest, UserID: 7, Status: model.MailStatusGenerating, CreatedAt: 1}
		if err := telemetry.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
		if err := RunUsageDigests(context.Background(), now); err != nil {
			t.Fatal(err)
		}
		if err := telemetry.First(&row, row.ID).Error; err != nil || row.Status != model.MailStatusFailed {
			t.Fatalf("claim not released: %+v %v", row, err)
		}
	})
	var rows []model.MailDelivery
	if err := core.Find(&rows).Error; err != nil || len(rows) != 1 || rows[0].Status != model.MailStatusGenerating {
		t.Fatalf("legacy Core archive changed: %+v %v", rows, err)
	}
}

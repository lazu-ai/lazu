package model

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
)

func setupMailTestDB(t *testing.T) {
	t.Helper()
	db := setupProjectTestDB(t, false)
	if err := db.AutoMigrate(&MailDelivery{}); err != nil {
		t.Fatal(err)
	}
	old := common.CryptoSecret
	common.CryptoSecret = "mail-archive-test"
	t.Cleanup(func() { common.CryptoSecret = old })
}

func TestMailArchiveSealsBodiesAndKeepsCredentialsOut(t *testing.T) {
	setupMailTestDB(t)
	user := User{Username: "reader", Email: "reader@example.com"}
	if err := coreDB.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	body := "<p>" + strings.Repeat("usage ", 200) + "</p>"
	RecordMailDelivery(common.MailRecord{Meta: common.MailMeta{Kind: common.MailKindBalance}, Subject: "Low balance", Receiver: "reader@example.com", HTML: body, ProviderID: "re_1"})
	RecordMailDelivery(common.MailRecord{Meta: common.MailMeta{Kind: common.MailKindAuth, Sensitive: true}, Subject: "Your code", Receiver: "reader@example.com", HTML: "code 123456", Err: errors.New("provider down")})

	var rows []MailDelivery
	if err := coreDB.Order("id").Find(&rows).Error; err != nil || len(rows) != 2 {
		t.Fatalf("rows = %d %v", len(rows), err)
	}
	if rows[0].UserID != user.Id || rows[0].Status != MailStatusSent || !rows[0].HasBody || strings.Contains(rows[0].Body, "usage") {
		t.Fatalf("archived row = %+v", rows[0])
	}
	if got, err := rows[0].HTML(); err != nil || got != body {
		t.Fatalf("body did not round-trip: %v", err)
	}
	if rows[1].Body != "" || rows[1].HasBody || rows[1].Status != MailStatusFailed || rows[1].Error == "" {
		t.Fatalf("credential mail kept its body or lost its failure: %+v", rows[1])
	}
}

func TestClaimMailDeliveryLetsOnePeriodGoOutOnce(t *testing.T) {
	setupMailTestDB(t)
	key := "digest:weekly:1:2026-W39"
	first := &MailDelivery{Kind: common.MailKindWeeklyDigest, UserID: 1, RefKey: &key}
	second := &MailDelivery{Kind: common.MailKindWeeklyDigest, UserID: 1, RefKey: &key}
	if ok, err := ClaimMailDelivery(first); err != nil || !ok {
		t.Fatalf("first claim = %v %v", ok, err)
	}
	if ok, err := ClaimMailDelivery(second); err != nil || ok {
		t.Fatalf("second claim = %v %v", ok, err)
	}
	if exists, _ := MailDeliveryExists(key); !exists {
		t.Fatal("claimed key not found")
	}
}

func TestPurgeMailArchiveDropsOldBodiesThenRows(t *testing.T) {
	setupMailTestDB(t)
	now := time.Now()
	for _, age := range []time.Duration{24 * time.Hour, 200 * 24 * time.Hour, 400 * 24 * time.Hour} {
		sealed, _ := SealMailArchive("<p>hi</p>")
		if err := coreDB.Create(&MailDelivery{Kind: "other", Status: MailStatusSent, Body: sealed, HasBody: true, CreatedAt: now.Add(-age).Unix()}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := PurgeMailArchive(now); err != nil {
		t.Fatal(err)
	}
	var rows []MailDelivery
	coreDB.Order("created_at DESC").Find(&rows)
	if len(rows) != 2 || !rows[0].HasBody || rows[1].HasBody || rows[1].Body != "" {
		t.Fatalf("after purge: %+v", rows)
	}
}

func TestSetUserSettingValuesKeepsOtherKeys(t *testing.T) {
	setupMailTestDB(t)
	user := User{Username: "prefs", Setting: `{"language":"zh","notify_type":"email"}`}
	if err := coreDB.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	if err := SetUserSettingValues(user.Id, map[string]any{"weekly_digest_off": true}); err != nil {
		t.Fatal(err)
	}
	if err := FillUserSettingValues(user.Id, map[string]any{"timezone": "Asia/Shanghai"}); err != nil {
		t.Fatal(err)
	}
	if err := FillUserSettingValues(user.Id, map[string]any{"timezone": "Europe/Berlin"}); err != nil {
		t.Fatal(err)
	}
	var stored User
	coreDB.First(&stored, user.Id)
	s := stored.GetSetting()
	if !s.WeeklyDigestOff || s.Timezone != "Asia/Shanghai" || s.Language != "zh" || s.NotifyType != "email" {
		t.Fatalf("settings = %+v", s)
	}
	if err := SetUserSettingValues(user.Id, map[string]any{"weekly_digest_off": false}); err != nil {
		t.Fatal(err)
	}
	coreDB.First(&stored, user.Id)
	if strings.Contains(stored.Setting, "weekly_digest_off") {
		t.Fatalf("turning mail back on left the key: %s", stored.Setting)
	}
}

package model

import (
	"testing"

	"github.com/lazu-ai/lazu/internal/common"
)

// A settled charge stamps the Key's last_used_at (it replaced accessed_time,
// which was only ever seeded at creation); a refund leaves it alone.
func TestTokenChargeStampsLastUsedAt(t *testing.T) {
	db := withPricingTestDB(t, "token_charge_last_used_at")
	token := Token{UserId: 1, Key: "last-used-key", Name: "k", RemainQuota: 1_000, Status: common.TokenStatusEnabled, ExpiredTime: -1}
	if err := db.Create(&token).Error; err != nil {
		t.Fatalf("seed token: %v", err)
	}
	before := common.GetTimestamp()
	if _, err := applyTokenQuotaDeltaTx(db, token.Id, token.Key, 100); err != nil {
		t.Fatalf("charge: %v", err)
	}
	var charged Token
	if err := db.First(&charged, token.Id).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if charged.LastUsedAt < before || charged.RemainQuota != 900 || charged.UsedQuota != 100 {
		t.Fatalf("charge did not stamp last_used_at: %+v", charged)
	}
	if err := db.Model(&Token{}).Where("id = ?", token.Id).UpdateColumn("last_used_at", 5).Error; err != nil {
		t.Fatalf("reset last_used_at: %v", err)
	}
	if _, err := applyTokenQuotaDeltaTx(db, token.Id, token.Key, -40); err != nil {
		t.Fatalf("refund: %v", err)
	}
	var refunded Token
	if err := db.First(&refunded, token.Id).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if refunded.LastUsedAt != 5 || refunded.RemainQuota != 940 {
		t.Fatalf("refund must not count as use: %+v", refunded)
	}
}

// Deleting a channel keeps its row (with deleted_at) so past logs, outcomes
// and costs still resolve it, while its abilities go.
func TestChannelDeleteIsSoft(t *testing.T) {
	db := withPricingTestDB(t, "channel_soft_delete")
	if err := db.AutoMigrate(&Channel{}); err != nil {
		t.Fatalf("migrate channels: %v", err)
	}
	channel := Channel{Id: 31, Name: "soft-delete", Status: common.ChannelStatusEnabled}
	if err := db.Create(&channel).Error; err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	if err := db.Create(&Ability{Group: "default", Model: "gpt-test", ChannelId: channel.Id, Enabled: true}).Error; err != nil {
		t.Fatalf("seed ability: %v", err)
	}
	if err := channel.Delete(); err != nil {
		t.Fatalf("delete channel: %v", err)
	}
	var visible int64
	if err := db.Model(&Channel{}).Where("id = ?", channel.Id).Count(&visible).Error; err != nil || visible != 0 {
		t.Fatalf("deleted channel still visible: count=%d err=%v", visible, err)
	}
	var kept Channel
	if err := db.Unscoped().First(&kept, channel.Id).Error; err != nil {
		t.Fatalf("soft-deleted row is gone: %v", err)
	}
	if !kept.DeletedAt.Valid || kept.Status != common.ChannelStatusManuallyDisabled || kept.Name != "soft-delete" {
		t.Fatalf("soft-deleted row: %+v", kept)
	}
	var abilities int64
	if err := db.Model(&Ability{}).Where("channel_id = ?", channel.Id).Count(&abilities).Error; err != nil || abilities != 0 {
		t.Fatalf("abilities survived delete: %d err=%v", abilities, err)
	}
}

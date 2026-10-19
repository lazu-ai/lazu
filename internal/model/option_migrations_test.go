package model

import (
	"errors"
	"fmt"
	"testing"

	"github.com/lazu-ai/lazu/internal/setting/email_setting"

	gormsqlite "github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func withOptionMigrationDB(t *testing.T) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(gormsqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&Option{}); err != nil {
		t.Fatalf("failed to migrate options: %v", err)
	}
	t.Cleanup(func() {
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

func TestDeleteLegacyReferralOptions(t *testing.T) {
	db := withOptionMigrationDB(t)
	rows := []Option{
		{Key: "QuotaForInviter", Value: "1000000"},
		{Key: "QuotaForInvitee", Value: "500000"},
		{Key: "QuotaForNewUser", Value: "2000000"},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("seed referral options: %v", err)
	}
	for _, key := range legacyReferralOptionKeys {
		if !IsIgnoredLegacyOption(key) {
			t.Fatalf("retired referral option %q is still accepted by the runtime", key)
		}
	}
	for range 2 {
		if err := deleteLegacyReferralOptions(db); err != nil {
			t.Fatalf("delete retired referral options: %v", err)
		}
	}
	var remaining []Option
	if err := db.Order("key").Find(&remaining).Error; err != nil {
		t.Fatalf("list remaining options: %v", err)
	}
	if len(remaining) != 1 || remaining[0].Key != "QuotaForNewUser" {
		t.Fatalf("unexpected options after cleanup: %+v", remaining)
	}
}

// withLowBalanceEmail sets the runtime email threshold — what YAML and defaults
// produce before the database loads — for one test.
func withLowBalanceEmail(t *testing.T, enabled bool, thresholdUSD float64) {
	t.Helper()
	cfg := email_setting.GetEmailSetting()
	previousEnabled, previousThreshold := cfg.LowBalanceEnabled, cfg.LowBalanceThresholdUSD
	cfg.LowBalanceEnabled = enabled
	cfg.LowBalanceThresholdUSD = thresholdUSD
	t.Cleanup(func() {
		cfg.LowBalanceEnabled = previousEnabled
		cfg.LowBalanceThresholdUSD = previousThreshold
	})
}

func lowBalanceThresholdValue(t *testing.T, db *gorm.DB) (string, bool) {
	t.Helper()

	var option Option
	err := db.Where("key = ?", lowBalanceThresholdOptionKey).First(&option).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", false
	}
	if err != nil {
		t.Fatalf("failed to read merged threshold: %v", err)
	}
	return option.Value, true
}

func TestMigrateQuotaRemindThresholdAdoptsLegacyValue(t *testing.T) {
	db := withOptionMigrationDB(t)
	// With low-balance mail off, QuotaRemindThreshold was the value in effect.
	withLowBalanceEmail(t, false, 20)
	// 12_500_000 microUSD is $12.50.
	if err := db.Create(&Option{Key: retiredQuotaRemindThresholdKey, Value: "12500000"}).Error; err != nil {
		t.Fatalf("failed to seed legacy option: %v", err)
	}

	if err := migrateQuotaRemindThreshold(db); err != nil {
		t.Fatalf("migration failed: %v", err)
	}

	value, ok := lowBalanceThresholdValue(t, db)
	if !ok {
		t.Fatal("expected the merged threshold to be written")
	}
	if value != "12.5" {
		t.Fatalf("expected the legacy microUSD value converted to USD, got %q", value)
	}

	var legacyCount int64
	if err := db.Model(&Option{}).Where("key = ?", retiredQuotaRemindThresholdKey).Count(&legacyCount).Error; err != nil {
		t.Fatalf("failed to count legacy rows: %v", err)
	}
	if legacyCount != 0 {
		t.Fatal("expected the retired option row to be deleted")
	}
}

func TestMigrateQuotaRemindThresholdKeepsConfiguredThreshold(t *testing.T) {
	db := withOptionMigrationDB(t)
	if err := db.Create(&Option{Key: retiredQuotaRemindThresholdKey, Value: "12500000"}).Error; err != nil {
		t.Fatalf("failed to seed legacy option: %v", err)
	}
	if err := db.Create(&Option{Key: lowBalanceThresholdOptionKey, Value: "40"}).Error; err != nil {
		t.Fatalf("failed to seed merged option: %v", err)
	}

	if err := migrateQuotaRemindThreshold(db); err != nil {
		t.Fatalf("migration failed: %v", err)
	}

	value, ok := lowBalanceThresholdValue(t, db)
	if !ok {
		t.Fatal("expected the merged threshold to survive")
	}
	if value != "40" {
		t.Fatalf("a deployment that tuned its own threshold must keep it, got %q", value)
	}
}

func TestMigrateQuotaRemindThresholdIsIdempotent(t *testing.T) {
	db := withOptionMigrationDB(t)

	if err := migrateQuotaRemindThreshold(db); err != nil {
		t.Fatalf("migration failed on a database without the legacy row: %v", err)
	}
	if _, ok := lowBalanceThresholdValue(t, db); ok {
		t.Fatal("no legacy row means nothing to adopt")
	}
}

func TestMigrateQuotaRemindThresholdCarriesAnExplicitZero(t *testing.T) {
	db := withOptionMigrationDB(t)
	withLowBalanceEmail(t, false, 20)
	// Zero means "never warn". Dropping it would hand this deployment the $20
	// default and start mailing users it had decided never to mail.
	if err := db.Create(&Option{Key: retiredQuotaRemindThresholdKey, Value: "0"}).Error; err != nil {
		t.Fatalf("failed to seed legacy option: %v", err)
	}

	if err := migrateQuotaRemindThreshold(db); err != nil {
		t.Fatalf("migration failed: %v", err)
	}

	value, ok := lowBalanceThresholdValue(t, db)
	if !ok {
		t.Fatal("expected the merged threshold to be written")
	}
	if value != "0" {
		t.Fatalf("expected the explicit zero to survive, got %q", value)
	}
}

func TestMigrateQuotaRemindThresholdKeepsTheYAMLThresholdInEffect(t *testing.T) {
	db := withOptionMigrationDB(t)
	// YAML enabled low-balance mail at $35 and stored no option row. That was
	// the threshold the gateway ran with, so the legacy row must not replace it.
	withLowBalanceEmail(t, true, 35)
	if err := db.Create(&Option{Key: retiredQuotaRemindThresholdKey, Value: "12500000"}).Error; err != nil {
		t.Fatalf("failed to seed legacy option: %v", err)
	}

	if err := migrateQuotaRemindThreshold(db); err != nil {
		t.Fatalf("migration failed: %v", err)
	}

	if value, ok := lowBalanceThresholdValue(t, db); ok {
		t.Fatalf("the YAML threshold was in effect; expected no stored override, got %q", value)
	}
	var legacyCount int64
	if err := db.Model(&Option{}).Where("key = ?", retiredQuotaRemindThresholdKey).Count(&legacyCount).Error; err != nil {
		t.Fatalf("failed to count legacy rows: %v", err)
	}
	if legacyCount != 0 {
		t.Fatal("the retired row is removed either way")
	}
}

func TestMigrateQuotaRemindThresholdHonoursAStoredDisabledSwitch(t *testing.T) {
	db := withOptionMigrationDB(t)
	// YAML says enabled, but a stored option turned the mail off — InitOptionMap
	// applies that row later, so the legacy threshold was the one in effect.
	withLowBalanceEmail(t, true, 20)
	if err := db.Create(&Option{Key: lowBalanceEnabledOptionKey, Value: "false"}).Error; err != nil {
		t.Fatalf("failed to seed switch: %v", err)
	}
	if err := db.Create(&Option{Key: retiredQuotaRemindThresholdKey, Value: "5000000"}).Error; err != nil {
		t.Fatalf("failed to seed legacy option: %v", err)
	}

	if err := migrateQuotaRemindThreshold(db); err != nil {
		t.Fatalf("migration failed: %v", err)
	}

	value, ok := lowBalanceThresholdValue(t, db)
	if !ok || value != "5" {
		t.Fatalf("expected the legacy $5 to carry over, got %q (stored=%v)", value, ok)
	}
}

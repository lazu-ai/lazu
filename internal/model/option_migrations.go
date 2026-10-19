package model

import (
	"errors"
	"strconv"
	"strings"

	"github.com/lazu-ai/lazu/internal/setting/email_setting"
	"github.com/lazu-ai/lazu/pkg/billing"
	"gorm.io/gorm"
)

const (
	retiredQuotaRemindThresholdKey = "QuotaRemindThreshold"
	lowBalanceThresholdOptionKey   = "email_setting.low_balance_threshold_usd"
	lowBalanceEnabledOptionKey     = "email_setting.low_balance_enabled"
)

// migrateQuotaRemindThreshold folds the retired QuotaRemindThreshold option
// into email_setting.low_balance_threshold_usd.
//
// Both options held "the balance below which we warn", and the gateway read
// the email one whenever low-balance mail was enabled — which is the default —
// so an operator editing QuotaRemindThreshold in the console was usually
// editing a value nothing consumed. The merged key is now the only threshold,
// and the stale row is removed so it cannot reappear in the settings payload.
//
// The legacy value is adopted only where it was the threshold in effect. Before
// the merge the gateway used the email threshold whenever low-balance mail was
// enabled with a positive threshold, and QuotaRemindThreshold otherwise. That
// email threshold can come from YAML — loaded before the database — without an
// option row, so "the merged key was never stored" is not the same as "the
// merged value was not in effect": adopting then would replace a threshold the
// deployment was running with one it had stopped using.
func migrateQuotaRemindThreshold(db *gorm.DB) error {
	if db == nil || !db.Migrator().HasTable(&Option{}) {
		return nil
	}

	var legacy Option
	err := db.Where("key = ?", retiredQuotaRemindThresholdKey).First(&legacy).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}

	var stored int64
	if err := db.Model(&Option{}).
		Where("key = ?", lowBalanceThresholdOptionKey).
		Count(&stored).Error; err != nil {
		return err
	}

	emailInEffect, err := lowBalanceEmailThresholdInEffect(db)
	if err != nil {
		return err
	}

	if stored == 0 && !emailInEffect {
		// Zero is a real setting — it means "never warn" — so it migrates like
		// any other value. Treating it as "nothing to carry over" would hand
		// that deployment the $20 default and start mailing users it had
		// deliberately decided never to mail.
		micro, parseErr := strconv.ParseInt(strings.TrimSpace(legacy.Value), 10, 64)
		if parseErr == nil && micro >= 0 {
			dollars := billing.MicroUSD(micro).Dollars()
			if err := db.Create(&Option{
				Key:   lowBalanceThresholdOptionKey,
				Value: strconv.FormatFloat(dollars, 'f', -1, 64),
			}).Error; err != nil {
				return err
			}
		}
	}

	return db.Where("key = ?", retiredQuotaRemindThresholdKey).Delete(&Option{}).Error
}

// lowBalanceEnabledOptionKey's stored row wins over YAML, as InitOptionMap
// applies it later; the threshold itself has no row by the time this is asked.
func lowBalanceEmailThresholdInEffect(db *gorm.DB) (bool, error) {
	cfg := email_setting.GetEmailSetting()
	enabled := cfg.LowBalanceEnabled

	var row Option
	err := db.Where("key = ?", lowBalanceEnabledOptionKey).First(&row).Error
	switch {
	case err == nil:
		if parsed, parseErr := strconv.ParseBool(strings.TrimSpace(row.Value)); parseErr == nil {
			enabled = parsed
		}
	case !errors.Is(err, gorm.ErrRecordNotFound):
		return false, err
	}

	return enabled && cfg.LowBalanceThresholdUSD > 0, nil
}

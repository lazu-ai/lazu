package model

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// SetUserSettingValues merges individual keys into users.setting under a row
// lock, leaving every other key as it is. A false or empty value removes the
// key, matching the omitempty fields of dto.UserSetting.
func SetUserSettingValues(userID int, values map[string]any) error {
	return updateUserSettingKeys(userID, values, true)
}

// FillUserSettingValues writes only the keys the account does not have yet.
func FillUserSettingValues(userID int, values map[string]any) error {
	return updateUserSettingKeys(userID, values, false)
}

func updateUserSettingKeys(userID int, values map[string]any, overwrite bool) error {
	if userID <= 0 || len(values) == 0 {
		return nil
	}
	changed := false
	err := CoreDB().Transaction(func(tx *gorm.DB) error {
		var current User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Select("id", "setting").First(&current, userID).Error; err != nil {
			return err
		}
		settings := make(map[string]json.RawMessage)
		if strings.TrimSpace(current.Setting) != "" {
			if err := json.Unmarshal([]byte(current.Setting), &settings); err != nil {
				return fmt.Errorf("invalid stored user setting: %w", err)
			}
			if settings == nil {
				settings = make(map[string]json.RawMessage)
			}
		}
		for key, value := range values {
			if existing, ok := settings[key]; ok && !overwrite && len(existing) > 0 && string(existing) != `""` && string(existing) != "null" {
				continue
			}
			if value == false || value == "" || value == nil {
				if _, ok := settings[key]; ok {
					delete(settings, key)
					changed = true
				}
				continue
			}
			encoded, err := json.Marshal(value)
			if err != nil {
				return err
			}
			if string(settings[key]) != string(encoded) {
				settings[key] = encoded
				changed = true
			}
		}
		if !changed {
			return nil
		}
		merged, err := json.Marshal(settings)
		if err != nil {
			return err
		}
		result := tx.Model(&User{}).Where("id = ?", current.Id).Update("setting", string(merged))
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errors.New("user setting update affected no row")
		}
		return nil
	})
	if err == nil && changed {
		_ = InvalidateUserCache(userID)
	}
	return err
}

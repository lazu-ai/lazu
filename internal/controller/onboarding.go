package controller

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/httperr"
	"github.com/lazu-ai/lazu/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// onboardingState reports first-run progress. Every step is derived from real
// account state rather than a stored checklist, so progress stays correct when
// a step is completed outside the guide.
type onboardingState struct {
	Active        bool `json:"active"`
	HasKey        bool `json:"has_key"`
	HasRequest    bool `json:"has_request"`
	HasAlert      bool `json:"has_alert"`
	NeedsPassword bool `json:"needs_password"`
	PasswordSet   bool `json:"password_set"`
}

func (state onboardingState) complete() bool {
	if !state.HasKey || !state.HasRequest || !state.HasAlert {
		return false
	}
	return !state.NeedsPassword || state.PasswordSet
}

func buildOnboardingState(user *model.User) (onboardingState, error) {
	setting := user.GetSetting()
	state := onboardingState{
		Active:        setting.OnboardingActive,
		HasAlert:      setting.QuotaWarningThreshold > 0,
		NeedsPassword: setting.NeedsPassword,
		PasswordSet:   !setting.NeedsPassword,
	}
	keyCount, err := model.CountUsableUserTokens(user.Id)
	if err != nil {
		return state, err
	}
	state.HasKey = keyCount > 0
	state.HasRequest, err = model.HasPublicAPIRequest(user.Id)
	if err != nil {
		return state, err
	}
	return state, nil
}

// GetSelfOnboarding returns the current user's first-run progress. When every
// applicable step is done the flag is retired, so the guide never reappears.
func GetSelfOnboarding(c *gin.Context) {
	userID := c.GetInt("id")
	user, err := model.GetUserById(userID, false)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	state, err := buildOnboardingState(user)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	if state.Active && state.complete() {
		if err := retireOnboarding(user); err != nil {
			httperr.Write(c, err)
			return
		}
		state.Active = false
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": state})
}

// DismissSelfOnboarding retires the guide at the user's request.
func DismissSelfOnboarding(c *gin.Context) {
	userID := c.GetInt("id")
	user, err := model.GetUserById(userID, false)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	if err := retireOnboarding(user); err != nil {
		httperr.Write(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": ""})
}

func retireOnboarding(user *model.User) error {
	if user == nil || user.Id <= 0 {
		return fmt.Errorf("onboarding update needs a user")
	}

	var updatedSetting string
	changed := false
	err := model.CoreDB().Transaction(func(tx *gorm.DB) error {
		var current model.User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Select("id", "setting").First(&current, user.Id).Error; err != nil {
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
		var active bool
		if raw, ok := settings["onboarding_active"]; ok {
			if err := json.Unmarshal(raw, &active); err != nil {
				return fmt.Errorf("invalid onboarding setting: %w", err)
			}
		}
		if !active {
			updatedSetting = current.Setting
			return nil
		}

		delete(settings, "onboarding_active")
		encoded, err := json.Marshal(settings)
		if err != nil {
			return err
		}
		updatedSetting = string(encoded)
		result := tx.Model(&model.User{}).Where("id = ?", current.Id).Update("setting", updatedSetting)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return fmt.Errorf("onboarding update affected no row")
		}
		changed = true
		return nil
	})
	if err != nil {
		return err
	}
	user.Setting = updatedSetting
	if !changed {
		return nil
	}
	return model.InvalidateUserCache(user.Id)
}

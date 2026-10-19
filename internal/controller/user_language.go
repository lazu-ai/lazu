package controller

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/httperr"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/types"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// The console language lives in the browser; the backend keeps a copy only so
// mail reaches each person in the language they read the console in.

var localeTagPattern = regexp.MustCompile(`^[A-Za-z]{2,3}(-[A-Za-z0-9]{2,8})?$`)

// normalizeLocaleTag accepts a BCP 47 language tag such as en, zh or zh-TW,
// or the first entry of an Accept-Language value. Anything else is "".
func normalizeLocaleTag(raw string) string {
	raw = strings.TrimSpace(strings.SplitN(strings.SplitN(raw, ",", 2)[0], ";", 2)[0])
	if len(raw) > 16 || !localeTagPattern.MatchString(raw) {
		return ""
	}
	return raw
}

type updateSelfLanguageRequest struct {
	Language string `json:"language"`
}

// UpdateSelfLanguage records the console language the account switched to.
func UpdateSelfLanguage(c *gin.Context) {
	var req updateSelfLanguageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httperr.Code(c, types.ErrorCodeInvalidRequest)
		return
	}
	language := normalizeLocaleTag(req.Language)
	if language == "" {
		httperr.Invalid(c, "language")
		return
	}
	userID := c.GetInt("id")
	changed, err := setUserLanguageLocked(userID, language, true)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	if changed {
		if err := model.InvalidateUserCache(userID); err != nil {
			common.SysLog(fmt.Sprintf("failed to invalidate user cache after language update for user %d: %v", userID, err))
		}
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": ""})
}

// rememberRequestLanguage stores the sign-in request's language for an
// account that has none yet. An explicit console choice is never overwritten
// by a browser default.
func rememberRequestLanguage(c *gin.Context, user *model.User) {
	if c == nil || c.Request == nil || user == nil || user.Id <= 0 || model.CoreDB() == nil {
		return
	}
	if user.GetSetting().Language != "" {
		return
	}
	language := normalizeLocaleTag(c.GetHeader("Accept-Language"))
	if language == "" {
		return
	}
	changed, err := setUserLanguageLocked(user.Id, language, false)
	if err != nil {
		common.SysLog(fmt.Sprintf("failed to remember language for user %d: %v", user.Id, err))
		return
	}
	if changed {
		setting := user.GetSetting()
		setting.Language = language
		user.SetSetting(setting)
		_ = model.InvalidateUserCache(user.Id)
	}
}

// setUserLanguageLocked writes settings.language. With overwrite false it
// only fills an empty value.
func setUserLanguageLocked(userID int, language string, overwrite bool) (bool, error) {
	changed := false
	err := model.CoreDB().Transaction(func(tx *gorm.DB) error {
		var current model.User
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
		if existing, ok := settings["language"]; ok && !overwrite {
			var stored string
			if json.Unmarshal(existing, &stored) == nil && stored != "" {
				return nil
			}
		}
		encoded, err := json.Marshal(language)
		if err != nil {
			return err
		}
		if string(settings["language"]) == string(encoded) {
			return nil
		}
		settings["language"] = encoded
		merged, err := json.Marshal(settings)
		if err != nil {
			return err
		}
		result := tx.Model(&model.User{}).Where("id = ?", current.Id).Update("setting", string(merged))
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errors.New("user language update affected no row")
		}
		changed = true
		return nil
	})
	return changed, err
}

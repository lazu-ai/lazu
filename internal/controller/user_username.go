package controller

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/httperr"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/types"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// The username is a login identifier, so a rename frees the old handle for
// whoever asks next. The policy below keeps that transfer slow and legible:
// a strict shape, a reserved list the product depends on, a uniqueness check
// that also sees soft-deleted rows, and a cooldown that stops handle churn.
const (
	usernameMinLength = 2
	usernameMaxLength = 20
	// UsernameChangeCooldown is the one knob worth tuning: shorten it to make
	// renames cheap, raise it to make impersonation-by-recycled-handle harder.
	UsernameChangeCooldown = 30 * 24 * time.Hour
)

// usernamePattern mirrors sanitizeOIDCUsername's alphabet so a handle derived
// from a provider profile is always one a person could also have typed here.
var usernamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{1,19}$`)

// reservedUsernames are handles the product routes on, signs mail as, or would
// let one account impersonate an operator with. Compared case-insensitively.
var reservedUsernames = map[string]struct{}{
	"about": {}, "abuse": {}, "admin": {}, "administrator": {}, "api": {},
	"auth": {}, "billing": {}, "console": {}, "dashboard": {}, "docs": {},
	"help": {}, "lazu": {}, "legal": {}, "login": {}, "logout": {},
	"mail": {}, "me": {}, "official": {}, "operator": {}, "owner": {},
	"privacy": {}, "register": {}, "root": {}, "security": {}, "settings": {},
	"signin": {}, "signup": {}, "staff": {}, "status": {}, "support": {},
	"system": {}, "team": {}, "terms": {}, "user": {}, "www": {},
}

// usernameVerdict is the single answer both the live check and the write path
// read, so what the field tells someone can never disagree with what saving
// does. Status is a stable machine token; Code is the error catalog code a
// save would fail with, which the console renders.
type usernameVerdict struct {
	// Available is true only when a save would be accepted right now.
	Available bool `json:"available"`
	// Status is one of: available, unchanged, too_short, too_long,
	// invalid_format, reserved, taken, cooldown.
	Status string `json:"status"`
	// Code is set when a save would be refused: username_invalid,
	// username_unavailable or username_change_cooldown.
	Code types.ErrorCode `json:"code,omitempty"`
	// NextChangeAt is a Unix timestamp, set only when Status is cooldown.
	NextChangeAt int64 `json:"next_change_at,omitempty"`
	// Normalized is the handle that would actually be stored.
	Normalized string `json:"normalized"`
	// CooldownDays publishes the policy so the UI never states a different
	// waiting period than the one enforced here.
	CooldownDays int `json:"cooldown_days"`
}

func usernameVerdictOf(status, normalized string) usernameVerdict {
	verdict := usernameVerdict{
		Available:    status == "available",
		Status:       status,
		Normalized:   normalized,
		CooldownDays: int(UsernameChangeCooldown / (24 * time.Hour)),
	}
	switch status {
	case "available", "unchanged":
	case "cooldown":
		verdict.Code = types.ErrorCodeUsernameChangeCooldown
	case "reserved", "taken":
		verdict.Code = types.ErrorCodeUsernameUnavailable
	default:
		verdict.Code = types.ErrorCodeUsernameInvalid
	}
	return verdict
}

// err is the failure a save answers with when the verdict refuses it. The
// details carry what the console needs to explain it.
func (verdict usernameVerdict) err() error {
	switch verdict.Code {
	case types.ErrorCodeUsernameChangeCooldown:
		return types.Err(verdict.Code, types.D{
			"next_change_at": verdict.NextChangeAt,
			"cooldown_days":  verdict.CooldownDays,
		})
	case types.ErrorCodeUsernameUnavailable:
		return types.Err(verdict.Code, types.D{"reason": verdict.Status})
	case types.ErrorCodeUsernameInvalid:
		return types.Err(verdict.Code, types.D{"rule": verdict.Status})
	}
	return types.Err(types.ErrorCodeInvalidRequest, types.D{"field": "username"})
}

// usernameChangeAllowedAt reports when this account may next rename, and
// whether a cooldown is in force at all. A user who has never renamed — every
// account created before this column existed included — is free to do so now.
func usernameChangeAllowedAt(user *model.User) (time.Time, bool) {
	if user == nil || user.UsernameChangedAt <= 0 {
		return time.Time{}, false
	}
	next := time.Unix(user.UsernameChangedAt, 0).Add(UsernameChangeCooldown)
	if !time.Now().Before(next) {
		return time.Time{}, false
	}
	return next, true
}

// evaluateUsernameChange decides whether user may take candidate. Passing a
// transaction lets the write path re-run the same decision under the same lock
// it commits in, which is what keeps the live check honest under a race.
func evaluateUsernameChange(db *gorm.DB, user *model.User, candidate string) (usernameVerdict, error) {
	if db == nil {
		return usernameVerdict{}, errors.New("database is not initialized")
	}
	if user == nil || user.Id <= 0 {
		return usernameVerdict{}, errors.New("user is required")
	}
	normalized := strings.TrimSpace(candidate)

	// An unchanged handle is neither an error nor an offer of availability:
	// the field simply has nothing to do.
	if normalized == user.Username {
		return usernameVerdictOf("unchanged", normalized), nil
	}
	switch {
	case len([]rune(normalized)) < usernameMinLength:
		return usernameVerdictOf("too_short", normalized), nil
	case len([]rune(normalized)) > usernameMaxLength:
		return usernameVerdictOf("too_long", normalized), nil
	case !usernamePattern.MatchString(normalized):
		return usernameVerdictOf("invalid_format", normalized), nil
	}
	if _, reserved := reservedUsernames[strings.ToLower(normalized)]; reserved {
		return usernameVerdictOf("reserved", normalized), nil
	}
	// Cooldown is checked before the availability query so a locked-out account
	// cannot use this endpoint to probe which handles are free.
	if next, locked := usernameChangeAllowedAt(user); locked {
		verdict := usernameVerdictOf("cooldown", normalized)
		verdict.NextChangeAt = next.Unix()
		return verdict, nil
	}
	taken, err := usernameTaken(db, normalized, user.Id)
	if err != nil {
		return usernameVerdict{}, err
	}
	if taken {
		return usernameVerdictOf("taken", normalized), nil
	}
	return usernameVerdictOf("available", normalized), nil
}

// usernameTaken looks through soft deletes on purpose: a deleted account's row
// still holds the unique index, so reporting its handle as free would promise
// a save that the database then refuses.
func usernameTaken(db *gorm.DB, username string, excludeUserID int) (bool, error) {
	var count int64
	query := db.Unscoped().Model(&model.User{}).Where("username = ?", username)
	if excludeUserID > 0 {
		query = query.Where("id <> ?", excludeUserID)
	}
	if err := query.Limit(1).Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

// withUsernameChangeLock serializes a rename against both handle-allocation
// paths. External sign-in reserves under oauth-username: and password
// registration under password-username:; a rename races both, so it takes each.
func withUsernameChangeLock(db *gorm.DB, username string, fn func(*gorm.DB) error) error {
	normalized := strings.ToLower(strings.TrimSpace(username))
	return withAccountResolutionLocks(db, []string{
		externalUsernameAllocationLockKey(username),
		"password-username:" + normalized,
	}, fn)
}

// CheckSelfUsername answers whether the signed-in account could rename itself
// to the requested handle, so the settings field can say so before saving.
//
// This is an existence oracle by nature. It is behind UserAuth and rate
// limiting, and it never reveals anything about who holds a taken handle.
func CheckSelfUsername(c *gin.Context) {
	userID := c.GetInt("id")
	user, err := model.GetUserById(userID, false)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	verdict, err := evaluateUsernameChange(model.CoreDB(), user, c.Query("username"))
	if err != nil {
		httperr.Write(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": verdict})
}

// applySelfProfileUpdate validates every requested credential change before it
// writes any of them, then commits the username, password and display name in
// one transaction. A failed original-password check therefore cannot leave a
// successful rename (and its cooldown) behind.
func applySelfProfileUpdate(
	userID int,
	candidate string,
	displayName string,
	newPassword string,
	originalPassword string,
) (*model.User, usernameVerdict, error) {
	var hashedPassword string
	var err error
	if newPassword != "" {
		hashedPassword, err = common.Password2Hash(newPassword)
		if err != nil {
			return nil, usernameVerdict{}, err
		}
	}

	var verdict usernameVerdict
	var updated model.User
	wrote := false
	apply := func(tx *gorm.DB) error {
		var current model.User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&current, userID).Error; err != nil {
			return err
		}

		updates := map[string]any{}
		if strings.TrimSpace(candidate) != "" {
			decided, err := evaluateUsernameChange(tx, &current, candidate)
			if err != nil {
				return err
			}
			verdict = decided
			if !decided.Available && decided.Status != "unchanged" {
				updated = current
				return nil
			}
			if decided.Available {
				updates["username"] = decided.Normalized
				updates["username_changed_at"] = time.Now().Unix()
			}
		}

		if newPassword != "" {
			if !current.NeedsPassword && !common.ValidatePasswordAndHash(originalPassword, current.Password) {
				return types.Err(types.ErrorCodeOriginalPasswordIncorrect)
			}
			updates["password"] = hashedPassword
			updates["needs_password"] = false
			updates["session_version"] = gorm.Expr("session_version + ?", 1)
		}
		if strings.TrimSpace(displayName) != "" {
			updates["display_name"] = displayName
		}
		if len(updates) == 0 {
			updated = current
			return nil
		}

		result := tx.Model(&model.User{}).Where("id = ?", userID).Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errors.New("profile update affected no row")
		}
		wrote = true
		return tx.Omit("password", "access_token").First(&updated, userID).Error
	}

	if strings.TrimSpace(candidate) != "" {
		err = withUsernameChangeLock(model.CoreDB(), candidate, apply)
	} else if model.CoreDB() == nil {
		err = errors.New("database is not initialized")
	} else {
		err = model.CoreDB().Transaction(apply)
	}
	if err != nil {
		return nil, usernameVerdict{}, err
	}
	if wrote {
		if err := model.InvalidateUserCache(userID); err != nil {
			common.SysLog(fmt.Sprintf("failed to invalidate user cache after profile change for user %d: %v", userID, err))
		}
	}
	return &updated, verdict, nil
}

// applyUsernameChange keeps the live-check tests and single-field call sites
// on the same transaction path as the full profile update.
func applyUsernameChange(userID int, candidate string) (usernameVerdict, error) {
	_, verdict, err := applySelfProfileUpdate(userID, candidate, "", "", "")
	return verdict, err
}

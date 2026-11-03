package model

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// AccountDeletion is the permanent record of one account deletion: who the
// account was when it was deleted, who deleted it, and what became of it. The
// users row itself is kept too, so the account's ledger, logs and signup
// attribution keep pointing at a real row; only its username, address and
// linked identities are released when the restore window ends.
//
// A deletion that is restored keeps its row with RestoredAt set; deleting the
// account again writes a new one.
type AccountDeletion struct {
	ID          int    `json:"id" gorm:"primaryKey"`
	UserID      int    `json:"user_id" gorm:"not null;index"`
	Username    string `json:"username" gorm:"size:64;not null"`
	Email       string `json:"email" gorm:"size:255;not null;default:''"`
	DisplayName string `json:"display_name" gorm:"size:64;not null;default:''"`
	Role        int    `json:"role" gorm:"not null;default:0"`
	// DeletedBy is "self", "admin", or "unknown" for a deletion made before
	// records were kept.
	DeletedBy string `json:"deleted_by" gorm:"size:16;not null"`
	// Identities is the linked sign-in methods at deletion time, as a JSON
	// list of {provider, subject, email}.
	Identities string `json:"-" gorm:"type:text"`
	// BalanceMicrousd is the balance when the account was deleted.
	BalanceMicrousd int64 `json:"balance_microusd" gorm:"not null;default:0"`
	DeletedAt       int64 `json:"deleted_at" gorm:"not null;index"`
	RestoredAt      int64 `json:"restored_at" gorm:"not null;default:0"`
	// RestoredBy is "sign_in" or "admin".
	RestoredBy string `json:"restored_by" gorm:"size:16;not null;default:''"`
	// PurgedAt is when the restore window ended and the account was cleared:
	// username, address and identities released, keys removed, balance
	// written off (WrittenOffMicrousd, through a ledger entry).
	PurgedAt           int64 `json:"purged_at" gorm:"not null;default:0;index"`
	WrittenOffMicrousd int64 `json:"written_off_microusd" gorm:"not null;default:0"`
}

type accountDeletionIdentity struct {
	Provider string `json:"provider"`
	Subject  string `json:"subject"`
	Email    string `json:"email,omitempty"`
}

const (
	AccountDeletedBySelf  = "self"
	AccountDeletedByAdmin = "admin"
	// AccountDeletedByUnknown marks a deletion made before records were kept.
	AccountDeletedByUnknown = "unknown"
)

// ReleasedIdentitySubjectPrefix starts the subject a released identity row is
// left with.
const ReleasedIdentitySubjectPrefix = "released:"

// recordAccountDeletionTx writes the deletion record in the transaction that
// soft-deletes the account. user is the row as it was locked, before the
// delete.
func recordAccountDeletionTx(tx *gorm.DB, user *User, deletedBy string, now time.Time) error {
	if !tx.Migrator().HasTable(&AccountDeletion{}) {
		return nil
	}
	var identities []UserIdentity
	if tx.Migrator().HasTable(&UserIdentity{}) {
		if err := tx.Where("user_id = ?", user.Id).Order("id").Find(&identities).Error; err != nil {
			return err
		}
	}
	snapshot := make([]accountDeletionIdentity, 0, len(identities))
	for _, identity := range identities {
		snapshot = append(snapshot, accountDeletionIdentity{Provider: identity.Provider, Subject: identity.Subject, Email: identity.Email})
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	return tx.Create(&AccountDeletion{
		UserID:          user.Id,
		Username:        user.Username,
		Email:           user.Email,
		DisplayName:     user.DisplayName,
		Role:            user.Role,
		DeletedBy:       deletedBy,
		Identities:      string(encoded),
		BalanceMicrousd: int64(user.Quota),
		DeletedAt:       now.Unix(),
	}).Error
}

// markAccountRestoredTx closes the open deletion record of a restored account.
func markAccountRestoredTx(tx *gorm.DB, userID int, source RestoreSource, now time.Time) error {
	if !tx.Migrator().HasTable(&AccountDeletion{}) {
		return nil
	}
	by := "sign_in"
	if source == RestoreByAdmin {
		by = "admin"
	}
	return tx.Model(&AccountDeletion{}).
		Where("user_id = ? AND restored_at = 0 AND purged_at = 0", userID).
		Updates(map[string]interface{}{"restored_at": now.Unix(), "restored_by": by}).Error
}

// LatestAccountDeletion returns the newest deletion record of an account, or
// nil when it has none.
func LatestAccountDeletion(userID int) (*AccountDeletion, error) {
	if CoreDB() == nil || !CoreDB().Migrator().HasTable(&AccountDeletion{}) {
		return nil, nil
	}
	var record AccountDeletion
	err := CoreDB().Where("user_id = ?", userID).Order("id DESC").First(&record).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &record, nil
}

// PurgedUsernamePrefix starts the username a cleared account is left with.
const PurgedUsernamePrefix = "deleted_"

// AccountPurged reports whether a deleted account has already been cleared.
func AccountPurged(user *User) bool {
	return user != nil && user.DeletedAt.Valid && user.Username == purgedUsername(user.Id)
}

func purgedUsername(userID int) string {
	return PurgedUsernamePrefix + strconv.Itoa(userID)
}

// PurgeExpiredDeletedAccounts clears every deleted account whose restore
// window ended before now. Each account is cleared in its own transaction; a
// failure is reported and the rest go on. It returns how many were cleared.
func PurgeExpiredDeletedAccounts(now time.Time) (int, error) {
	if CoreDB() == nil {
		return 0, nil
	}
	cutoff := now.Add(-AccountRestoreWindow)
	var ids []int
	if err := CoreDB().Unscoped().Model(&User{}).
		Where("deleted_at IS NOT NULL AND deleted_at < ? AND username <> "+purgedUsernameExpr(CoreDB()), cutoff).
		Order("id").Limit(500).Pluck("id", &ids).Error; err != nil {
		return 0, err
	}
	purged := 0
	var failures []error
	for _, id := range ids {
		changed, err := PurgeDeletedAccount(id, now)
		if err != nil {
			failures = append(failures, fmt.Errorf("user %d: %w", id, err))
			continue
		}
		if changed {
			purged++
		}
	}
	return purged, errors.Join(failures...)
}

// purgedUsernameExpr is the SQL for purgedUsername(id) on the row being
// filtered, so cleared accounts are not picked again.
func purgedUsernameExpr(db *gorm.DB) string {
	if db.Dialector.Name() == "mysql" {
		return "CONCAT('" + PurgedUsernamePrefix + "', id)"
	}
	return "('" + PurgedUsernamePrefix + "' || CAST(id AS TEXT))"
}

// PurgeDeletedAccount clears one deleted account whose restore window has
// ended. What it keeps: the users row (with its id, role, timestamps and
// ledger), the deletion record, signup attribution and logs. What it
// releases: the username and address, now free for a new account, and every
// linked identity, renamed so the same Google, GitHub, OIDC or Telegram
// account can sign up again. What it removes: the API keys and the session
// transcripts, and the balance, written off by a ledger entry.
func PurgeDeletedAccount(userID int, now time.Time) (bool, error) {
	var digests []string
	changed := false
	err := CoreDB().Transaction(func(tx *gorm.DB) error {
		var user User
		if err := tx.Unscoped().Omit("password", "access_token").
			Clauses(clause.Locking{Strength: "UPDATE"}).
			First(&user, userID).Error; err != nil {
			return userLookupErr(err)
		}
		if !user.DeletedAt.Valid || AccountPurged(&user) || DeletedAccountRestorable(&user, now) {
			return nil
		}
		record, err := openAccountDeletionTx(tx, &user)
		if err != nil {
			return err
		}

		// Keys go first: nothing may spend the balance being written off.
		var tokens []Token
		if err := tx.Unscoped().Clauses(clause.Locking{Strength: "UPDATE"}).
			Select("id", commonKeyCol, "key_digest").
			Where("user_id = ?", userID).Find(&tokens).Error; err != nil {
			return err
		}
		tokenIDs := make([]int, 0, len(tokens))
		for index := range tokens {
			digests = append(digests, tokens[index].CacheDigest())
			tokenIDs = append(tokenIDs, tokens[index].Id)
		}
		if len(tokenIDs) > 0 && tx.Migrator().HasTable(&TokenCredential{}) {
			if err := tx.Where("token_id IN ?", tokenIDs).Delete(&TokenCredential{}).Error; err != nil {
				return err
			}
		}
		if err := tx.Unscoped().Where("user_id = ?", userID).Delete(&Token{}).Error; err != nil {
			return err
		}

		writtenOff := int64(user.Quota)
		if writtenOff != 0 {
			if err := adjustUserQuotaWithLedgerTx(tx, AdjustUserQuotaWithLedgerInput{
				UserID:             userID,
				DeltaQuota:         -user.Quota,
				Kind:               WalletLedgerKindAdjustment,
				RefType:            "account_purge",
				RefID:              strconv.Itoa(record.ID),
				PricingVersion:     defaultPricingSeedVersion,
				Metadata:           map[string]any{"reason": "deleted account cleared after its restore window"},
				AllowNegative:      true,
				IncludeDeletedUser: true,
			}); err != nil {
				return err
			}
		}

		if tx.Migrator().HasTable(&UserIdentity{}) {
			var identities []UserIdentity
			if err := tx.Where("user_id = ? AND released_at IS NULL", userID).Find(&identities).Error; err != nil {
				return err
			}
			// The subject is kept in the deletion record; the row keeps a
			// placeholder so the provider account can be linked again.
			for _, identity := range identities {
				if err := tx.Model(&UserIdentity{}).Where("id = ?", identity.ID).Updates(map[string]interface{}{
					"subject":     ReleasedIdentitySubjectPrefix + strconv.FormatUint(uint64(identity.ID), 10),
					"email":       "",
					"released_at": now,
				}).Error; err != nil {
					return err
				}
			}
		}

		result := tx.Unscoped().Model(&User{}).Where("id = ? AND deleted_at IS NOT NULL", userID).Updates(map[string]interface{}{
			"username":        purgedUsername(userID),
			"email":           "",
			"verified_email":  "",
			"display_name":    "",
			"access_token":    nil,
			"session_version": gorm.Expr("session_version + ?", 1),
		})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errUserRowNotChanged("purge")
		}

		if err := runUserDeletingHooks(tx, userID); err != nil {
			return err
		}
		if CoreDB() == TelemetryDB() {
			if err := deleteUserSessionContentTx(tx, userID); err != nil {
				return err
			}
		} else if err := tx.Create(&TelemetryCleanupJob{UserID: userID}).Error; err != nil {
			return err
		}

		if err := tx.Model(&AccountDeletion{}).Where("id = ?", record.ID).Updates(map[string]interface{}{
			"purged_at":            now.Unix(),
			"written_off_microusd": writtenOff,
		}).Error; err != nil {
			return err
		}
		changed = true
		return nil
	})
	if err != nil || !changed {
		return false, err
	}
	if err := errors.Join(cacheInvalidateTokenSnapshotsWithFence(digests), invalidateUserCache(userID)); err != nil {
		common.SysWarn(fmt.Sprintf("cleared deleted account %d but cache invalidation failed: %v", userID, err))
	}
	return true, nil
}

// openAccountDeletionTx returns the deletion record the purge closes. An
// account deleted before records were kept gets one now, from the row as it
// stands.
func openAccountDeletionTx(tx *gorm.DB, user *User) (*AccountDeletion, error) {
	var record AccountDeletion
	err := tx.Where("user_id = ? AND restored_at = 0 AND purged_at = 0", user.Id).Order("id DESC").First(&record).Error
	if err == nil {
		return &record, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	if err := recordAccountDeletionTx(tx, user, AccountDeletedByUnknown, user.DeletedAt.Time); err != nil {
		return nil, err
	}
	if err := tx.Where("user_id = ? AND restored_at = 0 AND purged_at = 0", user.Id).Order("id DESC").First(&record).Error; err != nil {
		return nil, err
	}
	return &record, nil
}

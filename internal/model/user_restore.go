package model

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/types"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// AccountRestoreWindow is how long a deleted account can come back by its
// owner signing in again. Past it, every door answers account_deleted.
const AccountRestoreWindow = 30 * 24 * time.Hour

// AccountRestoreDeadline is the last moment a deletion made at deletedAt can
// still be undone by signing in.
func AccountRestoreDeadline(deletedAt time.Time) time.Time {
	return deletedAt.Add(AccountRestoreWindow)
}

// DeletedAccountRestorable reports whether a deleted account is still inside
// its restore window at now. A live account is not "restorable".
func DeletedAccountRestorable(user *User, now time.Time) bool {
	if user == nil || !user.DeletedAt.Valid {
		return false
	}
	return now.Before(AccountRestoreDeadline(user.DeletedAt.Time))
}

// RestoreSource says who brought an account back. The owner signing in is held
// to the restore window; an administrator is not.
type RestoreSource int

const (
	RestoreBySignIn RestoreSource = iota
	RestoreByAdmin
)

// RestoreDeletedUser undoes a soft delete inside its own transaction. changed
// is false when the account was already live, for instance because another
// sign-in restored it a moment earlier; nothing is logged or invalidated then.
func RestoreDeletedUser(userID int, source RestoreSource) (user *User, changed bool, err error) {
	var restored *User
	err = CoreDB().Transaction(func(tx *gorm.DB) error {
		var err error
		restored, changed, err = RestoreDeletedUserTx(tx, userID, source)
		return err
	})
	if err != nil {
		return nil, false, err
	}
	if !changed {
		return restored, false, nil
	}
	InvalidateRestoredUserCaches(restored.Id)
	by := "登录"
	if source == RestoreByAdmin {
		by = "管理员"
	}
	RecordLog(restored.Id, LogTypeSystem, "账号已恢复（"+by+"），API Key 已停用，需要时请重新启用")
	return restored, true, nil
}

// RestoreDeletedUserTx undoes a soft delete within the caller's transaction,
// so a sign-in door that already holds its resolution locks restores in the
// same commit that signs the person in. The caller invalidates caches after
// the commit (InvalidateRestoredUserCaches).
//
// What comes back:
//   - the account itself, with a new session version, so a session cookie
//     issued before the deletion stays dead;
//   - its membership in its own project. Memberships in other people's
//     projects stay removed: those owners saw the person leave, and invite
//     again if they want them back;
//   - its keys, but disabled. A key that worked before the deletion does not
//     start working again without the owner turning it back on.
//
// Balance, ledger, identities and history were never removed by the soft
// delete, so there is nothing else to put back.
func RestoreDeletedUserTx(tx *gorm.DB, userID int, source RestoreSource) (*User, bool, error) {
	if userID <= 0 {
		return nil, false, ErrUserNotFound
	}
	var user User
	if err := tx.Unscoped().Clauses(clause.Locking{Strength: "UPDATE"}).First(&user, userID).Error; err != nil {
		return nil, false, userLookupErr(err)
	}
	if !user.DeletedAt.Valid {
		// Another door restored it first; the account is live either way.
		return &user, false, nil
	}
	if source == RestoreBySignIn && !DeletedAccountRestorable(&user, time.Now()) {
		return nil, false, ErrAccountDeleted
	}
	// A cleared account gave up its username, address and identities, and
	// its balance was written off; there is nothing left to restore.
	if AccountPurged(&user) {
		return nil, false, ErrAccountDeleted
	}
	if err := ensureRestoredIdentityFree(tx, &user); err != nil {
		return nil, false, err
	}
	result := tx.Unscoped().Model(&User{}).Where("id = ? AND deleted_at IS NOT NULL", userID).Updates(map[string]interface{}{
		"deleted_at":      nil,
		"session_version": gorm.Expr("session_version + ?", 1),
	})
	if result.Error != nil {
		return nil, false, result.Error
	}
	if result.RowsAffected != 1 {
		return nil, false, errUserRowNotChanged("restore")
	}
	if err := tx.Model(&Token{}).Where("user_id = ? AND status = ?", userID, common.TokenStatusEnabled).
		Update("status", common.TokenStatusDisabled).Error; err != nil {
		return nil, false, err
	}
	if err := markAccountRestoredTx(tx, userID, source, time.Now()); err != nil {
		return nil, false, err
	}
	if tx.Migrator().HasTable(&Project{}) {
		own := tx.Model(&Project{}).Select("id").Where("owner_user_id = ?", userID)
		if err := tx.Model(&ProjectMember{}).
			Where("user_id = ? AND status = ? AND project_id IN (?)", userID, ProjectMemberRemoved, own).
			Updates(map[string]interface{}{
				"status":               ProjectMemberActive,
				"removed_at":           nil,
				"removal_operation_id": "",
				"version":              gorm.Expr("version + 1"),
			}).Error; err != nil {
			return nil, false, err
		}
	}
	// A fresh value: scanning into the locked copy would keep its deleted_at.
	var restored User
	if err := tx.First(&restored, userID).Error; err != nil {
		return nil, false, userLookupErr(err)
	}
	return &restored, true, nil
}

// ensureRestoredIdentityFree refuses a restore that would give two live
// accounts one username or address. Registration already treats a deleted
// account's username and address as taken, so this only trips on rows written
// around that rule (an import, a hand edit).
func ensureRestoredIdentityFree(tx *gorm.DB, user *User) error {
	var count int64
	query := tx.Model(&User{}).Where("id <> ?", user.Id)
	if user.Email != "" {
		query = query.Where("username = ? OR LOWER(email) = LOWER(?)", user.Username, user.Email)
	} else {
		query = query.Where("username = ?", user.Username)
	}
	if err := query.Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return errRestoreIdentityTaken
	}
	return nil
}

var errRestoreIdentityTaken = types.Err(types.ErrorCodeUserExists)

// InvalidateRestoredUserCaches drops what was cached about the account while
// it was deleted. A failure is logged: the restore has already committed, and
// the caches expire on their own.
func InvalidateRestoredUserCaches(userID int) {
	if err := errors.Join(invalidateUserCache(userID), InvalidateUserTokensCache(userID)); err != nil {
		common.SysWarn(fmt.Sprintf("restored user %d but cache invalidation failed: %v", userID, err))
	}
}

// DeletedAccountByEmail returns the deleted account that alone holds the
// address. It returns nil when a live account holds it, when none does, and
// when more than one deleted account does: only an unambiguous owner can be
// restored or told about its deletion.
func DeletedAccountByEmail(email string) *User {
	email = strings.TrimSpace(email)
	if CoreDB() == nil || email == "" {
		return nil
	}
	var live int64
	if err := CoreDB().Model(&User{}).Where("LOWER(email) = LOWER(?)", email).Count(&live).Error; err != nil || live > 0 {
		return nil
	}
	var deleted []User
	if err := CoreDB().Unscoped().Omit("password").Where("LOWER(email) = LOWER(?) AND deleted_at IS NOT NULL", email).Limit(2).Find(&deleted).Error; err != nil || len(deleted) != 1 {
		return nil
	}
	return &deleted[0]
}

// DeletedAccountForSignIn returns the deleted account a sign-in would reach,
// in the order the sign-in resolvers look: the account the external identity
// (provider and subject, as stored) is linked to, and only when no identity
// matches, the account that alone holds the verified address. It returns nil
// when the sign-in reaches a live account or none.
func DeletedAccountForSignIn(provider string, subject string, verifiedEmail string) *User {
	if CoreDB() == nil {
		return nil
	}
	if provider != "" && subject != "" {
		var identity UserIdentity
		err := CoreDB().Where("provider = ? AND subject = ?", provider, subject).First(&identity).Error
		if err == nil {
			var owner User
			if err := CoreDB().Unscoped().Omit("password").Where("id = ? AND deleted_at IS NOT NULL", identity.UserID).First(&owner).Error; err != nil {
				return nil
			}
			return &owner
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
	}
	return DeletedAccountByEmail(verifiedEmail)
}

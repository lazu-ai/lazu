package controller

import (
	"fmt"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
)

// A deleted account can come back for model.AccountRestoreWindow: signing in
// again, through any door that proves the person owns it, restores it. Past
// the window every door answers account_deleted.

// restoreDeletedAccountForSignIn restores the deleted account the request has
// just proved it owns, and tells the owner by mail.
func restoreDeletedAccountForSignIn(c *gin.Context, deleted *model.User) (*model.User, error) {
	return restoreDeletedAccount(deleted, requestMailLang(c))
}

func restoreDeletedAccount(deleted *model.User, lang string) (*model.User, error) {
	restored, changed, err := model.RestoreDeletedUser(deleted.Id, model.RestoreBySignIn)
	if err != nil || !changed {
		return restored, err
	}
	common.SysWarn(fmt.Sprintf("restored deleted account %d at sign-in", restored.Id))
	sendAccountRestoredEmail(restored, lang)
	return restored, nil
}

// restoreDeletedOwnerBeforeSignIn runs ahead of a door's own resolution. When
// the proven identity or address belongs to a deleted account inside its
// window, the account is restored first, so the resolution that follows finds
// it live and signs it in the usual way. Outside the window it does nothing,
// and the resolution answers account_deleted.
func restoreDeletedOwnerBeforeSignIn(provider string, subject string, verifiedEmail string, lang string) error {
	owner := model.DeletedAccountForSignIn(provider, subject, strings.TrimSpace(verifiedEmail))
	if owner == nil || !model.DeletedAccountRestorable(owner, time.Now()) {
		return nil
	}
	_, err := restoreDeletedAccount(owner, lang)
	return err
}

// accountRestoreDeadline is when a deleted address stops being restorable, or
// the zero time when it is past that or not deleted.
func accountRestoreDeadline(deleted *model.User) time.Time {
	if !model.DeletedAccountRestorable(deleted, time.Now()) {
		return time.Time{}
	}
	return model.AccountRestoreDeadline(deleted.DeletedAt.Time)
}

func sendAccountRestoredEmail(user *model.User, lang string) {
	if user == nil || strings.TrimSpace(user.Email) == "" {
		return
	}
	subject, body := authAccountRestoredMail(lang, user.Username, time.Now())
	sendAuthEmailAsync(subject, user.Email, body)
}

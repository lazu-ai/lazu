package model

import (
	"sync"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"gorm.io/gorm"
)

// Where a users.created_at value came from. Only "exact" rows were stamped at
// registration; the rest were inferred by the schema-15 backfill and are
// shown in the console but left out of growth metrics.
const (
	UserCreatedAtSourceExact = "exact"
	// terms_agreed_at is written by every sign-up path since 2026-08-27.
	UserCreatedAtSourceTerms = "terms"
	// The user_signup wallet ledger row is written in the account's creation
	// transaction whenever a sign-up bonus is configured.
	UserCreatedAtSourceSignupLedger = "signup_ledger"
	// A social or OIDC identity row created in the same transaction.
	UserCreatedAtSourceIdentity = "identity"
	// The earliest activity we can find (API key, top-up, ledger, request).
	// Registration happened no later than this.
	UserCreatedAtSourceEarliestActivity = "earliest_activity"
	// No evidence at all: bounded by neighbouring ids, which are issued in
	// registration order.
	UserCreatedAtSourceInterpolated = "interpolated"
)

// requestStampInterval bounds how often a user's row is written for request
// activity. last_request_at only has to be right to the minute; writing it on
// every request would put a row lock on the hot path for no reader's benefit.
const requestStampInterval = 60

var lastRequestStamp sync.Map // user id -> unix seconds of the last write

// StampUserRequestActivity records that userID made a request at `at`. It is
// the requester, not the payer: activation and churn are about who uses the
// gateway, even when a project owner pays.
func StampUserRequestActivity(userID int, at int64) {
	if userID <= 0 || CoreDB() == nil {
		return
	}
	if at <= 0 {
		at = time.Now().Unix()
	}
	if previous, ok := lastRequestStamp.Load(userID); ok && at-previous.(int64) < requestStampInterval {
		return
	}
	lastRequestStamp.Store(userID, at)
	err := CoreDB().Model(&User{}).Where("id = ? AND last_request_at < ?", userID, at-requestStampInterval+1).
		Updates(map[string]any{
			"last_request_at":  at,
			"first_request_at": gorm.Expr("CASE WHEN first_request_at = 0 THEN ? ELSE first_request_at END", at),
		}).Error
	if err != nil {
		common.SysLog("failed to stamp user request activity: " + err.Error())
	}
}

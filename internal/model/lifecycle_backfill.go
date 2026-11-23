package model

import (
	"fmt"
	"sort"
	"time"

	"gorm.io/gorm"
)

type userTimeRow struct {
	UserID int   `gorm:"column:user_id"`
	At     int64 `gorm:"column:at"`
}

// foldUserTimes runs one grouped query and folds its rows into the running
// earliest / latest maps.
func foldUserTimes(query *gorm.DB, into map[int]int64, keepEarliest bool) error {
	var rows []userTimeRow
	if err := query.Scan(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		if row.UserID <= 0 || row.At <= 0 {
			continue
		}
		current, ok := into[row.UserID]
		if !ok || (keepEarliest && row.At < current) || (!keepEarliest && row.At > current) {
			into[row.UserID] = row.At
		}
	}
	return nil
}

func unixExpr(db *gorm.DB, column string) string {
	if db.Dialector.Name() == "postgres" {
		return "CAST(EXTRACT(EPOCH FROM " + column + ") AS BIGINT)"
	}
	return "CAST(strftime('%s', " + column + ") AS INTEGER)"
}

type lifecycleUserRow struct {
	ID             int
	CreatedAt      int64
	TermsAgreedAt  int64
	FirstRequestAt int64
	LastRequestAt  int64
	FirstTopupAt   int64
}

type lifecycleEvidence struct {
	at     int64
	source string
}

// backfillUserLifecycle fills created_at (with its source), first/last
// request and first top-up for accounts that predate schema version 15.
func backfillUserLifecycle(db *gorm.DB, logDB *gorm.DB) error {
	if !db.Migrator().HasColumn(&User{}, "created_at") {
		return nil
	}
	var users []lifecycleUserRow
	if err := db.Unscoped().Model(&User{}).
		Select("id", "created_at", "terms_agreed_at", "first_request_at", "last_request_at", "first_topup_at").
		Order("id").Scan(&users).Error; err != nil {
		return err
	}
	needsCreated := false
	for _, user := range users {
		if user.CreatedAt == 0 {
			needsCreated = true
			break
		}
	}

	strong := map[string]map[int]int64{
		UserCreatedAtSourceSignupLedger: {},
		UserCreatedAtSourceIdentity:     {},
	}
	activity := map[int]int64{}
	firstRequest := map[int]int64{}
	lastRequest := map[int]int64{}
	firstTopup := map[int]int64{}

	if needsCreated {
		if db.Migrator().HasTable(&WalletLedger{}) {
			if err := foldUserTimes(db.Model(&WalletLedger{}).
				Select("user_id, MIN("+unixExpr(db, "created_at")+") AS at").
				Where("ref_type IN ?", []string{"user_signup", "admin_user_create"}).
				Group("user_id"), strong[UserCreatedAtSourceSignupLedger], true); err != nil {
				return err
			}
			// Opening balances and the migration sync were written for every
			// existing account at once; they say nothing about registration.
			if err := foldUserTimes(db.Model(&WalletLedger{}).
				Select("user_id, MIN("+unixExpr(db, "created_at")+") AS at").
				Where("ref_type NOT IN ?", []string{"opening_balance", "migration_sync"}).
				Group("user_id"), activity, true); err != nil {
				return err
			}
		}
		if db.Migrator().HasTable(&UserIdentity{}) {
			if err := foldUserTimes(db.Model(&UserIdentity{}).
				Select("user_id, MIN("+unixExpr(db, "created_at")+") AS at").
				Group("user_id"), strong[UserCreatedAtSourceIdentity], true); err != nil {
				return err
			}
		}
		if err := foldUserTimes(db.Model(&Token{}).Unscoped().
			Select("user_id, MIN(created_time) AS at").Where("created_time > 0").
			Group("user_id"), activity, true); err != nil {
			return err
		}
		if err := foldUserTimes(db.Model(&TopUp{}).
			Select("user_id, MIN(create_time) AS at").Where("create_time > 0").
			Group("user_id"), activity, true); err != nil {
			return err
		}
	}

	// Request activity: consume logs (kept 90 days), routing decisions,
	// per-user hourly rollups and ledger spend all outlive one another in
	// different ways, so take the widest view.
	if logDB != nil && logDB.Migrator().HasTable(&Log{}) {
		if err := foldUserTimes(logDB.Model(&Log{}).Select("user_id, MIN(created_at) AS at").
			Where("type = ?", LogTypeConsume).Group("user_id"), firstRequest, true); err != nil {
			return err
		}
		if err := foldUserTimes(logDB.Model(&Log{}).Select("user_id, MAX(created_at) AS at").
			Where("type = ?", LogTypeConsume).Group("user_id"), lastRequest, false); err != nil {
			return err
		}
		if needsCreated {
			if err := foldUserTimes(logDB.Model(&Log{}).Select("user_id, MIN(created_at) AS at").
				Group("user_id"), activity, true); err != nil {
				return err
			}
		}
	}
	if db.Migrator().HasTable(&RoutingDecision{}) {
		if err := foldUserTimes(db.Model(&RoutingDecision{}).
			Select("user_id, MIN("+unixExpr(db, "decision_at")+") AS at").
			Where("user_id IS NOT NULL").Group("user_id"), firstRequest, true); err != nil {
			return err
		}
	}
	if db.Migrator().HasTable(&DashboardUsageHourlyTotal{}) {
		if err := foldUserTimes(db.Model(&DashboardUsageHourlyTotal{}).
			Select("scope_user_id AS user_id, MIN(bucket_hour) AS at").
			Where("scope_user_id > 0 AND request_count > 0").Group("scope_user_id"), firstRequest, true); err != nil {
			return err
		}
		if err := foldUserTimes(db.Model(&DashboardUsageHourlyTotal{}).
			Select("scope_user_id AS user_id, MAX(bucket_hour) + 3599 AS at").
			Where("scope_user_id > 0 AND request_count > 0").Group("scope_user_id"), lastRequest, false); err != nil {
			return err
		}
	}
	if db.Migrator().HasTable(&WalletLedger{}) {
		if err := foldUserTimes(db.Model(&WalletLedger{}).
			Select("user_id, MIN("+unixExpr(db, "created_at")+") AS at").
			Where("kind = ? AND ref_type LIKE ?", WalletLedgerKindSpend, "relay_%").Group("user_id"), firstRequest, true); err != nil {
			return err
		}
	}
	if err := foldUserTimes(db.Model(&TopUp{}).
		Select("user_id, MIN(complete_time) AS at").
		Where("status = ? AND complete_time > 0", "success").Group("user_id"), firstTopup, true); err != nil {
		return err
	}
	for id, at := range firstRequest {
		if current, ok := activity[id]; !ok || at < current {
			activity[id] = at
		}
	}

	created := map[int]lifecycleEvidence{}
	if needsCreated {
		created = inferUserCreatedAt(users, strong, activity)
	}

	for _, user := range users {
		updates := map[string]any{}
		if user.CreatedAt == 0 {
			if evidence, ok := created[user.ID]; ok {
				updates["created_at"] = evidence.at
				updates["created_at_source"] = evidence.source
			}
		}
		if at := firstRequest[user.ID]; at > 0 && (user.FirstRequestAt == 0 || at < user.FirstRequestAt) {
			updates["first_request_at"] = at
		}
		if at := lastRequest[user.ID]; at > user.LastRequestAt {
			updates["last_request_at"] = at
		}
		if at := firstTopup[user.ID]; at > 0 && (user.FirstTopupAt == 0 || at < user.FirstTopupAt) {
			updates["first_topup_at"] = at
		}
		if len(updates) == 0 {
			continue
		}
		if err := db.Unscoped().Model(&User{}).Where("id = ?", user.ID).Updates(updates).Error; err != nil {
			return fmt.Errorf("user %d: %w", user.ID, err)
		}
	}
	return nil
}

// inferUserCreatedAt picks, for every account without created_at, the
// strongest evidence available. Ids are issued in registration order, so an
// inferred time is never allowed to be later than a later account's exact
// one, and an account with no evidence at all is placed just before the next
// known account.
func inferUserCreatedAt(users []lifecycleUserRow, strong map[string]map[int]int64, activity map[int]int64) map[int]lifecycleEvidence {
	result := map[int]lifecycleEvidence{}
	exact := map[int]int64{}
	for _, user := range users {
		if user.CreatedAt > 0 {
			exact[user.ID] = user.CreatedAt
			continue
		}
		best := lifecycleEvidence{}
		consider := func(at int64, source string) {
			if at > 0 && (best.at == 0 || at < best.at) {
				best = lifecycleEvidence{at: at, source: source}
			}
		}
		consider(strong[UserCreatedAtSourceSignupLedger][user.ID], UserCreatedAtSourceSignupLedger)
		consider(strong[UserCreatedAtSourceIdentity][user.ID], UserCreatedAtSourceIdentity)
		consider(user.TermsAgreedAt, UserCreatedAtSourceTerms)
		strongFound := best.at > 0
		if at := activity[user.ID]; at > 0 && (!strongFound || at < best.at) {
			best = lifecycleEvidence{at: at, source: UserCreatedAtSourceEarliestActivity}
		}
		if best.at > 0 {
			result[user.ID] = best
			if best.source != UserCreatedAtSourceEarliestActivity {
				exact[user.ID] = best.at
			}
		}
	}

	ids := make([]int, 0, len(users))
	for _, user := range users {
		ids = append(ids, user.ID)
	}
	sort.Ints(ids)
	// Walk from the newest account back: nobody registered after a later id.
	ceiling := time.Now().Unix()
	for i := len(ids) - 1; i >= 0; i-- {
		id := ids[i]
		if at, ok := exact[id]; ok {
			if at < ceiling {
				ceiling = at
			}
			continue
		}
		evidence, ok := result[id]
		if !ok {
			result[id] = lifecycleEvidence{at: ceiling, source: UserCreatedAtSourceInterpolated}
			continue
		}
		if evidence.at > ceiling {
			evidence.at = ceiling
			result[id] = evidence
		}
		if evidence.at < ceiling {
			ceiling = evidence.at
		}
	}
	return result
}

// backfillTokenLastUsedAt carries the last consume-log timestamp onto keys
// created before tokens.last_used_at existed.
func backfillTokenLastUsedAt(db *gorm.DB, logDB *gorm.DB) error {
	if logDB == nil || !logDB.Migrator().HasTable(&Log{}) || !db.Migrator().HasColumn(&Token{}, "last_used_at") {
		return nil
	}
	type row struct {
		TokenID int   `gorm:"column:token_id"`
		At      int64 `gorm:"column:at"`
	}
	var rows []row
	if err := logDB.Model(&Log{}).Select("token_id, MAX(created_at) AS at").
		Where("type = ? AND token_id > 0", LogTypeConsume).Group("token_id").Scan(&rows).Error; err != nil {
		return err
	}
	for _, item := range rows {
		if err := db.Unscoped().Model(&Token{}).
			Where("id = ? AND last_used_at < ?", item.TokenID, item.At).
			Update("last_used_at", item.At).Error; err != nil {
			return err
		}
	}
	return nil
}

// backfillLifecycleFromLogs fills user and Key activity from the request
// log when the separate telemetry store is available.
func backfillLifecycleFromLogs(db *gorm.DB, logDB *gorm.DB) error {
	if db == nil {
		return nil
	}
	if err := backfillUserLifecycle(db, logDB); err != nil {
		return fmt.Errorf("backfill user lifecycle: %w", err)
	}
	if err := backfillTokenLastUsedAt(db, logDB); err != nil {
		return fmt.Errorf("backfill token last use: %w", err)
	}
	return nil
}

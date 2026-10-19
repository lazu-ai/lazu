package model

import (
	"testing"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
)

// inferUserCreatedAt picks the strongest evidence per account and never lets
// an inferred time run later than a later account's known one.
func TestInferUserCreatedAtOrdersAndClampsByID(t *testing.T) {
	users := []lifecycleUserRow{
		{ID: 1, CreatedAt: 1000},     // exact already; not in the result
		{ID: 2},                      // activity only, later than id 3 → clamped
		{ID: 3, TermsAgreedAt: 3000}, // terms
		{ID: 4},                      // signup ledger beats a later identity row
		{ID: 5},                      // no evidence → interpolated
		{ID: 6},                      // activity earlier than its identity row
	}
	strong := map[string]map[int]int64{
		UserCreatedAtSourceSignupLedger: {4: 4000},
		UserCreatedAtSourceIdentity:     {4: 4500, 6: 6000},
	}
	activity := map[int]int64{2: 5000, 6: 5500}

	got := inferUserCreatedAt(users, strong, activity)

	if _, ok := got[1]; ok {
		t.Fatalf("an account with an exact created_at must not be re-inferred: %+v", got[1])
	}
	want := map[int]lifecycleEvidence{
		2: {at: 3000, source: UserCreatedAtSourceEarliestActivity},
		3: {at: 3000, source: UserCreatedAtSourceTerms},
		4: {at: 4000, source: UserCreatedAtSourceSignupLedger},
		5: {at: 5500, source: UserCreatedAtSourceInterpolated},
		6: {at: 5500, source: UserCreatedAtSourceEarliestActivity},
	}
	for id, expected := range want {
		if got[id] != expected {
			t.Fatalf("user %d: got %+v, want %+v", id, got[id], expected)
		}
	}
	// Registration order holds: no inferred time is later than a later id's.
	for id := 2; id < 6; id++ {
		if got[id].at > got[id+1].at {
			t.Fatalf("user %d (%d) registered after user %d (%d)", id, got[id].at, id+1, got[id+1].at)
		}
	}
}

func TestInferUserCreatedAtInterpolatesNewestAccountToNow(t *testing.T) {
	before := time.Now().Unix()
	got := inferUserCreatedAt([]lifecycleUserRow{{ID: 1, CreatedAt: 100}, {ID: 2}}, map[string]map[int]int64{}, map[int]int64{})
	if got[2].source != UserCreatedAtSourceInterpolated || got[2].at < before {
		t.Fatalf("newest account without evidence should be bounded by now, got %+v", got[2])
	}
}

func TestBackfillUserLifecycleFillsCreatedAtAndActivityOnce(t *testing.T) {
	db := withPricingTestDB(t, "schema_v15_backfill_user_lifecycle")
	if err := db.AutoMigrate(&UserIdentity{}, &DashboardUsageHourlyTotal{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	previousLogDB := telemetryDB
	telemetryDB = db
	t.Cleanup(func() { telemetryDB = previousLogDB })

	users := []User{
		{Id: 1, Username: "exact-user", CreatedAt: 1_700_000_000, CreatedAtSource: UserCreatedAtSourceExact},
		{Id: 2, Username: "ledger-user"},
		{Id: 3, Username: "no-evidence-user"},
		{Id: 4, Username: "terms-user", TermsAgreedAt: 1_700_500_000},
	}
	for i := range users {
		if err := db.Create(&users[i]).Error; err != nil {
			t.Fatalf("seed user %d: %v", users[i].Id, err)
		}
	}
	// autoCreateTime stamped the legacy rows; put them back to "unknown".
	if err := db.Model(&User{}).Where("id IN ?", []int{2, 3, 4}).UpdateColumn("created_at", 0).Error; err != nil {
		t.Fatalf("clear created_at: %v", err)
	}

	if err := db.Create(&WalletLedger{UserID: 2, Kind: "credit", RefType: "user_signup", RefID: "signup-2",
		CreatedAt: time.Unix(1_700_300_000, 0).UTC()}).Error; err != nil {
		t.Fatalf("seed signup ledger: %v", err)
	}
	if err := db.Create(&Token{UserId: 2, Key: "lifecycle-key-2", Name: "k", CreatedTime: 1_700_310_000}).Error; err != nil {
		t.Fatalf("seed token: %v", err)
	}
	if err := db.Create(&TopUp{UserId: 2, TradeNo: "lifecycle-topup-2", Amount: 5, Status: common.TopUpStatusSuccess,
		CreateTime: 1_700_315_000, CompleteTime: 1_700_320_000}).Error; err != nil {
		t.Fatalf("seed topup: %v", err)
	}
	for _, row := range []Log{
		{UserId: 1, Type: LogTypeConsume, CreatedAt: 1_700_100_000},
		{UserId: 1, Type: LogTypeConsume, CreatedAt: 1_700_200_000},
	} {
		if err := db.Create(&row).Error; err != nil {
			t.Fatalf("seed log: %v", err)
		}
	}
	if err := db.Create(&DashboardUsageHourlyTotal{ScopeUserID: 2, BucketHour: 1_700_398_800, RequestCount: 3}).Error; err != nil {
		t.Fatalf("seed rollup: %v", err)
	}

	if err := backfillUserLifecycle(db, telemetryDB); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	load := func() map[int]User {
		var rows []User
		if err := db.Order("id").Find(&rows).Error; err != nil {
			t.Fatalf("reload users: %v", err)
		}
		byID := map[int]User{}
		for _, row := range rows {
			byID[row.Id] = row
		}
		return byID
	}
	got := load()

	if u := got[1]; u.CreatedAt != 1_700_000_000 || u.CreatedAtSource != UserCreatedAtSourceExact ||
		u.FirstRequestAt != 1_700_100_000 || u.LastRequestAt != 1_700_200_000 {
		t.Fatalf("exact user: %+v", u)
	}
	if u := got[2]; u.CreatedAt != 1_700_300_000 || u.CreatedAtSource != UserCreatedAtSourceSignupLedger ||
		u.FirstTopupAt != 1_700_320_000 || u.FirstRequestAt != 1_700_398_800 || u.LastRequestAt != 1_700_398_800+3599 {
		t.Fatalf("ledger user: created=%d source=%q first_topup=%d first_request=%d last_request=%d",
			u.CreatedAt, u.CreatedAtSource, u.FirstTopupAt, u.FirstRequestAt, u.LastRequestAt)
	}
	if u := got[4]; u.CreatedAt != 1_700_500_000 || u.CreatedAtSource != UserCreatedAtSourceTerms {
		t.Fatalf("terms user: created=%d source=%q", u.CreatedAt, u.CreatedAtSource)
	}
	if u := got[3]; u.CreatedAtSource != UserCreatedAtSourceInterpolated || u.CreatedAt != 1_700_500_000 {
		t.Fatalf("no-evidence user should sit just before the next known account: created=%d source=%q", u.CreatedAt, u.CreatedAtSource)
	}

	if err := backfillUserLifecycle(db, telemetryDB); err != nil {
		t.Fatalf("second backfill: %v", err)
	}
	again := load()
	for id, first := range got {
		second := again[id]
		if first.CreatedAt != second.CreatedAt || first.CreatedAtSource != second.CreatedAtSource ||
			first.FirstRequestAt != second.FirstRequestAt || first.LastRequestAt != second.LastRequestAt ||
			first.FirstTopupAt != second.FirstTopupAt {
			t.Fatalf("second run changed user %d: %+v -> %+v", id, first, second)
		}
	}
}

// Raw-subject identity rows are rewritten only when the user's legacy columns
// prove the same issuer and subject; users with columns only gain a row.
func TestMoveLegacyOIDCIdentitiesNamespacesRawRows(t *testing.T) {
	db := withPricingTestDB(t, "schema_v15_move_legacy_oidc")
	if err := db.AutoMigrate(&UserIdentity{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	for _, stmt := range []string{
		"ALTER TABLE users ADD COLUMN oidc_issuer varchar(255) DEFAULT ''",
		"ALTER TABLE users ADD COLUMN oidc_subject varchar(255) DEFAULT ''",
	} {
		if err := db.Exec(stmt).Error; err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	for _, u := range []User{{Id: 1, Username: "raw-row"}, {Id: 2, Username: "columns-only"}, {Id: 3, Username: "foreign-row"}} {
		if err := db.Create(&u).Error; err != nil {
			t.Fatalf("seed user: %v", err)
		}
	}
	db.Exec("UPDATE users SET oidc_issuer = 'https://id.example/', oidc_subject = 'sub-1' WHERE id = 1")
	db.Exec("UPDATE users SET oidc_issuer = 'https://id.example', oidc_subject = 'sub-2' WHERE id = 2")
	db.Exec("UPDATE users SET oidc_issuer = 'https://id.example', oidc_subject = 'other' WHERE id = 3")
	for _, identity := range []UserIdentity{
		{UserID: 1, Provider: "oidc", Subject: "sub-1"},
		{UserID: 3, Provider: "oidc", Subject: "sub-3"},
	} {
		if err := db.Create(&identity).Error; err != nil {
			t.Fatalf("seed identity: %v", err)
		}
	}

	if err := moveLegacyOIDCIdentities(db); err != nil {
		t.Fatalf("move: %v", err)
	}

	subjects := map[int]string{}
	var rows []UserIdentity
	db.Where("provider = ?", "oidc").Find(&rows)
	for _, row := range rows {
		subjects[row.UserID] = row.Subject
	}
	if subjects[1] != legacyOIDCSubject("https://id.example", "sub-1") {
		t.Fatalf("raw row not namespaced: %q", subjects[1])
	}
	if subjects[2] != legacyOIDCSubject("https://id.example", "sub-2") {
		t.Fatalf("columns-only user missing identity: %q", subjects[2])
	}
	if subjects[3] != "sub-3" {
		t.Fatalf("unproven raw row must stay untouched: %q", subjects[3])
	}
}

func TestCopyLegacyStripeCustomersFillsEmptyProjectsOnly(t *testing.T) {
	db := withPricingTestDB(t, "schema_v15_copy_stripe_customers")
	if err := db.AutoMigrate(&Project{}, &ProjectMember{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := db.Exec("ALTER TABLE users ADD COLUMN stripe_customer varchar(64) DEFAULT ''").Error; err != nil {
		t.Fatalf("add column: %v", err)
	}
	for _, u := range []User{{Id: 1, Username: "empty-project"}, {Id: 2, Username: "has-customer"}} {
		if err := db.Create(&u).Error; err != nil {
			t.Fatalf("seed user: %v", err)
		}
	}
	db.Exec("UPDATE users SET stripe_customer = 'cus_legacy_' || id")
	// Creating a user opens its default project; give the second one a customer.
	for _, owner := range []int{1, 2} {
		if err := db.Where(Project{OwnerUserID: owner}).FirstOrCreate(&Project{OwnerUserID: owner, Name: "p"}).Error; err != nil {
			t.Fatalf("seed project: %v", err)
		}
	}
	db.Model(&Project{}).Where("owner_user_id = 2").Update("stripe_customer_id", "cus_kept")

	if err := copyLegacyStripeCustomers(db); err != nil {
		t.Fatalf("copy: %v", err)
	}

	var projects []Project
	db.Order("owner_user_id").Find(&projects)
	if projects[0].StripeCustomerID != "cus_legacy_1" || projects[1].StripeCustomerID != "cus_kept" {
		t.Fatalf("unexpected customers: %q, %q", projects[0].StripeCustomerID, projects[1].StripeCustomerID)
	}
}

// The log-reading backfill runs after the log database opens; given one, it
// stamps Key last use and the user's first and last request from the log.
func TestBackfillLifecycleFromLogsReadsTheLogDatabase(t *testing.T) {
	db := withPricingTestDB(t, "schema_v15_backfill_from_logs")
	if err := db.AutoMigrate(&Log{}, &UserIdentity{}, &DashboardUsageHourlyTotal{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	user := User{Username: "log-backfill-user"}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
	token := Token{UserId: user.Id, Key: "log-backfill-key", Name: "k", CreatedTime: 1_700_000_000}
	if err := db.Create(&token).Error; err != nil {
		t.Fatalf("seed token: %v", err)
	}
	for _, at := range []int64{1_700_100_000, 1_700_200_000} {
		if err := db.Create(&Log{UserId: user.Id, TokenId: token.Id, Type: LogTypeConsume, CreatedAt: at}).Error; err != nil {
			t.Fatalf("seed log: %v", err)
		}
	}

	if err := backfillLifecycleFromLogs(db, db); err != nil {
		t.Fatalf("backfill: %v", err)
	}

	var gotToken Token
	db.First(&gotToken, token.Id)
	if gotToken.LastUsedAt != 1_700_200_000 {
		t.Fatalf("token last_used_at = %d", gotToken.LastUsedAt)
	}
	var gotUser User
	db.First(&gotUser, user.Id)
	if gotUser.FirstRequestAt != 1_700_100_000 || gotUser.LastRequestAt != 1_700_200_000 {
		t.Fatalf("user first/last request = %d / %d", gotUser.FirstRequestAt, gotUser.LastRequestAt)
	}
}

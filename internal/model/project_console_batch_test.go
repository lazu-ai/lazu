package model

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	gormsqlite "github.com/glebarez/sqlite"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/pkg/billing"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type consoleBatchSQLCapture struct {
	logger.Interface
	statements []string
}

func (capture *consoleBatchSQLCapture) Trace(_ context.Context, _ time.Time, sql func() (string, int64), _ error) {
	statement, _ := sql()
	capture.statements = append(capture.statements, statement)
}

func consoleBatchFixture(t *testing.T, postgres bool) (*gorm.DB, *gorm.DB, *consoleBatchSQLCapture, *consoleBatchSQLCapture) {
	t.Helper()
	return consoleBatchFixtureWithMissingEnv(t, postgres, func(message string) { t.Skip(message) })
}

func consoleBatchFixtureWithMissingEnv(t *testing.T, postgres bool, missing func(string)) (*gorm.DB, *gorm.DB, *consoleBatchSQLCapture, *consoleBatchSQLCapture) {
	t.Helper()
	if postgres && (os.Getenv("TEST_GATEWAY_CORE_DSN") == "" || os.Getenv("TEST_GATEWAY_TELEMETRY_DSN") == "") {
		missing("isolated PostgreSQL test DSNs are not configured")
		return nil, nil, nil, nil
	}
	oldCore, oldTelemetry, oldRedis := CoreDB(), ConfiguredTelemetryDB(), common.RedisEnabled
	var core, telemetry *gorm.DB
	if postgres {
		core, telemetry = postgresTelemetryFixture(t)
	} else {
		open := func(kind string) *gorm.DB {
			db, err := gorm.Open(gormsqlite.Open(fmt.Sprintf("file:console_batch_%s_%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"), kind)), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { raw, _ := db.DB(); _ = raw.Close() })
			return db
		}
		core, telemetry = open("core"), open("telemetry")
	}
	t.Cleanup(func() { SetCoreDB(oldCore); SetTelemetryDB(oldTelemetry); common.RedisEnabled = oldRedis })
	common.RedisEnabled = false
	if err := core.AutoMigrate(&User{}, &Project{}, &ProjectMember{}, &Token{}, &SpendBudget{}, &ProjectSpend{}); err != nil {
		t.Fatal(err)
	}
	if err := telemetry.AutoMigrate(&Log{}); err != nil {
		t.Fatal(err)
	}
	if postgres {
		if err := installEffortClampedColumn(telemetry); err != nil {
			t.Fatal(err)
		}
	}
	coreTrace := &consoleBatchSQLCapture{Interface: logger.Default.LogMode(logger.Silent)}
	telemetryTrace := &consoleBatchSQLCapture{Interface: logger.Default.LogMode(logger.Silent)}
	core = core.Session(&gorm.Session{Logger: coreTrace})
	telemetry = telemetry.Session(&gorm.Session{Logger: telemetryTrace})
	SetCoreDB(core)
	SetTelemetryDB(telemetry)
	return core, telemetry, coreTrace, telemetryTrace
}

// Optional-store fixtures must not change process state before discovering
// that their integration environment is absent. Inject the missing-environment
// callback so this control test can assert the branch without emitting an
// intentional SKIP in the native coverage gate. Real fixtures still use t.Skip.
func TestConsoleBatchSkippedPostgresPreservesGlobals(t *testing.T) {
	oldCore, oldTelemetry, oldRedis := CoreDB(), ConfiguredTelemetryDB(), common.RedisEnabled
	t.Cleanup(func() { SetCoreDB(oldCore); SetTelemetryDB(oldTelemetry); common.RedisEnabled = oldRedis })
	SetCoreDB(nil)
	SetTelemetryDB(nil)
	common.RedisEnabled = true
	for _, missing := range []string{"both", "core", "telemetry"} {
		t.Run(missing, func(t *testing.T) {
			coreDSN, telemetryDSN := "not-opened-core", "not-opened-telemetry"
			if missing != "telemetry" {
				coreDSN = ""
			}
			if missing != "core" {
				telemetryDSN = ""
			}
			t.Setenv("TEST_GATEWAY_CORE_DSN", coreDSN)
			t.Setenv("TEST_GATEWAY_TELEMETRY_DSN", telemetryDSN)
			calls := 0
			core, telemetry, coreTrace, telemetryTrace := consoleBatchFixtureWithMissingEnv(t, true, func(message string) {
				calls++
				if !strings.Contains(message, "not configured") {
					t.Fatalf("unexpected missing-environment result: %s", message)
				}
			})
			if calls != 1 || core != nil || telemetry != nil || coreTrace != nil || telemetryTrace != nil {
				t.Fatal("unconfigured optional PostgreSQL fixture did not stop before opening stores")
			}
			if CoreDB() != nil || ConfiguredTelemetryDB() != nil || !common.RedisEnabled {
				t.Fatal("unconfigured PostgreSQL fixture changed the surrounding stores or Redis mode")
			}
		})
	}
}

func createConsoleBatchRows(t *testing.T, db *gorm.DB, value interface{}) {
	t.Helper()
	if err := db.Session(&gorm.Session{SkipHooks: true}).Create(value).Error; err != nil {
		t.Fatal(err)
	}
}

func testConsoleBatchStores(t *testing.T, run func(*testing.T, bool)) {
	t.Helper()
	for _, store := range []struct {
		name     string
		postgres bool
	}{{"sqlite", false}, {"postgres", true}} {
		t.Run(store.name, func(t *testing.T) { run(t, store.postgres) })
	}
}

func TestConsoleProjectLogsBatchQueriesAndHistoricalIdentities(t *testing.T) {
	testConsoleBatchStores(t, func(t *testing.T, postgres bool) {
		core, telemetry, coreTrace, teleTrace := consoleBatchFixture(t, postgres)
		deleted := gorm.DeletedAt{Time: time.Now().UTC(), Valid: true}
		createConsoleBatchRows(t, core, &[]User{{Id: 1, PublicID: "usr_batch_owner", Username: "owner"}, {Id: 2, PublicID: "usr_batch_deleted", Username: "deleted", DeletedAt: deleted}, {Id: 3, PublicID: "usr_batch_member", Username: "member"}})
		createConsoleBatchRows(t, core, &[]Token{{Id: 2, PublicID: "tok_batch_deleted", UserId: 2, Key: common.GenerateRequestID(), DeletedAt: deleted}, {Id: 3, PublicID: "tok_batch_member", UserId: 3, Key: common.GenerateRequestID()}})
		project := Project{ID: 1, PublicID: "proj_batch", OwnerUserID: 1}
		var logs []*Log
		for i := 0; i < 20; i++ {
			logs = append(logs, &Log{UserId: 2 + i%2, TokenId: 2 + i%2, Username: fmt.Sprintf("historic-%d", i), ChannelId: 8, UsageFact: `{"provider_cost_usd":0.4}`, Other: `{"provider_cost_usd":0.4,"customer_cost_usd":0.6}`})
		}
		for _, count := range []int{1, 20} {
			coreTrace.statements, teleTrace.statements = nil, nil
			views, err := ProjectLogViews(project, logs[:count])
			if err != nil || len(views) != count {
				t.Fatalf("page: %v %v", views, err)
			}
			if len(coreTrace.statements) != 2 || len(teleTrace.statements) != 0 {
				t.Fatalf("%d logs query count core=%d telemetry=%d: %v", count, len(coreTrace.statements), len(teleTrace.statements), coreTrace.statements)
			}
			t.Logf("%d logs: identity hydration Core=2, Telemetry=0", count)
			for i, view := range views {
				wantUser, wantToken := "usr_batch_deleted", "tok_batch_deleted"
				if i%2 != 0 {
					wantUser, wantToken = "usr_batch_member", "tok_batch_member"
				}
				if view.PayerPublicID != "usr_batch_owner" || view.Actor == nil || view.Actor.ID != wantUser || view.Actor.Username != logs[i].Username || view.TokenPublicID == nil || *view.TokenPublicID != wantToken {
					t.Fatalf("historical identity %d: %+v", i, view)
				}
				if view.ChannelId != 0 || view.UsageFact != "" || strings.Contains(view.Other, "provider_cost_usd") || !strings.Contains(view.Other, "customer_cost_usd") || logs[i].ChannelId != 8 {
					t.Fatalf("customer redaction/source mutation: %+v", view)
				}
			}
		}
		views, err := ProjectLogViews(project, []*Log{{UserId: 999, TokenId: 999}})
		if err != nil || views[0].Actor == nil || views[0].Actor.ID != "" || views[0].UserPublicID == nil || *views[0].UserPublicID != "" || views[0].TokenPublicID != nil {
			t.Fatalf("missing historical references: %+v %v", views, err)
		}
		if _, err := ProjectLogViews(Project{OwnerUserID: 999}, logs); !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Fatalf("missing payer accepted: %v", err)
		}
		// Hydration does not widen the upstream log authorization scope.
		for i, row := range []Log{{ProjectID: 1, UserId: 3, Type: LogTypeConsume}, {ProjectID: 1, UserId: 2, Type: LogTypeConsume}, {ProjectID: 2, UserId: 3, Type: LogTypeConsume}, {ProjectID: 1, UserId: 3, Type: LogTypeUnknown}} {
			row.PublicID, row.RequestTS = fmt.Sprintf("log_scope_%d", i), int64(i+1)
			createConsoleBatchRows(t, telemetry, &row)
		}
		access, err := NewProjectLogAccess(project, ProjectMember{ProjectID: 1, UserID: 3, Role: ProjectRoleMember, Status: ProjectMemberSuspended})
		if err != nil {
			t.Fatal(err)
		}
		var visible []*Log
		if err := ProjectLogsQuery(access, ProjectLogFilter{}).Find(&visible).Error; err != nil || len(visible) != 1 || visible[0].UserId != 3 || visible[0].ProjectID != 1 {
			t.Fatalf("scope widened: %+v %v", visible, err)
		}
	})
}

func TestConsoleProjectSummariesBatchQueriesAndSuspension(t *testing.T) {
	testConsoleBatchStores(t, func(t *testing.T, postgres bool) {
		core, _, capture, _ := consoleBatchFixture(t, postgres)
		createConsoleBatchRows(t, core, &User{Id: 1, PublicID: "usr_summary_viewer", Username: "viewer", Quota: 500})
		addProject := func(id int) {
			ownerID := 1
			if id != 1 {
				ownerID = 100 + id
				createConsoleBatchRows(t, core, &User{Id: ownerID, PublicID: fmt.Sprintf("usr_summary_%d", id), Username: fmt.Sprintf("owner-%d", id), Quota: billing.MicroUSD(500 + id)})
			}
			project := Project{ID: id, PublicID: fmt.Sprintf("proj_summary_%d", id), OwnerUserID: ownerID, Name: fmt.Sprintf("project-%d", id)}
			createConsoleBatchRows(t, core, &project)
			status := ProjectMemberActive
			if id%3 == 0 {
				status = ProjectMemberSuspended
			}
			createConsoleBatchRows(t, core, &ProjectMember{ProjectID: id, UserID: 1, Role: ProjectRoleMember, Status: status})
			createConsoleBatchRows(t, core, &ProjectMember{ProjectID: id, UserID: 999, Role: ProjectRoleMember, Status: ProjectMemberRemoved})
		}
		addProject(1)
		for _, count := range []int{1, 20} {
			if count == 20 {
				for id := 2; id <= 20; id++ {
					addProject(id)
				}
			}
			capture.statements = nil
			rows, defaultID, err := ListUserProjects(1)
			if err != nil || len(rows) != count || defaultID != "proj_summary_1" {
				t.Fatalf("summaries: %+v %s %v", rows, defaultID, err)
			}
			if len(capture.statements) != 4 {
				t.Fatalf("%d projects queries=%d: %v", count, len(capture.statements), capture.statements)
			}
			t.Logf("%d projects: list Core=4, Telemetry=0", count)
			for i, row := range rows {
				id := i + 1
				if row.ID != fmt.Sprintf("proj_summary_%d", id) || row.Name != fmt.Sprintf("project-%d", id) {
					t.Fatalf("list order changed: %+v", rows)
				}
				if id%3 == 0 {
					if row.BalanceMicrousd != nil || row.MemberCount != 0 || row.Status != ProjectMemberSuspended {
						t.Fatalf("suspended balance leaked: %+v", row)
					}
				} else if row.BalanceMicrousd == nil || row.MemberCount != 1 {
					t.Fatalf("active count includes removed member: %+v", row)
				}
			}
		}
		createConsoleBatchRows(t, core, &ProjectMember{ProjectID: 999, UserID: 2, Role: ProjectRoleMember, Status: ProjectMemberRemoved})
		rows, _, err := ListUserProjects(2)
		if err != nil || len(rows) != 0 || rows == nil {
			t.Fatalf("removed/empty list: %+v %v", rows, err)
		}
		if err := core.Model(&ProjectMember{}).Where("project_id = ? AND user_id = ?", 999, 2).UpdateColumn("status", ProjectMemberActive).Error; err != nil {
			t.Fatal(err)
		}
		if _, _, err := ListUserProjects(2); !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Fatalf("missing project accepted: %v", err)
		}
		if err := core.Session(&gorm.Session{SkipHooks: true}).Delete(&User{}, 120).Error; err != nil {
			t.Fatal(err)
		}
		if _, _, err := ListUserProjects(1); !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Fatalf("deleted owner accepted: %v", err)
		}
	})
}

func TestConsoleMemberViewsBatchQueriesAndExactLegacyContract(t *testing.T) {
	testConsoleBatchStores(t, func(t *testing.T, postgres bool) {
		core, telemetry, coreTrace, teleTrace := consoleBatchFixture(t, postgres)
		now := time.Date(2026, 10, 3, 12, 30, 0, 0, time.FixedZone("fixture", 3600)).UTC()
		monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
		dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
		project := Project{ID: 1, PublicID: "proj_members_batch", OwnerUserID: 1}
		createConsoleBatchRows(t, core, &User{Id: 99, PublicID: "usr_deleted_inviter", Username: "historical inviter", DeletedAt: gorm.DeletedAt{Time: now, Valid: true}})
		members := make([]ProjectMember, 0, 20)
		for id := 1; id <= 20; id++ {
			createConsoleBatchRows(t, core, &User{Id: id, PublicID: fmt.Sprintf("usr_members_%d", id), Username: fmt.Sprintf("member-%d", id), Email: fmt.Sprintf("member-%d@example.test", id)})
			member := ProjectMember{ProjectID: 1, UserID: id, Role: ProjectRoleMember, Status: ProjectMemberActive, Version: int64(id), JoinedAt: int64(id), AllowedModels: StringSlice{"model-a"}, ReasoningEffortCap: "medium"}
			if id == 1 {
				member.InvitedBy, member.Status = 99, ProjectMemberSuspended
			}
			members = append(members, member)
			createConsoleBatchRows(t, core, &Token{Id: 100 + id, PublicID: fmt.Sprintf("tok_members_%d", id), ProjectID: 1, UserId: id, Key: common.GenerateRequestID()})
			createConsoleBatchRows(t, core, &ProjectSpend{EventKey: fmt.Sprintf("spend-members-%d", id), ProjectID: 1, ActorUserID: id, AmountMicrousd: billing.MicroUSD(100 + id), OccurredAt: monthStart.Add(time.Hour)})
			for j, row := range []Log{{ProjectID: 1, UserId: id, Type: LogTypeConsume, CreatedAt: monthStart.Add(time.Hour).Unix(), Other: `{"effort_clamped":true}`}, {ProjectID: 1, UserId: id, Type: LogTypeError, CreatedAt: dayStart.Unix()}, {ProjectID: 1, UserId: id, Type: LogTypeError, CreatedAt: dayStart.Add(-time.Second).Unix()}, {ProjectID: 2, UserId: id, Type: LogTypeConsume, CreatedAt: now.Unix()}} {
				row.PublicID, row.RequestTS = fmt.Sprintf("log_members_%d_%d", id, j), row.CreatedAt*1000+int64(id*10+j+1)
				createConsoleBatchRows(t, telemetry, &row)
			}
		}
		createConsoleBatchRows(t, core, &[]SpendBudget{{ID: 1, ProjectID: 1, Scope: SpendBudgetScopeUser, UserID: 1, Period: SpendBudgetPeriodMonth, LimitMicrousd: 800, WarningThresholdPct: 41, Enabled: false}, {ID: 2, ProjectID: 1, Scope: SpendBudgetScopeUser, UserID: 1, Period: SpendBudgetPeriodMonth, LimitMicrousd: 900, WarningThresholdPct: 70, Enabled: true}, {ID: 3, ProjectID: 1, Scope: SpendBudgetScopeUser, UserID: 3, Period: SpendBudgetPeriodMonth, LimitMicrousd: 700, WarningThresholdPct: 88, Enabled: true}})
		// Set the disabled value explicitly after GORM applies its boolean default.
		if err := core.Model(&SpendBudget{}).Where("id = ?", 1).UpdateColumn("enabled", false).Error; err != nil {
			t.Fatal(err)
		}
		internalScope := "fixture-internal"
		createConsoleBatchRows(t, core, &[]Token{{Id: 201, PublicID: "tok_extra_disabled", ProjectID: 1, UserId: 1, Key: common.GenerateRequestID(), Status: common.TokenStatusDisabled}, {Id: 202, PublicID: "tok_extra_internal", ProjectID: 1, UserId: 1, Key: common.GenerateRequestID(), InternalScope: &internalScope}, {Id: 203, PublicID: "tok_extra_deleted", ProjectID: 1, UserId: 1, Key: common.GenerateRequestID(), DeletedAt: gorm.DeletedAt{Time: now, Valid: true}}, {Id: 204, PublicID: "tok_extra_foreign", ProjectID: 2, UserId: 1, Key: common.GenerateRequestID()}})
		createConsoleBatchRows(t, core, &[]ProjectSpend{{EventKey: "refund-members", ProjectID: 1, ActorUserID: 1, AmountMicrousd: -20, OccurredAt: monthStart.Add(2 * time.Hour)}, {EventKey: "daily-members", ProjectID: 1, ActorUserID: 1, AmountMicrousd: 25, Aggregated: true, OccurredAt: dayStart}, {EventKey: "old-members", ProjectID: 1, ActorUserID: 1, AmountMicrousd: 999, OccurredAt: monthStart.Add(-time.Second)}, {EventKey: "future-members", ProjectID: 1, ActorUserID: 1, AmountMicrousd: 999, OccurredAt: time.Unix(now.Unix()+1, 0)}, {EventKey: "foreign-members", ProjectID: 2, ActorUserID: 1, AmountMicrousd: 999, OccurredAt: dayStart}})
		for j, row := range []Log{{ProjectID: 0, UserId: 1, Type: LogTypeConsume, CreatedAt: now.Unix()}, {ProjectID: 0, UserId: 2, Type: LogTypeConsume, CreatedAt: now.Unix()}, {ProjectID: 1, UserId: 1, Type: LogTypeError, CreatedAt: now.Unix() + 1}, {ProjectID: 1, UserId: 1, Type: LogTypeUnknown, CreatedAt: now.Unix() + 9, Other: `{"effort_clamped":true}`}} {
			row.PublicID, row.RequestTS = fmt.Sprintf("log_extra_members_%d", j), row.CreatedAt*1000+int64(999+j)
			createConsoleBatchRows(t, telemetry, &row)
		}
		for _, count := range []int{1, 20} {
			coreTrace.statements, teleTrace.statements = nil, nil
			rows, err := ReadProjectMemberViews(project, members[:count], now)
			if err != nil {
				t.Fatal(err)
			}
			if len(coreTrace.statements) != 4 || len(teleTrace.statements) != 3 {
				t.Fatalf("%d members queries core=%d telemetry=%d: %v %v", count, len(coreTrace.statements), len(teleTrace.statements), coreTrace.statements, teleTrace.statements)
			}
			t.Logf("%d members: hydration Core=4, Telemetry=3", count)
			for i, row := range rows {
				legacy, err := ReadProjectMemberView(project, members[i], now)
				if err != nil || !reflect.DeepEqual(row, legacy) {
					t.Fatalf("member %d contract differs:\n batch=%+v\n original=%+v\n err=%v", i, row, legacy, err)
				}
			}
			first := rows[0]
			if first.MonthSpendMicrousd != 106 || first.BudgetMicrousd != nil || first.WarningPct != 41 || first.KeyCount != 2 || first.InvitedBy == nil || first.InvitedBy.ID != "usr_deleted_inviter" || first.RejectedCountToday != 1 || first.LastActiveAt == nil || *first.LastActiveAt != now.Unix() || first.EffortDownedMonth != 1 {
				t.Fatalf("edge contract: %+v", first)
			}
			if count > 1 && (rows[1].WarningPct != 80 || rows[1].BudgetMicrousd != nil || rows[1].LastActiveAt == nil || *rows[1].LastActiveAt != monthStart.Add(time.Hour).Unix() || rows[2].BudgetMicrousd == nil || *rows[2].BudgetMicrousd != 700) {
				t.Fatalf("missing budget/legacy scope/enabled budget: %+v %+v", rows[1], rows[2])
			}
		}
		if _, err := ReadProjectMemberViews(project, []ProjectMember{{ProjectID: 1, UserID: 999}}, now); !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Fatalf("missing user accepted: %v", err)
		}
		if _, err := ReadProjectMemberViews(project, []ProjectMember{{ProjectID: 1, UserID: 1, InvitedBy: 999}}, now); !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Fatalf("missing inviter accepted: %v", err)
		}
		if err := core.Session(&gorm.Session{SkipHooks: true}).Delete(&User{}, 2).Error; err != nil {
			t.Fatal(err)
		}
		if _, err := ReadProjectMemberViews(project, members[1:2], now); !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Fatalf("soft-deleted member accepted: %v", err)
		}
	})
}

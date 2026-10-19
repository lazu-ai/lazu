package controller

import (
	"errors"
	"fmt"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	gormsqlite "github.com/glebarez/sqlite"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/middleware"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/setting"
	"github.com/lazu-ai/lazu/pkg/billing"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func projectTokenBatchFixture(t *testing.T, dialect string) (*gorm.DB, *gorm.DB) {
	t.Helper()
	config := &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)}
	open := func(label string) *gorm.DB {
		var db *gorm.DB
		var err error
		if dialect == "postgres" {
			dsn := os.Getenv("TEST_GATEWAY_CORE_DSN")
			if dsn == "" {
				t.Skip("isolated TEST_GATEWAY_CORE_DSN required")
			}
			base, err := gorm.Open(postgres.Open(dsn), config)
			if err != nil {
				t.Fatal(err)
			}
			schema := "key_page_" + label + "_" + strings.ToLower(strings.ReplaceAll(common.GenerateRequestID(), "-", ""))
			if err := base.Exec("CREATE SCHEMA " + schema).Error; err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				base.Exec("DROP SCHEMA " + schema + " CASCADE")
				connection, _ := base.DB()
				connection.Close()
			})
			parsed, err := url.Parse(dsn)
			if err != nil {
				t.Fatal(err)
			}
			query := parsed.Query()
			query.Set("search_path", schema)
			parsed.RawQuery = query.Encode()
			db, err = gorm.Open(postgres.Open(parsed.String()), config)
		} else {
			db, err = gorm.Open(gormsqlite.Open("file:"+strings.ReplaceAll(t.Name(), "/", "_")+label+"?mode=memory&cache=shared"), config)
		}
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { connection, _ := db.DB(); connection.Close() })
		return db
	}
	core, telemetry := open("core"), open("telemetry")
	if err := core.AutoMigrate(&model.User{}, &model.Project{}, &model.ProjectMember{}, &model.Token{}, &model.SpendBudget{}, &model.SpendBudgetCharge{}, &model.ProjectSpend{}); err != nil {
		t.Fatal(err)
	}
	if err := telemetry.AutoMigrate(&model.Log{}); err != nil {
		t.Fatal(err)
	}
	oldCore, oldTelemetry, oldRedis := model.CoreDB(), model.ConfiguredTelemetryDB(), common.RedisEnabled
	model.SetCoreDB(core)
	model.SetTelemetryDB(telemetry)
	common.RedisEnabled = false
	t.Cleanup(func() {
		model.SetCoreDB(oldCore)
		model.SetTelemetryDB(oldTelemetry)
		common.RedisEnabled = oldRedis
	})
	return core, telemetry
}

func TestProjectTokenBudgetPageUsesFixedQueriesAndPreservesFacts(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			core, telemetry := projectTokenBatchFixture(t, dialect)
			create := func(db *gorm.DB, value interface{}) {
				t.Helper()
				if err := db.Session(&gorm.Session{SkipHooks: true}).Create(value).Error; err != nil {
					t.Fatal(err)
				}
			}
			now := time.Now().UTC()
			owner := model.User{Id: 1, PublicID: "usr_list_owner", Username: "list-owner", Quota: 10000, HasToppedUp: true, RPMLimit: 321, TPMLimit: 654321}
			create(core, &owner)
			upgraded := now.Unix()
			project := model.Project{ID: 1, PublicID: "prj_key_page", OwnerUserID: owner.Id, Name: "Key page", Settings: model.JSONValue(`{"who_can_topup":"owner_admin","warning_pct":80}`), UpgradedAt: &upgraded}
			create(core, &project)
			viewer := model.ProjectMember{ProjectID: project.ID, UserID: owner.Id, Role: model.ProjectRoleOwner, Status: model.ProjectMemberActive}
			create(core, &viewer)
			for i := 2; i <= 4; i++ {
				user := model.User{Id: i, PublicID: fmt.Sprintf("usr_list_%d", i), Username: fmt.Sprintf("member-%d", i)}
				create(core, &user)
				member := model.ProjectMember{ProjectID: project.ID, UserID: i, Role: model.ProjectRoleMember, Status: model.ProjectMemberActive}
				if i == 4 {
					member.Status = model.ProjectMemberRemoved
				}
				create(core, &member)
			}
			tokens := make([]*model.Token, 20)
			for i := range tokens {
				tokens[i] = &model.Token{Id: 100 + i, PublicID: fmt.Sprintf("key_list_%d", i), ProjectID: project.ID, UserId: i%4 + 1,
					Key: fmt.Sprintf("example-project-list-key-%02d", i), Name: fmt.Sprintf("key-%d", i), Status: common.TokenStatusEnabled,
					ExpiredTime: -1, UnlimitedQuota: true, LastUsedAt: now.Unix(), RPMLimit: 200, TPMLimit: 5000, RateLimitsEnabled: true}
				create(core, tokens[i])
			}
			budgets := []model.SpendBudget{
				{ID: 1, ProjectID: project.ID, Scope: model.SpendBudgetScopeOrg, Period: "month", LimitMicrousd: 5000, Enabled: true},
				{ID: 2, ProjectID: project.ID, Scope: model.SpendBudgetScopeUser, UserID: 2, Period: "month", LimitMicrousd: 500, Enabled: true},
				{ID: 3, ProjectID: project.ID, Scope: model.SpendBudgetScopeToken, TokenID: tokens[0].Id, Period: "day", LimitMicrousd: 100, Enabled: true},
				{ID: 4, ProjectID: project.ID, Scope: model.SpendBudgetScopeToken, TokenID: tokens[0].Id, Period: "month", LimitMicrousd: 300, Enabled: true},
				{ID: 5, ProjectID: project.ID, Scope: model.SpendBudgetScopeToken, TokenID: tokens[1].Id, Period: "day", LimitMicrousd: 999, Enabled: false},
				{ID: 6, ProjectID: project.ID, Scope: model.SpendBudgetScopeToken, TokenID: tokens[1].Id, Period: "month", LimitMicrousd: 1000, Enabled: true},
			}
			create(core, &budgets)
			// GORM's default:true applies on create; persist the disabled first row.
			if err := core.Session(&gorm.Session{SkipHooks: true}).Model(&model.SpendBudget{}).Where("id = ?", 5).Update("enabled", false).Error; err != nil {
				t.Fatal(err)
			}
			spends := []model.ProjectSpend{
				{EventKey: "key-zero", ProjectID: project.ID, ActorUserID: owner.Id, TokenID: tokens[0].Id, AmountMicrousd: 90, OccurredAt: now},
				{EventKey: "key-zero-refund", ProjectID: project.ID, ActorUserID: owner.Id, TokenID: tokens[0].Id, AmountMicrousd: -10, OccurredAt: now},
				{EventKey: "key-one", ProjectID: project.ID, ActorUserID: 2, TokenID: tokens[1].Id, AmountMicrousd: 300, OccurredAt: now},
			}
			create(core, &spends)
			logs := []model.Log{
				{RequestID: "reject-empty", ProjectID: project.ID, UserId: owner.Id, TokenId: tokens[0].Id, Type: model.LogTypeError, CreatedAt: now.Unix()},
				{RequestID: "reject-legacy", ProjectID: 0, UserId: owner.Id, TokenId: tokens[0].Id, Type: model.LogTypeError, CreatedAt: now.Unix()},
				{RequestID: "reject-budget", ProjectID: project.ID, TokenId: tokens[0].Id, Type: model.LogTypeError, RejectionReason: "budget", CreatedAt: now.Unix()},
				{RequestID: "reject-other-project", ProjectID: 99, UserId: owner.Id, TokenId: tokens[0].Id, Type: model.LogTypeError, CreatedAt: now.Unix()},
				{RequestID: "reject-yesterday", ProjectID: project.ID, TokenId: tokens[0].Id, Type: model.LogTypeError, CreatedAt: now.AddDate(0, 0, -1).Unix()},
			}
			for i := range logs {
				logs[i].PublicID = fmt.Sprintf("log_budget_page_%d", i)
			}
			create(telemetry, &logs)
			queries := 0
			observe := func(tx *gorm.DB) {
				if tx.Statement.SQL.Len() > 0 {
					queries++
				}
			}
			for _, db := range []*gorm.DB{core, telemetry} {
				db.Callback().Query().After("gorm:query").Register("test:key_page_queries", observe)
				db.Callback().Row().After("gorm:row").Register("test:key_page_queries", observe)
				t.Cleanup(func() {
					db.Callback().Query().Remove("test:key_page_queries")
					db.Callback().Row().Remove("test:key_page_queries")
				})
			}
			pc := middleware.ProjectContext{Project: project, Member: viewer}
			context, _ := gin.CreateTestContext(httptest.NewRecorder())
			context.Request = httptest.NewRequest("GET", "/api/token", nil)
			queries = 0
			few, err := projectTokenPayloads(context, pc, tokens[:2])
			if err != nil {
				t.Fatal(err)
			}
			fewQueries := queries
			queries = 0
			many, err := projectTokenPayloads(context, pc, tokens)
			if err != nil {
				t.Fatal(err)
			}
			if queries != fewQueries || queries > 12 {
				t.Fatalf("SQL grows with Keys: two=%d twenty=%d", fewQueries, queries)
			}
			if !reflect.DeepEqual(few, many[:2]) {
				t.Fatal("page size changed a Key's facts")
			}
			first := many[0]
			if first["month_spend_microusd"] != billing.MicroUSD(80) || first["cap_microusd"] != billing.MicroUSD(300) {
				t.Fatalf("finalized spend/month cap lost: %+v", first)
			}
			if cap := first["period_cap"].(*model.TokenPeriodCap); cap.Period != "day" || cap.SpentUSD != .00008 || cap.LimitUSD != .0001 {
				t.Fatalf("first periodic cap changed: %+v", cap)
			}
			if got := first["effective_limit"].(gin.H); got["kind"] != "key" || got["remaining_microusd"] != billing.MicroUSD(20) {
				t.Fatalf("Key limit lost: %+v", got)
			}
			if many[1]["period_cap"] != (*model.TokenPeriodCap)(nil) {
				t.Fatalf("disabled earliest definition was skipped: %+v", many[1]["period_cap"])
			}
			if got := many[1]["effective_limit"].(gin.H); got["kind"] != "member" || got["remaining_microusd"] != billing.MicroUSD(200) {
				t.Fatalf("member budget lost: %+v", got)
			}
			wantRejections := []projectTokenRejection{{Reason: "budget", Count: 1}, {Reason: "unknown", Count: 2}}
			if !reflect.DeepEqual(first["rejected_today"], wantRejections) || len(many[1]["rejected_today"].([]projectTokenRejection)) != 0 {
				t.Fatalf("rejection grouping/scope changed: %+v", first["rejected_today"])
			}
			ceilingRPM, ceilingTPM := setting.AccountRateLimit(owner.HasToppedUp, owner.RPMLimit, owner.TPMLimit)
			if !reflect.DeepEqual(first["effective_rate_limits"], tokenRateLimitPayloadForAccount(tokens[0], ceilingRPM, ceilingTPM)) {
				t.Fatal("payer rate ceiling changed")
			}
			if capabilities := many[3]["capabilities"].(gin.H); capabilities["can_update"] != false || capabilities["can_reveal"] != false {
				t.Fatalf("removed member gained Key access: %+v", capabilities)
			}
			facts, err := readProjectTokenFacts(pc, tokens)
			if err != nil {
				t.Fatal(err)
			}
			queries = 0
			for _, token := range tokens {
				if _, err := projectTokenPayloadWithFacts(context, pc, token, facts); err != nil {
					t.Fatal(err)
				}
			}
			if queries != 0 {
				t.Fatalf("payload rendering still performs %d SQL queries", queries)
			}
			delete(facts.Members, owner.Id)
			if _, err := projectTokenPayloadWithFacts(context, pc, tokens[0], facts); !errors.Is(err, gorm.ErrRecordNotFound) {
				t.Fatalf("missing membership no longer rejects: %v", err)
			}
			t.Logf("%s: two and twenty Keys both use %d queries; rendering uses zero", dialect, fewQueries)
		})
	}
}

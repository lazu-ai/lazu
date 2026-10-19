package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	gormsqlite "github.com/glebarez/sqlite"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
	"gorm.io/gorm"
)

type dashboardRequestContextKey struct{}

func setupDashboardContextControllerDB(t *testing.T) (*gorm.DB, model.User, model.Project, model.ProjectLogAccess) {
	t.Helper()
	previousCore, previousTelemetry := model.CoreDB(), model.ConfiguredTelemetryDB()
	previousRedis := common.RedisEnabled
	common.RedisEnabled = false
	db, err := gorm.Open(gormsqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	model.SetCoreDB(db)
	model.SetTelemetryDB(db)
	model.EnsureColumnNamesInitialized()
	t.Cleanup(func() {
		model.SetCoreDB(previousCore)
		model.SetTelemetryDB(previousTelemetry)
		common.RedisEnabled = previousRedis
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
	})
	t.Setenv("DASHBOARD_ROLLUP_ENABLED", "false")
	if err := db.AutoMigrate(&model.User{}, &model.Project{}, &model.ProjectMember{}, &model.Token{}, &model.Channel{}, &model.DashboardUsageHourlyTotal{}, &model.Log{}, &model.RoutingDecision{}, &model.RoutingOutcome{}); err != nil {
		t.Fatal(err)
	}
	user := model.User{Username: "dashboard-context-owner"}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	var project model.Project
	var member model.ProjectMember
	if err := db.Where("owner_user_id = ?", user.Id).First(&project).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Where("project_id = ? AND user_id = ?", project.ID, user.Id).First(&member).Error; err != nil {
		t.Fatal(err)
	}
	access, err := model.NewProjectLogAccess(project, member)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Log{UserId: user.Id, ProjectID: project.ID, Type: model.LogTypeConsume, ModelName: "context-model", CreatedAt: 100, AmountMicrousd: 7}).Error; err != nil {
		t.Fatal(err)
	}
	return db, user, project, access
}

// Inspect real GORM reads, including Scan's Row callback. Cancel at the first
// analytical SQL dispatch to model a disconnected HTTP client; no slow query
// or timing assumption is needed to prove the handler reaches the driver with
// the same request context.
func observeDashboardRequestContext(t *testing.T, db *gorm.DB, expected any, cancel context.CancelFunc) *int {
	t.Helper()
	reads := 0
	observe := func(tx *gorm.DB) {
		table := tx.Statement.Table
		if table == "" && tx.Statement.Schema != nil {
			table = tx.Statement.Schema.Table
		}
		if table != "logs" && table != "routing_outcome" && !strings.HasPrefix(table, "dashboard_usage_hourly_") {
			return
		}
		reads++
		if tx.Statement.Context.Value(dashboardRequestContextKey{}) != expected {
			t.Errorf("analytical read of %s lost the HTTP request context", table)
		}
		if cancel != nil {
			cancel()
		}
	}
	if err := db.Callback().Query().Before("gorm:query").Register("test:dashboard-request-context", observe); err != nil {
		t.Fatal(err)
	}
	if err := db.Callback().Row().Before("gorm:row").Register("test:dashboard-request-context", observe); err != nil {
		t.Fatal(err)
	}
	return &reads
}

func TestDashboardHandlersPropagateRequestContext(t *testing.T) {
	cases := []struct {
		name, route, query string
		handler            gin.HandlerFunc
	}{
		{"platform", "/api/data/dashboard", "scope=platform", GetUserDashboardUsage},
		{"platform filtered users", "/api/data/dashboard", "scope=platform&username=dashboard-context-owner", GetUserDashboardUsage},
		{"customer project", "/api/data/dashboard", "scope=self", GetUserDashboardUsage},
		{"self", "/api/data/self/dashboard", "", GetSelfDashboardUsage},
		{"account", "/api/user/:id/dashboard", "", GetUserAccountDashboard},
		{"platform export", "/api/data/export", "scope=platform&dataset=logs&format=csv", ExportUserUsage},
		{"project export", "/api/data/export", "scope=self&dataset=logs&format=xlsx", ExportUserUsage},
		{"model spend", "/api/models/ops/spend", "models=context-model", GetModelOpsSpend},
	}
	for _, test := range cases {
		for _, disconnected := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/disconnected=%t", test.name, disconnected), func(t *testing.T) {
				db, user, _, _ := setupDashboardContextControllerDB(t)
				token := new(int)
				ctx, cancel := context.WithCancel(context.WithValue(context.Background(), dashboardRequestContextKey{}, token))
				defer cancel()
				var cancelOnRead context.CancelFunc
				if disconnected {
					cancelOnRead = cancel
				}
				reads := observeDashboardRequestContext(t, db, token, cancelOnRead)
				gin.SetMode(gin.TestMode)
				router := gin.New()
				router.Use(func(c *gin.Context) { c.Set("id", user.Id); c.Set("role", common.RoleRootUser) })
				router.GET(test.route, test.handler)
				target := strings.ReplaceAll(test.route, ":id", user.PublicID) + "?start_timestamp=1&end_timestamp=1000&" + test.query
				recorder := httptest.NewRecorder()
				router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, target, nil).WithContext(ctx))
				if *reads == 0 {
					t.Fatalf("handler did not reach an analytical query: %d %s", recorder.Code, recorder.Body.String())
				}
				if disconnected {
					var body struct {
						Success bool `json:"success"`
					}
					if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil || body.Success {
						t.Fatalf("canceled analytics returned success: %d %s", recorder.Code, recorder.Body.String())
					}
					if !errors.Is(ctx.Err(), context.Canceled) {
						t.Fatal("request was not canceled at analytical dispatch")
					}
				} else if recorder.Code != http.StatusOK {
					t.Fatalf("successful read: %d %s", recorder.Code, recorder.Body.String())
				}
			})
		}
	}
}

func TestExportUsageLogsPropagatesRequestContext(t *testing.T) {
	for _, projectScoped := range []bool{false, true} {
		for _, disconnected := range []bool{false, true} {
			t.Run(fmt.Sprintf("project=%t/disconnected=%t", projectScoped, disconnected), func(t *testing.T) {
				db, user, _, access := setupDashboardContextControllerDB(t)
				token := new(int)
				ctx, cancel := context.WithCancel(context.WithValue(context.Background(), dashboardRequestContextKey{}, token))
				defer cancel()
				var cancelOnRead context.CancelFunc
				if disconnected {
					cancelOnRead = cancel
				}
				reads := observeDashboardRequestContext(t, db, token, cancelOnRead)
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest(http.MethodGet, "/api/data/export", nil).WithContext(ctx)
				c.Set("id", user.Id)
				var projectAccess *model.ProjectLogAccess
				if projectScoped {
					projectAccess = &access
				}
				logs, err := listExportUsageLogs(c, projectAccess, common.RoleRootUser, model.DashboardUsageWindow{StartTimestamp: 1, EndTimestamp: 1000}, model.DashboardUsageLogFilters{})
				if *reads != 1 {
					t.Fatalf("export should dispatch one bounded log read, got %d", *reads)
				}
				if disconnected {
					if !errors.Is(err, context.Canceled) {
						t.Fatalf("canceled log read: %v", err)
					}
				} else if err != nil || len(logs) != 1 || logs[0].AmountMicrousd != 7 {
					t.Fatalf("log export changed data: logs=%v err=%v", logs, err)
				}
			})
		}
	}
}

func TestOverviewHandlerPropagatesCanceledRequestContext(t *testing.T) {
	for _, scope := range []string{"self", "platform"} {
		t.Run(scope, func(t *testing.T) {
			db, user, _, _ := setupDashboardContextControllerDB(t)
			token := new(int)
			ctx, cancel := context.WithCancel(context.WithValue(context.Background(), dashboardRequestContextKey{}, token))
			defer cancel()
			reads := observeDashboardRequestContext(t, db, token, cancel)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodGet, "/api/data/overview?start=1&end=1000&scope="+scope, nil).WithContext(ctx)
			c.Set("id", user.Id)
			c.Set("role", common.RoleRootUser)
			GetOverview(c)
			var body struct {
				Success bool `json:"success"`
			}
			if *reads != 1 || !errors.Is(ctx.Err(), context.Canceled) || json.Unmarshal(recorder.Body.Bytes(), &body) != nil || body.Success {
				t.Fatalf("overview did not stop on its canceled initial aggregate: reads=%d status=%d body=%s", *reads, recorder.Code, recorder.Body.String())
			}
		})
	}
}

package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	gormsqlite "github.com/glebarez/sqlite"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/middleware"
	"github.com/lazu-ai/lazu/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type projectMemberBatchSQLCapture struct {
	logger.Interface
	statements []string
}

func (capture *projectMemberBatchSQLCapture) Trace(_ context.Context, _ time.Time, sql func() (string, int64), _ error) {
	statement, _ := sql()
	capture.statements = append(capture.statements, statement)
}

func projectMemberBatchFixture(t *testing.T) (*gorm.DB, *projectMemberBatchSQLCapture, *projectMemberBatchSQLCapture) {
	t.Helper()
	oldCore, oldTelemetry, oldRedis := model.CoreDB(), model.ConfiguredTelemetryDB(), common.RedisEnabled
	common.RedisEnabled = false
	open := func(kind string) *gorm.DB {
		db, err := gorm.Open(gormsqlite.Open(fmt.Sprintf("file:member_list_%s_%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"), kind)), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { raw, _ := db.DB(); _ = raw.Close() })
		return db
	}
	core, telemetry := open("core"), open("telemetry")
	t.Cleanup(func() { model.SetCoreDB(oldCore); model.SetTelemetryDB(oldTelemetry); common.RedisEnabled = oldRedis })
	if err := core.AutoMigrate(&model.User{}, &model.Project{}, &model.ProjectMember{}, &model.Token{}, &model.SpendBudget{}, &model.ProjectSpend{}); err != nil {
		t.Fatal(err)
	}
	if err := telemetry.AutoMigrate(&model.Log{}); err != nil {
		t.Fatal(err)
	}
	coreTrace := &projectMemberBatchSQLCapture{Interface: logger.Default.LogMode(logger.Silent)}
	telemetryTrace := &projectMemberBatchSQLCapture{Interface: logger.Default.LogMode(logger.Silent)}
	core = core.Session(&gorm.Session{Logger: coreTrace})
	model.SetCoreDB(core)
	model.SetTelemetryDB(telemetry.Session(&gorm.Session{Logger: telemetryTrace}))
	return core, coreTrace, telemetryTrace
}

func TestConsoleProjectMemberListBatchQueriesPermissionsAndPagination(t *testing.T) {
	gin.SetMode(gin.TestMode)
	core, coreTrace, teleTrace := projectMemberBatchFixture(t)
	create := func(value interface{}) {
		if err := core.Session(&gorm.Session{SkipHooks: true}).Create(value).Error; err != nil {
			t.Fatal(err)
		}
	}
	project := model.Project{ID: 1, PublicID: "proj_member_list", OwnerUserID: 1}
	create(&project)
	for id := 21; id >= 1; id-- {
		create(&model.User{Id: id, PublicID: fmt.Sprintf("usr_member_list_%d", id), Username: fmt.Sprintf("member-%d", id)})
		create(&model.ProjectMember{ID: id, ProjectID: 1, UserID: id, Role: model.ProjectRoleMember, Status: model.ProjectMemberActive, JoinedAt: int64((id - 1) / 2)})
	}
	create(&model.ProjectMember{ID: 22, ProjectID: 1, UserID: 22, Role: model.ProjectRoleMember, Status: model.ProjectMemberRemoved})
	create(&model.ProjectMember{ID: 23, ProjectID: 2, UserID: 23, Role: model.ProjectRoleMember, Status: model.ProjectMemberActive})
	request := func(viewer model.ProjectMember, path string, withContext bool) *httptest.ResponseRecorder {
		engine := gin.New()
		engine.GET("/members", func(c *gin.Context) {
			if withContext {
				c.Set("console_project_context", middleware.ProjectContext{Project: project, Member: viewer})
			}
			ListProjectMembers(c)
		})
		coreTrace.statements, teleTrace.statements = nil, nil
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		return w
	}
	for _, role := range []string{model.ProjectRoleOwner, model.ProjectRoleAdmin} {
		viewer := model.ProjectMember{ProjectID: 1, UserID: 1, Role: role, Status: model.ProjectMemberActive}
		for _, page := range []struct{ number, size, count, first int }{{1, 1, 1, 1}, {1, 20, 20, 1}, {2, 20, 1, 21}} {
			w := request(viewer, fmt.Sprintf("/members?page=%d&page_size=%d", page.number, page.size), true)
			var body struct {
				Success bool
				Data    struct {
					Items    []model.ProjectMemberView
					Total    int64
					Page     int
					PageSize int `json:"page_size"`
				}
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || w.Code != http.StatusOK || !body.Success || body.Data.Total != 21 || body.Data.Page != page.number || body.Data.PageSize != page.size || len(body.Data.Items) != page.count {
				t.Fatalf("%s page contract: status=%d body=%s err=%v", role, w.Code, w.Body.String(), err)
			}
			if len(coreTrace.statements) != 6 || len(teleTrace.statements) != 3 {
				t.Fatalf("%s page size %d SQL core=%d telemetry=%d: %v %v", role, page.size, len(coreTrace.statements), len(teleTrace.statements), coreTrace.statements, teleTrace.statements)
			}
			t.Logf("%s page=%d size=%d: full handler Core=6, Telemetry=3", role, page.number, page.size)
			for i, item := range body.Data.Items {
				if item.UserID != fmt.Sprintf("usr_member_list_%d", page.first+i) || item.AllowedModels == nil {
					t.Fatalf("joined_at/id ordering or array contract changed: %+v", body.Data.Items)
				}
			}
		}
		w := request(viewer, "/members?page=3&page_size=20", true)
		if w.Code != http.StatusOK || len(coreTrace.statements) != 2 || len(teleTrace.statements) != 0 || !strings.Contains(w.Body.String(), `"items":[]`) {
			t.Fatalf("empty page should skip hydration: %s %v %v", w.Body.String(), coreTrace.statements, teleTrace.statements)
		}
	}
	for _, tc := range []struct {
		name        string
		viewer      model.ProjectMember
		withContext bool
	}{
		{"ordinary member", model.ProjectMember{Role: model.ProjectRoleMember, Status: model.ProjectMemberActive}, true},
		{"suspended owner", model.ProjectMember{Role: model.ProjectRoleOwner, Status: model.ProjectMemberSuspended}, true},
		{"removed admin", model.ProjectMember{Role: model.ProjectRoleAdmin, Status: model.ProjectMemberRemoved}, true},
		{"missing context", model.ProjectMember{}, false},
	} {
		w := request(tc.viewer, "/members", tc.withContext)
		if w.Code != http.StatusForbidden || len(coreTrace.statements) != 0 || len(teleTrace.statements) != 0 || strings.Contains(w.Body.String(), "usr_member_list_") {
			t.Fatalf("%s bypassed manager guard: status=%d body=%s SQL=%v/%v", tc.name, w.Code, w.Body.String(), coreTrace.statements, teleTrace.statements)
		}
	}
}

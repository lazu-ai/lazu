package router

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	gormsqlite "github.com/glebarez/sqlite"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/model"
	"gorm.io/gorm"
)

func hasRoute(routes []gin.RouteInfo, method string, path string) bool {
	for _, route := range routes {
		if route.Method == method && route.Path == path {
			return true
		}
	}
	return false
}

func setupModelCatalogRouteTestDB(t *testing.T) func() {
	t.Helper()
	drainRelayDiagnosticFixture(t)

	previousDB := model.CoreDB()
	previousRedisEnabled := common.RedisEnabled
	common.RedisEnabled = false

	db, err := gorm.Open(gormsqlite.Open("file:"+common.GenerateRequestID()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	// Shared-cache SQLite reports table-lock deadlocks (SQLITE_LOCKED) at
	// once instead of waiting. The global relay-diagnostics workers update
	// logs while the next request flushes its consume log, so one pooled
	// connection serializes those writers the way a busy timeout would.
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("failed to get sqlite handle: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	model.SetCoreDB(db)
	model.EnsureColumnNamesInitialized()

	if err := db.AutoMigrate(
		&model.Project{}, &model.ProjectMember{}, &model.ProjectRemoval{}, &model.ProjectRemovalKey{},
		&model.SpendBudget{}, &model.SpendBudgetCharge{}, &model.ProjectSpend{}, &model.SpendBudgetAlert{}, &model.SpendBudgetReservation{},
		&model.User{},
		&model.Token{},
		&model.Channel{},
		&model.Ability{},
		&model.Model{},
		&model.ModelMetadata{},
		&model.ModelSellPrice{},
		&model.MediaJob{},
		&model.EphemeralCredential{},
		&model.EphemeralCredentialAudit{},
		&model.Vendor{},
	); err != nil {
		t.Fatalf("failed to migrate test tables: %v", err)
	}

	return func() {
		drainRelayDiagnosticFixture(t)
		_ = sqlDB.Close()
		common.RedisEnabled = previousRedisEnabled
		model.SetCoreDB(previousDB)
		if previousDB != nil {
			model.RefreshPricing()
		}
	}
}

func TestSetApiRouter_RegistersCriticalRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()

	SetApiRouter(engine)
	routes := engine.Routes()

	criticalRoutes := []struct {
		method string
		path   string
	}{
		{method: "GET", path: "/api/public/status"},
		{method: "POST", path: "/api/stripe/webhook"},
		{method: "POST", path: "/api/telegram/webhook"},
		{method: "POST", path: "/api/user/stripe/pay"},
		{method: "GET", path: "/api/user/self"},
		{method: "GET", path: "/api/models/catalog"},
		{method: "POST", path: "/api/files"},
		{method: "GET", path: "/api/usage/summary"},
		{method: "GET", path: "/api/usage/requests/:request_id"},
		{method: "GET", path: "/api/search/backends"},
		{method: "GET", path: "/api/status"},
		{method: "GET", path: "/api/auth/oauth/:provider/login"},
		{method: "GET", path: "/api/auth/oauth/:provider/callback"},
		{method: "POST", path: "/api/user/reset"},
		{method: "POST", path: "/api/user/reset/v2"},
		{method: "GET", path: "/api/user/email/bind"},
	}

	for _, item := range criticalRoutes {
		if !hasRoute(routes, item.method, item.path) {
			t.Fatalf("missing route %s %s", item.method, item.path)
		}
	}

}

func TestSetApiRouter_ModelCatalogAcceptsBearerAPIKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cleanup := setupModelCatalogRouteTestDB(t)
	defer cleanup()

	user := &model.User{
		Id:          2001,
		Username:    "catalog_tester",
		Password:    "password123",
		DisplayName: "catalog_tester",
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
	}
	if err := model.CoreDB().Create(user).Error; err != nil {
		t.Fatalf("failed to insert user: %v", err)
	}

	token := &model.Token{
		Id:                 3001,
		UserId:             user.Id,
		Key:                "catalogtoken",
		Status:             common.TokenStatusEnabled,
		ExpiredTime:        -1,
		RemainQuota:        100,
		ModelLimitsEnabled: true,
		ModelLimits:        "catalog-gpt",
	}
	if err := token.Insert(); err != nil {
		t.Fatalf("failed to insert token: %v", err)
	}

	channel := &model.Channel{
		Id:     4001,
		Type:   constant.ChannelTypeOpenAI,
		Key:    "upstream-key",
		Status: common.ChannelStatusEnabled,
		Name:   "catalog-channel",
		Models: "catalog-gpt",
		Group:  model.ChannelLaneDirect,
	}
	if err := model.CoreDB().Create(channel).Error; err != nil {
		t.Fatalf("failed to insert channel: %v", err)
	}

	ability := &model.Ability{
		Group:     model.ChannelLaneDirect,
		Model:     "catalog-gpt",
		ChannelId: channel.Id,
		Enabled:   true,
	}
	if err := model.CoreDB().Create(ability).Error; err != nil {
		t.Fatalf("failed to insert ability: %v", err)
	}
	if err := (&model.Model{
		ModelName:       "catalog-gpt",
		Status:          1,
		LifecycleStatus: model.ModelLifecycleActive,
	}).Insert(); err != nil {
		t.Fatalf("failed to insert model metadata: %v", err)
	}

	model.RefreshPricing()

	engine := gin.New()
	SetApiRouter(engine)

	req := httptest.NewRequest(http.MethodGet, "/api/models/catalog", nil)
	req.Header.Set("Authorization", "Bearer sk-catalogtoken")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", w.Code, w.Body.String())
	}

	var payload struct {
		Object string `json:"object"`
		Data   []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if payload.Object != "list" {
		t.Fatalf("expected object=list, got %q", payload.Object)
	}
	if len(payload.Data) != 1 || payload.Data[0].ID != "catalog-gpt" {
		t.Fatalf("expected catalog to contain catalog-gpt only, got %+v", payload.Data)
	}
}

func TestSetApiRouter_UserModelCatalogUsesPublishedLanesForSessionTiers(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cleanup := setupModelCatalogRouteTestDB(t)
	defer cleanup()

	channels := []model.Channel{
		{
			Id:     4201,
			Type:   constant.ChannelTypeOpenAI,
			Key:    "session-direct-upstream",
			Status: common.ChannelStatusEnabled,
			Name:   "session-direct-channel",
			Models: "session-direct,session-shared",
			Group:  model.ChannelLaneDirect,
		},
		{
			Id:     4202,
			Type:   constant.ChannelTypeOpenAI,
			Key:    "session-cheap-upstream",
			Status: common.ChannelStatusEnabled,
			Name:   "session-agent-lane-channel",
			Models: "session-cheap,session-shared",
			Group:  model.ChannelLaneAgent,
		},
	}
	if err := model.CoreDB().Create(&channels).Error; err != nil {
		t.Fatalf("failed to insert channels: %v", err)
	}

	abilities := []model.Ability{
		{Group: model.ChannelLaneDirect, Model: "session-direct", ChannelId: 4201, Enabled: true},
		{Group: model.ChannelLaneDirect, Model: "session-shared", ChannelId: 4201, Enabled: true},
		{Group: model.ChannelLaneAgent, Model: "session-cheap", ChannelId: 4202, Enabled: true},
		{Group: model.ChannelLaneAgent, Model: "session-shared", ChannelId: 4202, Enabled: true},
	}
	if err := model.CoreDB().Create(&abilities).Error; err != nil {
		t.Fatalf("failed to insert abilities: %v", err)
	}

	models := []model.Model{
		{ModelName: "session-direct", Status: 1, LifecycleStatus: model.ModelLifecycleActive},
		{ModelName: "session-cheap", Status: 1, LifecycleStatus: model.ModelLifecycleActive},
		{ModelName: "session-shared", Status: 1, LifecycleStatus: model.ModelLifecycleActive},
	}
	if err := model.CoreDB().Create(&models).Error; err != nil {
		t.Fatalf("failed to insert model metadata: %v", err)
	}
	model.RefreshPricing()

	for i, tier := range []string{"default", "Tier0"} {
		t.Run(tier, func(t *testing.T) {
			user := &model.User{
				Id:          2201 + i,
				Username:    "session_catalog_" + tier,
				Password:    "password123",
				DisplayName: "session catalog " + tier,
				Role:        common.RoleCommonUser,
				Status:      common.UserStatusEnabled,
			}
			if err := model.CoreDB().Create(user).Error; err != nil {
				t.Fatalf("failed to insert user: %v", err)
			}

			engine := gin.New()
			engine.Use(sessions.Sessions("test-session", cookie.NewStore([]byte("test-secret"))))
			engine.Use(func(c *gin.Context) {
				session := sessions.Default(c)
				session.Set("username", user.Username)
				session.Set("role", user.Role)
				session.Set("id", user.Id)
				session.Set("status", user.Status)
				session.Set("session_version", user.SessionVersion)
				c.Next()
			})
			SetApiRouter(engine)

			req := httptest.NewRequest(http.MethodGet, "/api/user/models/catalog", nil)
			req.Header.Set("X-Lazu-User", user.PublicID)
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("expected status 200, got %d, body=%s", w.Code, w.Body.String())
			}

			var payload struct {
				Object string `json:"object"`
				Data   []struct {
					ID         string `json:"id"`
					Accessible *bool  `json:"accessible"`
				} `json:"data"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
				t.Fatalf("failed to decode response: %v", err)
			}
			if payload.Object != "list" {
				t.Fatalf("expected object=list, got %q", payload.Object)
			}

			got := make([]string, 0, len(payload.Data))
			for _, item := range payload.Data {
				got = append(got, item.ID)
				if item.Accessible == nil || !*item.Accessible {
					t.Fatalf("expected %s to be accessible, got %+v", item.ID, item.Accessible)
				}
			}
			want := []string{"session-cheap", "session-direct", "session-shared"}
			if len(got) != len(want) {
				t.Fatalf("expected session catalog %v, got %v", want, got)
			}
			for index := range want {
				if got[index] != want[index] {
					t.Fatalf("expected session catalog %v, got %v", want, got)
				}
			}
		})
	}
}

func TestSetApiRouter_ModelCatalogFiltersUnavailableAndVendorLimitedModels(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cleanup := setupModelCatalogRouteTestDB(t)
	defer cleanup()

	user := &model.User{
		Id:          2101,
		Username:    "catalog_filter_tester",
		Password:    "password123",
		DisplayName: "catalog_filter_tester",
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
	}
	if err := model.CoreDB().Create(user).Error; err != nil {
		t.Fatalf("failed to insert user: %v", err)
	}

	token := &model.Token{
		Id:                  3101,
		UserId:              user.Id,
		Key:                 "catalogfiltertoken",
		Status:              common.TokenStatusEnabled,
		ExpiredTime:         -1,
		RemainQuota:         100,
		ModelLimitsEnabled:  true,
		ModelLimits:         "allowed-model,blocked-vendor-model,stale-model",
		VendorLimitsEnabled: true,
		VendorLimits:        model.IntSlice{202},
	}
	if err := token.Insert(); err != nil {
		t.Fatalf("failed to insert token: %v", err)
	}

	vendors := []*model.Vendor{
		{Id: 201, Name: "Allowed Vendor", Status: 1},
		{Id: 202, Name: "Blocked Vendor", Status: 1},
	}
	for _, vendor := range vendors {
		if err := vendor.Insert(); err != nil {
			t.Fatalf("failed to insert vendor: %v", err)
		}
	}

	models := []*model.Model{
		{Id: 5101, ModelName: "allowed-model", VendorID: 201, Status: 1},
		{Id: 5102, ModelName: "blocked-vendor-model", VendorID: 202, Status: 1},
		{Id: 5103, ModelName: "stale-model", VendorID: 201, Status: 1},
	}
	for _, meta := range models {
		if err := meta.Insert(); err != nil {
			t.Fatalf("failed to insert model metadata: %v", err)
		}
	}

	channels := []*model.Channel{
		{
			Id:     4101,
			Type:   constant.ChannelTypeOpenAI,
			Key:    "upstream-key-a",
			Status: common.ChannelStatusEnabled,
			Name:   "allowed-channel",
			Models: "allowed-model",
			Group:  model.ChannelLaneDirect,
		},
		{
			Id:     4102,
			Type:   constant.ChannelTypeOpenAI,
			Key:    "upstream-key-b",
			Status: common.ChannelStatusEnabled,
			Name:   "blocked-channel",
			Models: "blocked-vendor-model",
			Group:  model.ChannelLaneDirect,
		},
	}
	for _, channel := range channels {
		if err := model.CoreDB().Create(channel).Error; err != nil {
			t.Fatalf("failed to insert channel: %v", err)
		}
	}

	abilities := []*model.Ability{
		{Group: model.ChannelLaneDirect, Model: "allowed-model", ChannelId: 4101, Enabled: true},
		{Group: model.ChannelLaneDirect, Model: "blocked-vendor-model", ChannelId: 4102, Enabled: true},
	}
	for _, ability := range abilities {
		if err := model.CoreDB().Create(ability).Error; err != nil {
			t.Fatalf("failed to insert ability: %v", err)
		}
	}

	model.RefreshPricing()

	engine := gin.New()
	SetApiRouter(engine)

	req := httptest.NewRequest(http.MethodGet, "/api/models/catalog", nil)
	req.Header.Set("Authorization", "Bearer sk-catalogfiltertoken")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", w.Code, w.Body.String())
	}

	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(payload.Data) != 1 {
		t.Fatalf("expected only one visible model after filtering, got %+v", payload.Data)
	}
	if payload.Data[0].ID != "allowed-model" {
		t.Fatalf("expected catalog to contain allowed-model only, got %+v", payload.Data)
	}
}

func TestSetApiRouter_ModelCatalogExcludesCheapLaneWithoutVendorGroups(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cleanup := setupModelCatalogRouteTestDB(t)
	defer cleanup()

	user := &model.User{
		Id:          2401,
		Username:    "catalog_vendor_override_tester",
		Password:    "password123",
		DisplayName: "catalog_vendor_override_tester",
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
	}
	if err := model.CoreDB().Create(user).Error; err != nil {
		t.Fatalf("failed to insert user: %v", err)
	}

	token := &model.Token{
		Id:          3401,
		UserId:      user.Id,
		Key:         "catalogvendoroverride",
		Status:      common.TokenStatusEnabled,
		ExpiredTime: -1,
		RemainQuota: 100,
	}
	if err := token.Insert(); err != nil {
		t.Fatalf("failed to insert token: %v", err)
	}

	vendors := []*model.Vendor{
		{Id: 500, Name: "Base Vendor", Status: 1},
		{Id: 501, Name: "Override Vendor", Status: 1},
	}
	for _, vendor := range vendors {
		if err := vendor.Insert(); err != nil {
			t.Fatalf("failed to insert vendor: %v", err)
		}
	}

	models := []*model.Model{
		{Id: 5201, ModelName: "base-model", VendorID: 500, Status: 1},
		{Id: 5202, ModelName: "vendor-override-model", VendorID: 501, Status: 1},
	}
	for _, meta := range models {
		if err := meta.Insert(); err != nil {
			t.Fatalf("failed to insert model metadata: %v", err)
		}
	}

	channels := []*model.Channel{
		{
			Id:     4401,
			Type:   constant.ChannelTypeOpenAI,
			Key:    "upstream-key-base",
			Status: common.ChannelStatusEnabled,
			Name:   "base-channel",
			Models: "base-model",
			Group:  model.ChannelLaneDirect,
		},
		{
			Id:     4402,
			Type:   constant.ChannelTypeOpenAI,
			Key:    "upstream-key-override",
			Status: common.ChannelStatusEnabled,
			Name:   "vendor-channel",
			Models: "vendor-override-model",
			Group:  model.ChannelLaneAgent,
		},
	}
	for _, channel := range channels {
		if err := model.CoreDB().Create(channel).Error; err != nil {
			t.Fatalf("failed to insert channel: %v", err)
		}
	}

	abilities := []*model.Ability{
		{Group: model.ChannelLaneDirect, Model: "base-model", ChannelId: 4401, Enabled: true},
		{Group: model.ChannelLaneAgent, Model: "vendor-override-model", ChannelId: 4402, Enabled: true},
	}
	for _, ability := range abilities {
		if err := model.CoreDB().Create(ability).Error; err != nil {
			t.Fatalf("failed to insert ability: %v", err)
		}
	}

	model.RefreshPricing()

	engine := gin.New()
	SetApiRouter(engine)

	req := httptest.NewRequest(http.MethodGet, "/api/models/catalog", nil)
	req.Header.Set("Authorization", "Bearer sk-catalogvendoroverride")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", w.Code, w.Body.String())
	}

	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(payload.Data) != 1 {
		t.Fatalf("expected only direct-lane model, got %+v", payload.Data)
	}
	if payload.Data[0].ID != "base-model" {
		t.Fatalf("expected catalog to exclude agent-lane model without vendor group, got %+v", payload.Data)
	}
}

func TestRouterDatabaseFixturesAreIsolated(t *testing.T) {
	cleanupFirst := setupModelCatalogRouteTestDB(t)
	defer cleanupFirst()
	if err := model.CoreDB().Create(&model.User{Id: 991, Username: "first-fixture"}).Error; err != nil {
		t.Fatal(err)
	}
	cleanupSecond := setupModelCatalogRouteTestDB(t)
	defer cleanupSecond()
	var count int64
	if err := model.CoreDB().Model(&model.User{}).Where("id = ?", 991).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("router fixture reused another test's live database")
	}
}

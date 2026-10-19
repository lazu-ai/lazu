package controller

import (
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	gormsqlite "github.com/glebarez/sqlite"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/model"
	"gorm.io/gorm"
)

func setupModelOpsControllerTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	previousDB := model.CoreDB()
	previousSQLite := common.UsingSQLite
	previousPostgreSQL := common.UsingPostgreSQL
	db, err := gorm.Open(gormsqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatalf("open model ops test database: %v", err)
	}
	model.SetCoreDB(db)
	common.UsingSQLite = true
	common.UsingPostgreSQL = false
	if err := db.AutoMigrate(
		&model.Channel{}, &model.Ability{}, &model.ProviderCostPrice{},
		&model.ChannelProbeResult{}, &model.ChannelProbeBudget{}, &model.ChannelProbeClaim{},
		&model.RouteHealthHourly{}, &model.RouteHealthRollupState{},
	); err != nil {
		t.Fatalf("migrate model ops test database: %v", err)
	}
	t.Cleanup(func() {
		// A handler here starts a health refresh in the background. Close the
		// connection out from under it and the next test's setup races it, so
		// let anything this test started finish first.
		WaitForChannelHealthRefreshes()
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
		model.SetCoreDB(previousDB)
		common.UsingSQLite = previousSQLite
		common.UsingPostgreSQL = previousPostgreSQL
	})
	return db
}

func TestModelOpsModelsWithoutStablePoolRequiresEnabledAbilityAndChannel(t *testing.T) {
	db := setupModelOpsControllerTestDB(t)
	channels := []model.Channel{
		{Id: 1, PublicID: "ready-source", Key: "test", Name: "ready", Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled, Group: model.ChannelLaneDirect},
		{Id: 2, PublicID: "disabled-source", Key: "test", Name: "disabled", Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusManuallyDisabled, Group: model.ChannelLaneDirect},
	}
	if err := db.Create(&channels).Error; err != nil {
		t.Fatalf("create channels: %v", err)
	}
	abilities := []model.Ability{
		{Group: model.ChannelLaneDirect, Model: "ready-model", ChannelId: 1, Enabled: true},
		{Group: model.ChannelLaneDirect, Model: "disabled-model", ChannelId: 2, Enabled: true},
		{Group: model.ChannelLaneAgent, Model: "agent-lane-only-model", ChannelId: 1, Enabled: true},
		{Group: model.ChannelLaneDirect, Model: "ability-off-model", ChannelId: 1, Enabled: false},
	}
	if err := db.Create(&abilities).Error; err != nil {
		t.Fatalf("create abilities: %v", err)
	}

	missing, err := modelOpsModelsWithoutStablePool([]string{"ready-model", "disabled-model", "agent-lane-only-model", "ability-off-model"})
	if err != nil {
		t.Fatalf("check stable pools: %v", err)
	}
	want := []string{"ability-off-model", "agent-lane-only-model", "disabled-model"}
	if !reflect.DeepEqual(missing, want) {
		t.Fatalf("expected missing stable pools %#v, got %#v", want, missing)
	}
	cheapMissing, err := modelOpsModelsWithoutLanePool([]string{"agent-lane-only-model", "ready-model"}, model.ChannelLaneAgent)
	if err != nil {
		t.Fatalf("check agent-lane pools: %v", err)
	}
	if !reflect.DeepEqual(cheapMissing, []string{"ready-model"}) {
		t.Fatalf("expected only the direct-only model to lack an agent-lane pool, got %#v", cheapMissing)
	}
}

func TestModelOpsCostBasisIncludesRequestAndSecondPrices(t *testing.T) {
	db := setupModelOpsControllerTestDB(t)
	requestA, requestB := 0.1, 0.5
	secondA, secondB := 0.01, 0.05
	rows := []model.ProviderCostPrice{
		{ModelName: "media-a", Provider: "test", ChannelID: 1, RequestUSD: &requestA, SecondUSD: &secondA, PricingVersion: 1},
		{ModelName: "media-b", Provider: "test", ChannelID: 1, RequestUSD: &requestB, SecondUSD: &secondB, PricingVersion: 1},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("create provider costs: %v", err)
	}

	costs, err := modelOpsChannelCostBasis([]int{1})
	if err != nil {
		t.Fatalf("build channel cost basis: %v", err)
	}
	cost := costs[1]
	if cost.request == nil || math.Abs(*cost.request-0.3) > 1e-9 || cost.second == nil || math.Abs(*cost.second-0.03) > 1e-9 {
		t.Fatalf("expected request/second medians 0.3/0.03, got %#v", cost)
	}
	exact, err := modelOpsModelChannelCosts([]string{"media-a"}, []int{1})
	if err != nil {
		t.Fatalf("build exact model-channel costs: %v", err)
	}
	row := exact[modelOpsCostKey("media-a", 1)]
	if row.request == nil || *row.request != requestA || row.second == nil || *row.second != secondA {
		t.Fatalf("expected exact request/second costs, got %#v", row)
	}
}

func TestModelOpsModelChannelCostsNormalizesVariantNames(t *testing.T) {
	db := setupModelOpsControllerTestDB(t)
	input := 3.5
	if err := db.Create(&model.ProviderCostPrice{
		ModelName:      "gpt-4o-gizmo-*",
		Provider:       "test",
		ChannelID:      1,
		InputPerMtok:   &input,
		PricingVersion: 4,
	}).Error; err != nil {
		t.Fatalf("create wildcard provider cost: %v", err)
	}

	costs, err := modelOpsModelChannelCosts([]string{"gpt-4o-gizmo-custom"}, []int{1})
	if err != nil {
		t.Fatalf("build normalized model-channel costs: %v", err)
	}
	cost := costs[modelOpsCostKey("gpt-4o-gizmo-custom", 1)]
	if cost.input == nil || *cost.input != input {
		t.Fatalf("expected normalized wildcard cost %v, got %#v", input, cost)
	}
}

// The provider ledger shifts a failing upstream's traffic off every model it
// serves at once. That has to be all-or-nothing: a half-applied move leaves the
// pool in a state the operator did not ask for and was not told about.
func TestBatchUpdateModelOpsPoolWeightIsAtomic(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := setupModelOpsControllerTestDB(t)

	channel := model.Channel{Id: 41, Name: "reseller", Status: common.ChannelStatusEnabled}
	if err := db.Create(&channel).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	abilities := []model.Ability{
		{Group: model.ChannelLaneAgent, Model: "deepseek-v4-pro", ChannelId: 41, Enabled: true, Weight: 70},
		{Group: model.ChannelLaneAgent, Model: "glm-5.3", ChannelId: 41, Enabled: true, Weight: 40},
	}
	if err := db.Create(&abilities).Error; err != nil {
		t.Fatalf("create abilities: %v", err)
	}

	call := func(body string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(
			http.MethodPut, "/api/model_ops/pool_weight/batch", strings.NewReader(body),
		)
		c.Request.Header.Set("Content-Type", "application/json")
		BatchUpdateModelOpsPoolWeight(c)
		return recorder
	}

	weightOf := func(modelName string) uint {
		t.Helper()
		var ability model.Ability
		if err := db.Where("model = ? AND channel_id = ?", modelName, 41).
			First(&ability).Error; err != nil {
			t.Fatalf("read ability %s: %v", modelName, err)
		}
		return ability.Weight
	}

	// One unknown route in the set must roll the whole batch back.
	recorder := call(`{"channel_id":"41","weight":0,"routes":[
		{"lane":"cheap","model":"deepseek-v4-pro"},
		{"lane":"cheap","model":"not-served-here"}
	]}`)
	if recorder.Code == http.StatusOK &&
		!strings.Contains(recorder.Body.String(), `"success":false`) {
		t.Fatalf("partial batch was accepted: %s", recorder.Body.String())
	}
	if got := weightOf("deepseek-v4-pro"); got != 70 {
		t.Fatalf("weight = %d after a failed batch, want the original 70", got)
	}

	// A batch naming only real routes applies to all of them.
	recorder = call(`{"channel_id":"41","weight":0,"routes":[
		{"lane":"cheap","model":"deepseek-v4-pro"},
		{"lane":"cheap","model":"glm-5.3"}
	]}`)
	if recorder.Code != http.StatusOK ||
		!strings.Contains(recorder.Body.String(), `"success":true`) {
		t.Fatalf("valid batch rejected: %d %s", recorder.Code, recorder.Body.String())
	}
	if got := weightOf("deepseek-v4-pro"); got != 0 {
		t.Fatalf("deepseek weight = %d, want 0", got)
	}
	if got := weightOf("glm-5.3"); got != 0 {
		t.Fatalf("glm weight = %d, want 0", got)
	}
}

// Taking an upstream out of rotation is a narrow, explicit act: it flips one
// channel's status and nothing else. Routing it through the full edit form
// would risk writing back whatever unrelated fields the caller happened to
// hold, which is exactly what the ledger must not do.
func TestUpdateChannelStatusByIdRejectsUnknownStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := setupModelOpsControllerTestDB(t)
	if err := db.Create(&model.Channel{
		Id: 77, Name: "reseller", Status: common.ChannelStatusEnabled,
	}).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}

	call := func(body string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Params = gin.Params{{Key: "id", Value: "77"}}
		c.Request = httptest.NewRequest(
			http.MethodPut, "/api/channel/77/status", strings.NewReader(body),
		)
		c.Request.Header.Set("Content-Type", "application/json")
		UpdateChannelStatusById(c)
		return recorder
	}

	// Auto-disabled is the router's own verdict, not something an operator
	// asserts by hand, so only enable/manual-disable are accepted.
	if body := call(`{"status":3}`).Body.String(); !strings.Contains(body, `"success":false`) {
		t.Fatalf("auto-disabled status was accepted: %s", body)
	}
	if body := call(`{}`).Body.String(); !strings.Contains(body, `"success":false`) {
		t.Fatalf("missing status was accepted: %s", body)
	}

	var channel model.Channel
	if err := db.First(&channel, 77).Error; err != nil {
		t.Fatalf("reload channel: %v", err)
	}
	if channel.Status != common.ChannelStatusEnabled {
		t.Fatalf("status = %d after rejected requests, want it untouched", channel.Status)
	}
}

func TestUpdateChannelStatusByIdStopsWholeMultiKeyChannel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := setupModelOpsControllerTestDB(t)
	if err := db.Create(&model.Channel{
		Id: 78, Name: "multi-key-upstream", Key: "first\nsecond",
		Status: common.ChannelStatusEnabled,
		ChannelInfo: model.ChannelInfo{
			IsMultiKey: true, MultiKeySize: 2,
		},
	}).Error; err != nil {
		t.Fatalf("create multi-key channel: %v", err)
	}

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Params = gin.Params{{Key: "id", Value: "78"}}
	c.Request = httptest.NewRequest(
		http.MethodPut, "/api/channel/78/status",
		strings.NewReader(`{"status":2}`),
	)
	c.Request.Header.Set("Content-Type", "application/json")
	UpdateChannelStatusById(c)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"success":true`) {
		t.Fatalf("multi-key pause rejected: %d %s", recorder.Code, recorder.Body.String())
	}

	var channel model.Channel
	if err := db.First(&channel, 78).Error; err != nil {
		t.Fatalf("reload multi-key channel: %v", err)
	}
	if channel.Status != common.ChannelStatusManuallyDisabled {
		t.Fatalf("channel status = %d, want whole upstream manually disabled", channel.Status)
	}
	if len(channel.ChannelInfo.MultiKeyStatusList) != 0 {
		t.Fatalf("whole-channel pause must not masquerade as a per-key update: %+v", channel.ChannelInfo.MultiKeyStatusList)
	}
}

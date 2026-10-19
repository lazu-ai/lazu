package controller

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/types"
	"gorm.io/gorm"
)

// Regression tests for the models / model-ops / pricing domain: every failure
// leaves with its real status and a catalog code, never a 200 with prose.

type errorEnvelope struct {
	Success *bool `json:"success"`
	Error   struct {
		Code    string         `json:"code"`
		Type    string         `json:"type"`
		Message string         `json:"message"`
		Lazu    string         `json:"lazu"`
		Details map[string]any `json:"details"`
	} `json:"error"`
}

func setupModelsErrorTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db := setupModelOpsControllerTestDB(t)
	if err := db.AutoMigrate(
		&model.User{}, &model.Model{}, &model.ModelMetadata{}, &model.Vendor{}, &model.PrefillGroup{},
		&model.ModelSellPrice{}, &model.Token{}, &model.AdminAuditLog{},
	); err != nil {
		t.Fatalf("migrate models error test database: %v", err)
	}
	return db
}

type handlerCall struct {
	method string
	target string
	body   string
	params gin.Params
	setup  func(c *gin.Context)
}

func callModelsHandler(handler gin.HandlerFunc, call handlerCall) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Set("role", common.RoleRootUser)
	var reader *strings.Reader
	if call.body != "" {
		reader = strings.NewReader(call.body)
	} else {
		reader = strings.NewReader("")
	}
	c.Request = httptest.NewRequest(call.method, call.target, reader)
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = call.params
	if call.setup != nil {
		call.setup(c)
	}
	handler(c)
	return recorder
}

func assertCatalogError(t *testing.T, recorder *httptest.ResponseRecorder, status int, code types.ErrorCode) errorEnvelope {
	t.Helper()
	var body errorEnvelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %q: %v", recorder.Body.String(), err)
	}
	if recorder.Code != status || body.Error.Code != string(code) {
		t.Fatalf("got %d %s, want %d %s", recorder.Code, recorder.Body.String(), status, code)
	}
	if body.Error.Lazu == "" || recorder.Header().Get("X-Lazu-Error") != body.Error.Lazu {
		t.Fatalf("missing LZ number: %s", recorder.Body.String())
	}
	for _, r := range body.Error.Message {
		if r >= 0x4e00 && r <= 0x9fff {
			t.Fatalf("CJK text in the wire message: %s", body.Error.Message)
		}
	}
	return body
}

func TestModelOpsPoolWeightErrors(t *testing.T) {
	db := setupModelsErrorTestDB(t)
	if err := db.Create(&model.Channel{Id: 7, Name: "src", Status: common.ChannelStatusEnabled}).Error; err != nil {
		t.Fatal(err)
	}

	unknownLane := callModelsHandler(UpdateModelOpsPoolWeight, handlerCall{
		method: http.MethodPut, target: "/api/model_ops/pool_weight",
		body: `{"channel_id":"7","lane":"nope","model":"m","weight":1}`,
	})
	assertCatalogError(t, unknownLane, http.StatusNotFound, types.ErrorCodeLaneNotFound)

	notServed := callModelsHandler(UpdateModelOpsPoolWeight, handlerCall{
		method: http.MethodPut, target: "/api/model_ops/pool_weight",
		body: `{"channel_id":"7","lane":"direct","model":"m","weight":1}`,
	})
	body := assertCatalogError(t, notServed, http.StatusNotFound, types.ErrorCodeChannelRouteNotFound)
	if body.Error.Details["model"] != "m" || body.Error.Details["lane"] != model.ChannelLaneDirect {
		t.Fatalf("details: %+v", body.Error.Details)
	}

	badWeight := callModelsHandler(UpdateModelOpsPoolWeight, handlerCall{
		method: http.MethodPut, target: "/api/model_ops/pool_weight",
		body: `{"channel_id":"7","lane":"direct","model":"m","weight":-1}`,
	})
	assertCatalogError(t, badWeight, http.StatusBadRequest, types.ErrorCodeInvalidRequest)

	missingSource := callModelsHandler(UpdateModelOpsPoolWeight, handlerCall{
		method: http.MethodPut, target: "/api/model_ops/pool_weight",
		body: `{"channel_id":"channel_missing","lane":"direct","model":"m","weight":1}`,
	})
	assertCatalogError(t, missingSource, http.StatusNotFound, types.ErrorCodeChannelNotFound)
}

func TestModelOpsListingRefusesUnpricedModels(t *testing.T) {
	db := setupModelsErrorTestDB(t)
	item := model.Model{ModelName: "unpriced-model"}
	if err := db.Create(&item).Error; err != nil {
		t.Fatal(err)
	}
	recorder := callModelsHandler(BatchUpdateModelOpsStatus, handlerCall{
		method: http.MethodPut, target: "/api/model_ops/models/status",
		body: `{"ids":[` + jsonInt(item.Id) + `],"status":1}`,
	})
	body := assertCatalogError(t, recorder, http.StatusConflict, types.ErrorCodeModelListingUnpriced)
	if body.Error.Details["models"] != "unpriced-model" {
		t.Fatalf("details: %+v", body.Error.Details)
	}

	empty := callModelsHandler(BatchUpdateModelOpsStatus, handlerCall{
		method: http.MethodPut, target: "/api/model_ops/models/status", body: `{"ids":[],"status":1}`,
	})
	assertCatalogError(t, empty, http.StatusBadRequest, types.ErrorCodeMissingRequiredParameter)
}

func TestModelOpsLaneErrors(t *testing.T) {
	setupModelsErrorTestDB(t)

	unknown := callModelsHandler(DeleteChannelLane, handlerCall{
		method: http.MethodDelete, target: "/api/model_ops/lanes/nope", params: gin.Params{{Key: "slug", Value: "nope"}},
	})
	assertCatalogError(t, unknown, http.StatusNotFound, types.ErrorCodeLaneNotFound)

	builtin := callModelsHandler(DeleteChannelLane, handlerCall{
		method: http.MethodDelete, target: "/api/model_ops/lanes/direct", params: gin.Params{{Key: "slug", Value: model.ChannelLaneDirect}},
	})
	assertCatalogError(t, builtin, http.StatusConflict, types.ErrorCodeLaneBuiltin)

	enableUnknown := callModelsHandler(SetChannelLaneEnabled, handlerCall{
		method: http.MethodPut, target: "/api/model_ops/lanes/nope/enabled", body: `{"enabled":true}`,
		params: gin.Params{{Key: "slug", Value: "nope"}},
	})
	assertCatalogError(t, enableUnknown, http.StatusNotFound, types.ErrorCodeLaneNotFound)
}

func TestModelMetaVendorAndPrefillErrors(t *testing.T) {
	db := setupModelsErrorTestDB(t)
	if err := db.Create(&model.Model{ModelName: "taken-model"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Vendor{Name: "Acme"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.PrefillGroup{Name: "grp", Type: "model"}).Error; err != nil {
		t.Fatal(err)
	}

	dupModel := callModelsHandler(CreateModelMeta, handlerCall{
		method: http.MethodPost, target: "/api/models/", body: `{"model_name":"taken-model"}`,
	})
	assertCatalogError(t, dupModel, http.StatusConflict, types.ErrorCodeModelNameTaken)

	missingModel := callModelsHandler(GetModelMeta, handlerCall{
		method: http.MethodGet, target: "/api/models/999", params: gin.Params{{Key: "id", Value: "999"}},
	})
	assertCatalogError(t, missingModel, http.StatusNotFound, types.ErrorCodeResourceNotFound)

	noVendorName := callModelsHandler(CreateVendorMeta, handlerCall{
		method: http.MethodPost, target: "/api/vendors/", body: `{"name":""}`,
	})
	assertCatalogError(t, noVendorName, http.StatusBadRequest, types.ErrorCodeMissingRequiredParameter)

	dupVendor := callModelsHandler(CreateVendorMeta, handlerCall{
		method: http.MethodPost, target: "/api/vendors/", body: `{"name":"Acme"}`,
	})
	assertCatalogError(t, dupVendor, http.StatusConflict, types.ErrorCodeVendorNameTaken)

	unknownVendor := callModelsHandler(GetVendorMeta, handlerCall{
		method: http.MethodGet, target: "/api/vendors/vendor_missing", params: gin.Params{{Key: "id", Value: "vendor_missing"}},
	})
	assertCatalogError(t, unknownVendor, http.StatusNotFound, types.ErrorCodeResourceNotFound)

	dupGroup := callModelsHandler(CreatePrefillGroup, handlerCall{
		method: http.MethodPost, target: "/api/prefill_group/", body: `{"name":"grp","type":"model"}`,
	})
	assertCatalogError(t, dupGroup, http.StatusConflict, types.ErrorCodePrefillGroupNameTaken)
}

func TestPricingErrors(t *testing.T) {
	db := setupModelsErrorTestDB(t)

	noName := callModelsHandler(DeleteModelSellPrice, handlerCall{method: http.MethodDelete, target: "/api/pricing/sell_prices"})
	assertCatalogError(t, noName, http.StatusBadRequest, types.ErrorCodeMissingRequiredParameter)

	listed := model.Model{ModelName: "listed-model", Status: 1}
	if err := db.Create(&listed).Error; err != nil {
		t.Fatal(err)
	}
	held := callModelsHandler(DeleteModelSellPrice, handlerCall{
		method: http.MethodDelete, target: "/api/pricing/sell_prices?model_name=listed-model",
	})
	if held.Code == http.StatusOK {
		t.Fatalf("a listed model's price was removed: %s", held.Body.String())
	}
	var heldBody errorEnvelope
	_ = json.Unmarshal(held.Body.Bytes(), &heldBody)
	if !strings.HasPrefix(heldBody.Error.Code, "sell_price_model_") || held.Code != http.StatusConflict {
		t.Fatalf("expected a sell_price_model_* conflict, got %d %s", held.Code, held.Body.String())
	}

	noPrice := callModelsHandler(UpsertModelSellPrice, handlerCall{
		method: http.MethodPut, target: "/api/pricing/sell_prices", body: `{"model_name":"m","channel_group":"direct"}`,
	})
	assertCatalogError(t, noPrice, http.StatusBadRequest, types.ErrorCodeSellPriceMissing)

	badID := callModelsHandler(UpdatePricePlan, handlerCall{
		method: http.MethodPut, target: "/api/price-plans/x", body: `{}`, params: gin.Params{{Key: "id", Value: "x"}},
	})
	assertCatalogError(t, badID, http.StatusBadRequest, types.ErrorCodeInvalidRequest)
}

func TestModelCatalogAndCommunityErrors(t *testing.T) {
	setupModelsErrorTestDB(t)

	// /v1 control-plane routes answer in the OpenAI envelope with a real status.
	emptyID := callModelsHandler(RetrieveModelDiscovery, handlerCall{method: http.MethodGet, target: "/v1/models/"})
	body := assertCatalogError(t, emptyID, http.StatusNotFound, types.ErrorCodeModelNotFound)
	if body.Success != nil {
		t.Fatalf("control-plane error carried the console envelope: %s", emptyID.Body.String())
	}

	brokenScope := callModelsHandler(func(c *gin.Context) { RetrieveModel(c, constant.ChannelTypeAnthropic) }, handlerCall{
		method: http.MethodGet, target: "/v1/models/x", params: gin.Params{{Key: "model", Value: "x"}},
		setup: func(c *gin.Context) { c.Set(string(constant.ContextKeyTokenSpecificChannelId), "not-a-number") },
	})
	body = assertCatalogError(t, brokenScope, http.StatusInternalServerError, types.ErrorCodeInternalError)
	if strings.Contains(body.Error.Message, "strconv") || strings.Contains(body.Error.Message, "not-a-number") {
		t.Fatalf("internal cause leaked: %s", body.Error.Message)
	}

	unknownCommunity := callModelsHandler(GetCommunityModelByID, handlerCall{
		method: http.MethodGet, target: "/api/community/models/nope", params: gin.Params{{Key: "id", Value: "nope"}},
	})
	assertCatalogError(t, unknownCommunity, http.StatusNotFound, types.ErrorCodeModelNotFound)
}

func TestModelRankingAndMissingModelsHideDatabaseErrors(t *testing.T) {
	db := setupModelsErrorTestDB(t)
	// Missing-models reads the enabled abilities, then the models table.
	if err := db.Create(&model.Ability{Group: model.ChannelLaneDirect, Model: "orphan-model", ChannelId: 1, Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Migrator().DropTable(&model.Model{}, &model.ModelMetadata{}); err != nil {
		t.Fatal(err)
	}
	for name, handler := range map[string]gin.HandlerFunc{"missing": GetMissingModels, "rankings": GetModelRankings} {
		recorder := callModelsHandler(handler, handlerCall{method: http.MethodGet, target: "/api/models/" + name})
		body := assertCatalogError(t, recorder, http.StatusInternalServerError, types.ErrorCodeInternalError)
		if strings.Contains(strings.ToLower(body.Error.Message), "no such table") {
			t.Fatalf("%s leaked the database error: %s", name, body.Error.Message)
		}
	}
}

func TestModelSyncErrors(t *testing.T) {
	setupModelsErrorTestDB(t)

	for name, handler := range map[string]gin.HandlerFunc{"litellm": SyncLiteLLM, "openrouter": SyncOpenRouter} {
		recorder := callModelsHandler(handler, handlerCall{method: http.MethodPost, target: "/api/models/sync_" + name, body: `{`})
		assertCatalogError(t, recorder, http.StatusBadRequest, types.ErrorCodeInvalidRequest)
	}

	noKey := callModelsHandler(SyncOpenRouter, handlerCall{
		method: http.MethodPost, target: "/api/models/sync_openrouter", body: `{"translate":true,"model":"gpt-x"}`,
		setup: func(c *gin.Context) { c.Set("id", 42) },
	})
	body := assertCatalogError(t, noKey, http.StatusBadRequest, types.ErrorCodeTranslationTokenMissing)
	if body.Error.Details["model"] != "gpt-x" {
		t.Fatalf("details: %+v", body.Error.Details)
	}
}

func TestResolveIDParamErrorsAreCoded(t *testing.T) {
	setupModelsErrorTestDB(t)
	if _, err := resolveIDParam("vendors", "  "); !types.IsErrCode(err, types.ErrorCodeMissingRequiredParameter) {
		t.Fatalf("blank id: %v", err)
	}
	if _, err := resolveIDParam("vendors", "vendor_missing"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("unknown id: %v", err)
	}
	if _, err := resolveIDRaw("vendors", json.RawMessage(`{}`)); !types.IsErrCode(err, types.ErrorCodeInvalidRequest) {
		t.Fatalf("malformed id: %v", err)
	}
}

func jsonInt(value int) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}

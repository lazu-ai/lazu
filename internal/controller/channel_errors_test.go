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
)

type channelErrorBody struct {
	Success bool `json:"success"`
	Error   struct {
		Code    string         `json:"code"`
		Lazu    string         `json:"lazu"`
		Details map[string]any `json:"details"`
	} `json:"error"`
}

// callChannelHandler runs one console handler and returns its recorder.
func callChannelHandler(
	t *testing.T, handler gin.HandlerFunc, method, target, body string, params gin.Params,
) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Set("role", common.RoleRootUser)
	c.Params = params
	c.Request = httptest.NewRequest(method, target, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	handler(c)
	return recorder
}

// assertChannelError checks a failure's real status and catalog code.
func assertChannelError(t *testing.T, recorder *httptest.ResponseRecorder, status int, code types.ErrorCode) channelErrorBody {
	t.Helper()
	var body channelErrorBody
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %s: %v", recorder.Body.String(), err)
	}
	if recorder.Code != status || body.Success || body.Error.Code != string(code) {
		t.Fatalf("got %d %s, want %d %s", recorder.Code, recorder.Body.String(), status, code)
	}
	if body.Error.Lazu == "" {
		t.Fatalf("error %s carries no LZ number: %s", code, recorder.Body.String())
	}
	if strings.ContainsAny(recorder.Body.String(), "渠道密钥档位") {
		t.Fatalf("response carries Chinese prose: %s", recorder.Body.String())
	}
	return body
}

func TestManageMultiKeysAnswersWithCatalogCodes(t *testing.T) {
	db := setupModelOpsControllerTestDB(t)
	single := model.Channel{Id: 51, PublicID: "single-key", Key: "sk-one", Name: "single", Type: constant.ChannelTypeOpenAI}
	multi := model.Channel{Id: 52, PublicID: "multi-key", Key: "sk-a\nsk-b", Name: "multi", Type: constant.ChannelTypeOpenAI}
	multi.ChannelInfo.IsMultiKey = true
	multi.ChannelInfo.MultiKeySize = 2
	if err := db.Create(&[]model.Channel{single, multi}).Error; err != nil {
		t.Fatalf("create channels: %v", err)
	}
	manage := func(body string) *httptest.ResponseRecorder {
		return callChannelHandler(t, ManageMultiKeys, http.MethodPost, "/api/channel/multi_key/manage", body, nil)
	}

	assertChannelError(t, manage(`{"channel_id":"missing","action":"get_key_status"}`),
		http.StatusNotFound, types.ErrorCodeChannelNotFound)
	assertChannelError(t, manage(`{"channel_id":"single-key","action":"disable_key","key_index":0}`),
		http.StatusConflict, types.ErrorCodeChannelNotMultiKey)
	body := assertChannelError(t, manage(`{"channel_id":"multi-key","action":"disable_key","key_index":5}`),
		http.StatusBadRequest, types.ErrorCodeChannelKeyIndexInvalid)
	if body.Error.Details["key_count"] != float64(2) {
		t.Fatalf("key_count detail = %v, want 2", body.Error.Details["key_count"])
	}
	body = assertChannelError(t, manage(`{"channel_id":"multi-key","action":"enable_key"}`),
		http.StatusBadRequest, types.ErrorCodeMissingRequiredParameter)
	if body.Error.Details["action"] != "enable_key" || body.Error.Details["field"] != "key_index" {
		t.Fatalf("missing index details = %v", body.Error.Details)
	}
	assertChannelError(t, manage(`{"channel_id":"multi-key","action":"delete_disabled_keys"}`),
		http.StatusConflict, types.ErrorCodeChannelKeysNoneEligible)
	assertChannelError(t, manage(`{"channel_id":"multi-key","action":"rotate"}`),
		http.StatusBadRequest, types.ErrorCodeInvalidRequest)

	// A success carries data for the console, never prose.
	recorder := manage(`{"channel_id":"multi-key","action":"disable_key","key_index":1}`)
	if recorder.Code != http.StatusOK || strings.Contains(recorder.Body.String(), "密钥") {
		t.Fatalf("disable_key: %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestAddChannelRejectsUnknownLaneAndMode(t *testing.T) {
	setupModelOpsControllerTestDB(t)
	add := func(body string) *httptest.ResponseRecorder {
		return callChannelHandler(t, AddChannel, http.MethodPost, "/api/channel/", body, nil)
	}
	body := assertChannelError(t,
		add(`{"mode":"single","channel":{"name":"x","key":"sk","type":1,"group":"direct,cheap"}}`),
		http.StatusBadRequest, types.ErrorCodeChannelLaneInvalid)
	if body.Error.Details["lane"] != "direct,cheap" {
		t.Fatalf("lane detail = %v", body.Error.Details["lane"])
	}
	assertChannelError(t, add(`{"mode":"single","channel":{"name":"x","type":1,"group":"direct"}}`),
		http.StatusBadRequest, types.ErrorCodeMissingRequiredParameter)
	assertChannelError(t, add(`{"mode":"sideways","channel":{"name":"x","key":"sk","type":1,"group":"direct"}}`),
		http.StatusBadRequest, types.ErrorCodeInvalidRequest)
	assertChannelError(t, add(`{"mode":"single","channel":{"name":"x","key":"sk","type":1,"group":"direct","settings":"{"}}`),
		http.StatusBadRequest, types.ErrorCodeChannelSettingsInvalid)
}

func TestUpdateChannelBalanceRejectsMultiKeyChannel(t *testing.T) {
	db := setupModelOpsControllerTestDB(t)
	multi := model.Channel{Id: 61, PublicID: "multi-balance", Key: "sk-a\nsk-b", Name: "multi", Type: constant.ChannelTypeOpenAI}
	multi.ChannelInfo.IsMultiKey = true
	multi.ChannelInfo.MultiKeySize = 2
	if err := db.Create(&multi).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	previous := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = false
	t.Cleanup(func() { common.MemoryCacheEnabled = previous })

	recorder := callChannelHandler(t, UpdateChannelBalance, http.MethodGet, "/api/channel/update_balance/multi-balance", "",
		gin.Params{{Key: "id", Value: "multi-balance"}})
	body := assertChannelError(t, recorder, http.StatusBadRequest, types.ErrorCodeChannelBalanceUnsupported)
	if body.Error.Details["reason"] != "multi_key" {
		t.Fatalf("reason detail = %v", body.Error.Details["reason"])
	}
}

func TestUpstreamFailureCarriesStatusNotBody(t *testing.T) {
	err := upstreamFailure(&upstreamStatusError{status: http.StatusUnauthorized}, types.ErrorCodeChannelUpstreamModelsFailed)
	var apiErr *types.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != types.ErrorCodeChannelUpstreamModelsFailed || apiErr.Details["status"] != http.StatusUnauthorized {
		t.Fatalf("upstream failure = %#v", err)
	}
	if got := upstreamFailure(errBalanceUnsupported, types.ErrorCodeChannelBalanceQueryFailed); !types.IsErrCode(got, types.ErrorCodeChannelBalanceUnsupported) {
		t.Fatalf("a coded cause must keep its code, got %v", got)
	}
}

func TestChannelTestAnswersWithCatalogCodes(t *testing.T) {
	setupModelOpsControllerTestDB(t)
	previous := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = false
	t.Cleanup(func() { common.MemoryCacheEnabled = previous })

	recorder := callChannelHandler(t, TestChannel, http.MethodGet, "/api/channel/test/missing", "",
		gin.Params{{Key: "id", Value: "missing"}})
	assertChannelError(t, recorder, http.StatusNotFound, types.ErrorCodeChannelNotFound)

	releaseAllChannelsTest()
	if err := acquireAllChannelsTest(); err != nil {
		t.Fatalf("acquire test lease: %v", err)
	}
	t.Cleanup(releaseAllChannelsTest)
	recorder = callChannelHandler(t, TestAllChannels, http.MethodGet, "/api/channel/test", "", nil)
	body := assertChannelError(t, recorder, http.StatusConflict, types.ErrorCodeChannelTaskRunning)
	if body.Error.Details["task"] != "channel_test" {
		t.Fatalf("task detail = %v", body.Error.Details["task"])
	}
}

func TestChannelTestFailureSeparatesProviderFromLocal(t *testing.T) {
	upstream := types.NewOpenAIError(errors.New("401 from provider"), types.ErrorCodeChannelInvalidKey, http.StatusUnauthorized)
	failed := channelTestFailure(testResult{localErr: upstream, lazuErr: upstream, providerAttempted: true}, 1.5)
	var apiErr *types.APIError
	if !errors.As(failed, &apiErr) || apiErr.Code != types.ErrorCodeChannelTestFailed {
		t.Fatalf("provider failure = %#v", failed)
	}
	if apiErr.Details["cause_code"] != string(types.ErrorCodeUpstreamAuthFailed) {
		t.Fatalf("cause_code = %v", apiErr.Details["cause_code"])
	}

	notSent := channelTestFailure(testResult{localErr: errors.New(`unknown endpoint_type "x"`)}, 0)
	if !types.IsErrCode(notSent, types.ErrorCodeChannelTestNotSent) {
		t.Fatalf("local failure = %v", notSent)
	}
	if channelTestFailure(testResult{}, 0) != nil {
		t.Fatal("a passing test is not a failure")
	}
}

func TestChannelHealthRoutesAnswerWithCatalogCodes(t *testing.T) {
	db := setupModelOpsControllerTestDB(t)
	if err := db.Create(&model.Channel{
		Id: 71, PublicID: "route-source", Key: "sk", Name: "route", Type: constant.ChannelTypeOpenAI,
		Status: common.ChannelStatusEnabled, Group: model.ChannelLaneDirect,
	}).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	role := func(body string) *httptest.ResponseRecorder {
		return callChannelHandler(t, UpdateChannelHealthRouteRole, http.MethodPut, "/api/channel_health/route_role", body, nil)
	}
	assertChannelError(t, role(`{"channel_id":"route-source","lane":"moon","model":"gpt-x","role":"primary"}`),
		http.StatusBadRequest, types.ErrorCodeChannelLaneInvalid)
	assertChannelError(t, role(`{"channel_id":"route-source","lane":"direct","model":"","role":"primary"}`),
		http.StatusBadRequest, types.ErrorCodeMissingRequiredParameter)
	assertChannelError(t, role(`{"channel_id":"route-source","lane":"direct","model":"gpt-x","role":"captain"}`),
		http.StatusBadRequest, types.ErrorCodeInvalidRequest)
	assertChannelError(t, role(`{"channel_id":"route-source","lane":"direct","model":"gpt-x","role":"primary"}`),
		http.StatusNotFound, types.ErrorCodeChannelRouteNotFound)

	retreat := callChannelHandler(t, ClearChannelHealthRouteRetreat, http.MethodPost,
		"/api/channel_health/route_retreat/clear", `{"channel_id":"nobody","lane":"direct","model":"gpt-x"}`, nil)
	assertChannelError(t, retreat, http.StatusNotFound, types.ErrorCodeChannelNotFound)
}

func TestProbeChannelForHealthRejectsUnservedRoute(t *testing.T) {
	db := setupModelOpsControllerTestDB(t)
	if err := db.Create(&model.Channel{
		Id: 81, PublicID: "probe-source", Key: "sk", Name: "probe", Type: constant.ChannelTypeOpenAI,
		Status: common.ChannelStatusEnabled, Group: model.ChannelLaneDirect,
	}).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	probe := func(body string) *httptest.ResponseRecorder {
		return callChannelHandler(t, ProbeChannelForHealth, http.MethodPost, "/api/channel/probe-source/probe", body,
			gin.Params{{Key: "id", Value: "probe-source"}})
	}
	assertChannelError(t, probe(`{"model_name":"gpt-x","lane":"moon"}`),
		http.StatusBadRequest, types.ErrorCodeChannelLaneInvalid)
	assertChannelError(t, probe(`{"model_name":"gpt-x","lane":"direct"}`),
		http.StatusNotFound, types.ErrorCodeChannelRouteNotFound)
}

func TestGetChannelHealthHidesStorageFailure(t *testing.T) {
	db := setupModelOpsControllerTestDB(t)
	InvalidateChannelHealthCache()
	t.Cleanup(InvalidateChannelHealthCache)
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("database handle: %v", err)
	}
	_ = sqlDB.Close()

	recorder := callChannelHandler(t, GetChannelHealth, http.MethodGet, "/api/channel_health?window=24h", "", nil)
	assertChannelError(t, recorder, http.StatusInternalServerError, types.ErrorCodeInternalError)
	if strings.Contains(recorder.Body.String(), "sql") {
		t.Fatalf("storage error text leaked: %s", recorder.Body.String())
	}
}

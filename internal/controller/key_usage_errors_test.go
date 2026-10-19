package controller

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/httperr"
	"github.com/lazu-ai/lazu/internal/model"
)

type keyUsageErrorBody struct {
	Success *bool `json:"success"`
	Error   struct {
		Type    string         `json:"type"`
		Code    string         `json:"code"`
		Lazu    string         `json:"lazu"`
		Param   string         `json:"param"`
		Details map[string]any `json:"details"`
	} `json:"error"`
}

// serveKeyUsage runs one handler behind a route and decodes the error body.
func serveKeyUsage(t *testing.T, method, route, target string, body []byte, handler gin.HandlerFunc, setup ...gin.HandlerFunc) (*httptest.ResponseRecorder, keyUsageErrorBody) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	handlers := append(append([]gin.HandlerFunc{}, setup...), handler)
	engine.Handle(method, route, handlers...)
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		reader = bytes.NewReader(body)
	}
	req := httptest.NewRequest(method, target, reader)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	var decoded keyUsageErrorBody
	if err := json.Unmarshal(w.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode %s: %v", w.Body.String(), err)
	}
	return w, decoded
}

func assertKeyUsageError(t *testing.T, w *httptest.ResponseRecorder, body keyUsageErrorBody, status int, code string, console bool) {
	t.Helper()
	if w.Code != status {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, status, w.Body.String())
	}
	if body.Error.Code != code {
		t.Fatalf("error.code = %q, want %q; body=%s", body.Error.Code, code, w.Body.String())
	}
	if body.Error.Lazu == "" || w.Header().Get(httperr.HeaderLazuError) != body.Error.Lazu {
		t.Fatalf("missing LZ number: body=%s header=%q", w.Body.String(), w.Header().Get(httperr.HeaderLazuError))
	}
	if console && (body.Success == nil || *body.Success) {
		t.Fatalf("console envelope must carry success:false: %s", w.Body.String())
	}
	if !console && body.Success != nil {
		t.Fatalf("OpenAI envelope must not carry success: %s", w.Body.String())
	}
}

func TestInspectTokenUsageWithoutSessionIsSessionRequired(t *testing.T) {
	w, body := serveKeyUsage(t, http.MethodPost, "/api/token/inspect", "/api/token/inspect",
		[]byte(`{"api_key":"sk-anything"}`), InspectTokenUsage)
	assertKeyUsageError(t, w, body, http.StatusUnauthorized, "session_required", true)
}

func TestPublicInspectTokenUsageBlankKeyIsInvalidField(t *testing.T) {
	w, body := serveKeyUsage(t, http.MethodPost, "/api/public/token/inspect", "/api/public/token/inspect",
		[]byte(`{"api_key":"   "}`), PublicInspectTokenUsage)
	assertKeyUsageError(t, w, body, http.StatusBadRequest, "invalid_request", true)
	if body.Error.Param != "api_key" {
		t.Fatalf("param = %q, want api_key", body.Error.Param)
	}
}

func TestGetTokenUsageWithoutBearerIsInvalidAPIKey(t *testing.T) {
	w, body := serveKeyUsage(t, http.MethodGet, "/api/usage/token/", "/api/usage/token/", nil, GetTokenUsage)
	assertKeyUsageError(t, w, body, http.StatusUnauthorized, "invalid_api_key", true)
}

func TestGetLogByKeyWithoutSessionIsSessionRequired(t *testing.T) {
	w, body := serveKeyUsage(t, http.MethodGet, "/api/log/token", "/api/log/token?key=sk-x", nil, GetLogByKey)
	assertKeyUsageError(t, w, body, http.StatusUnauthorized, "session_required", true)
}

func TestDeleteHistoryLogsRequiresTargetTimestamp(t *testing.T) {
	w, body := serveKeyUsage(t, http.MethodDelete, "/api/log/", "/api/log/", nil, DeleteHistoryLogs)
	assertKeyUsageError(t, w, body, http.StatusBadRequest, "invalid_request", true)
	if body.Error.Param != "target_timestamp" {
		t.Fatalf("param = %q, want target_timestamp", body.Error.Param)
	}
}

func TestLogSessionDetailBlankIDIsInvalidField(t *testing.T) {
	w, body := serveKeyUsage(t, http.MethodGet, "/api/log/sessions/:session_id", "/api/log/sessions/%20", nil, GetAllLogSessionDetail)
	assertKeyUsageError(t, w, body, http.StatusBadRequest, "invalid_request", true)
	if body.Error.Param != "session_id" {
		t.Fatalf("param = %q, want session_id", body.Error.Param)
	}
}

func TestExportUserUsageRejectsUnknownFormat(t *testing.T) {
	w, body := serveKeyUsage(t, http.MethodGet, "/api/data/export", "/api/data/export?format=pdf", nil, ExportUserUsage)
	assertKeyUsageError(t, w, body, http.StatusBadRequest, "usage_export_format_unsupported", true)
	if body.Error.Details["format"] != "pdf" {
		t.Fatalf("details = %#v", body.Error.Details)
	}
}

func TestGetUserUsageDataRejectsWindowOverThirtyDays(t *testing.T) {
	w, body := serveKeyUsage(t, http.MethodGet, "/api/data/self", "/api/data/self?start_timestamp=0&end_timestamp=5000000", nil, GetUserUsageData)
	assertKeyUsageError(t, w, body, http.StatusBadRequest, "usage_range_too_long", true)
	if body.Error.Details["max_days"] != float64(usageDataMaxDays) {
		t.Fatalf("details = %#v", body.Error.Details)
	}
}

func TestGenerateMonthlyReportRejectsMonthOutOfRange(t *testing.T) {
	w, body := serveKeyUsage(t, http.MethodPost, "/api/reports/monthly/generate", "/api/reports/monthly/generate?year=2026&month=13", nil, GenerateMonthlyReport)
	assertKeyUsageError(t, w, body, http.StatusBadRequest, "invalid_request", true)
	if body.Error.Param != "month" {
		t.Fatalf("param = %q, want month", body.Error.Param)
	}
}

func TestDeleteSpendBudgetRejectsMissingID(t *testing.T) {
	w, body := serveKeyUsage(t, http.MethodDelete, "/api/admin/spend-budgets/:id", "/api/admin/spend-budgets/abc", nil, DeleteSpendBudget)
	assertKeyUsageError(t, w, body, http.StatusBadRequest, "invalid_request", true)
}

func TestSaveSpendBudgetUnknownIDIsNotFound(t *testing.T) {
	setupSpendBudgetControllerTestDB(t)
	payload, _ := json.Marshal(spendBudgetPayload{Scope: model.SpendBudgetScopeOrg, LimitMicrousd: 1_000})
	w, body := serveKeyUsage(t, http.MethodPut, "/api/admin/spend-budgets/:id", "/api/admin/spend-budgets/404", payload, SaveSpendBudget)
	assertKeyUsageError(t, w, body, http.StatusNotFound, "spend_budget_not_found", true)
}

func TestSaveSpendBudgetValidationNamesTheField(t *testing.T) {
	setupSpendBudgetControllerTestDB(t)
	enabled := true
	payload, _ := json.Marshal(spendBudgetPayload{Scope: model.SpendBudgetScopeOrg, LimitMicrousd: 0, Enabled: &enabled})
	w, body := serveKeyUsage(t, http.MethodPost, "/api/admin/spend-budgets", "/api/admin/spend-budgets", payload, SaveSpendBudget)
	assertKeyUsageError(t, w, body, http.StatusBadRequest, "invalid_request", true)
	if body.Error.Param != "limit_microusd" {
		t.Fatalf("param = %q, want limit_microusd", body.Error.Param)
	}
}

func TestGetUsageSummaryRejectsUnknownGranularity(t *testing.T) {
	setIdentity := func(c *gin.Context) {
		c.Set("token_id", 7)
		c.Set("id", 3)
	}
	w, body := serveKeyUsage(t, http.MethodGet, "/api/usage/summary", "/api/usage/summary?granularity=week", nil, GetUsageSummary, setIdentity)
	assertKeyUsageError(t, w, body, http.StatusBadRequest, "invalid_usage_summary_window", false)
	if body.Error.Param != "granularity" {
		t.Fatalf("param = %q, want granularity", body.Error.Param)
	}
}

func TestGetUsageSummaryWithoutKeyIsInvalidAPIKey(t *testing.T) {
	w, body := serveKeyUsage(t, http.MethodGet, "/api/usage/summary", "/api/usage/summary", nil, GetUsageSummary)
	assertKeyUsageError(t, w, body, http.StatusUnauthorized, "invalid_api_key", false)
}

func TestPlaygroundRejectsAccessTokenInOpenAIEnvelope(t *testing.T) {
	useAccessToken := func(c *gin.Context) { c.Set("use_access_token", true) }
	w, body := serveKeyUsage(t, http.MethodPost, "/pg/chat/completions", "/pg/chat/completions", []byte(`{}`), Playground, useAccessToken)
	assertKeyUsageError(t, w, body, http.StatusForbidden, "access_denied", false)
}

package controller

import (
	"encoding/json"
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

type settingsErrorBody struct {
	Success *bool `json:"success"`
	Error   struct {
		Code    string         `json:"code"`
		Lazu    string         `json:"lazu"`
		Message string         `json:"message"`
		Details map[string]any `json:"details"`
	} `json:"error"`
}

func serveSettingsRequest(t *testing.T, method, route, target, body string, handler gin.HandlerFunc) (*httptest.ResponseRecorder, settingsErrorBody) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set("role", common.RoleRootUser) })
	router.Handle(method, route, handler)
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, target, reader)
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)
	var decoded settingsErrorBody
	if err := json.Unmarshal(recorder.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode %s: %v", recorder.Body.String(), err)
	}
	return recorder, decoded
}

func expectSettingsError(t *testing.T, recorder *httptest.ResponseRecorder, body settingsErrorBody, status int, code string) {
	t.Helper()
	if recorder.Code != status {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, status, recorder.Body.String())
	}
	if body.Error.Code != code {
		t.Fatalf("error.code = %q, want %q: %s", body.Error.Code, code, recorder.Body.String())
	}
	if body.Error.Lazu == "" || recorder.Header().Get("X-Lazu-Error") != body.Error.Lazu {
		t.Fatalf("missing LZ number: %s", recorder.Body.String())
	}
}

func TestUpdateOptionReportsMalformedStructuredSettingWithoutEchoingTheParser(t *testing.T) {
	db, err := gorm.Open(gormsqlite.Open("file:settings_errors_malformed?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Option{}); err != nil {
		t.Fatal(err)
	}
	previous := model.CoreDB()
	model.SetCoreDB(db)
	t.Cleanup(func() { model.SetCoreDB(previous); sqlDB, _ := db.DB(); _ = sqlDB.Close() })
	previousOptions := common.OptionMap
	common.OptionMap = map[string]string{}
	t.Cleanup(func() { common.OptionMap = previousOptions })

	recorder, body := serveSettingsRequest(t, http.MethodPut, "/api/option/", "/api/option/",
		`{"key":"ChannelLanes","value":"{not json"}`, UpdateOption)
	expectSettingsError(t, recorder, body, http.StatusBadRequest, "setting_invalid")
	if body.Error.Details["field"] != "ChannelLanes" || body.Error.Details["rule"] != "format" {
		t.Fatalf("details = %v", body.Error.Details)
	}
	if strings.Contains(recorder.Body.String(), "invalid character") {
		t.Fatalf("parser error leaked: %s", recorder.Body.String())
	}
}

func TestUpdateOptionReportsConsoleListItemsByIndexAndRule(t *testing.T) {
	recorder, body := serveSettingsRequest(t, http.MethodPut, "/api/option/", "/api/option/",
		`{"key":"console_setting.faq","value":"[{\"question\":\"q\",\"answer\":\"\"}]"}`, UpdateOption)
	expectSettingsError(t, recorder, body, http.StatusBadRequest, "setting_invalid")
	details := body.Error.Details
	if details["field"] != "console_setting.faq" || details["rule"] != "required" || details["item"] != "answer" || details["index"] != float64(1) {
		t.Fatalf("details = %v", details)
	}

	items := make([]string, 21)
	for i := range items {
		items[i] = `{\"categoryName\":\"g` + strings.Repeat("x", i) + `\",\"url\":\"https://status.example.com\",\"slug\":\"s\"}`
	}
	recorder, body = serveSettingsRequest(t, http.MethodPut, "/api/option/", "/api/option/",
		`{"key":"console_setting.uptime_kuma_groups","value":"[`+strings.Join(items, ",")+`]"}`, UpdateOption)
	expectSettingsError(t, recorder, body, http.StatusBadRequest, "content_limit_exceeded")
	if body.Error.Details["limit"] != float64(20) {
		t.Fatalf("details = %v", body.Error.Details)
	}
}

func TestAnnouncementHandlersAnswerWithCatalogCodes(t *testing.T) {
	db, err := gorm.Open(gormsqlite.Open("file:settings_errors_announcements?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Announcement{}); err != nil {
		t.Fatal(err)
	}
	previous := model.CoreDB()
	model.SetCoreDB(db)
	t.Cleanup(func() { model.SetCoreDB(previous); sqlDB, _ := db.DB(); _ = sqlDB.Close() })

	recorder, body := serveSettingsRequest(t, http.MethodPut, "/api/admin/announcements/:id", "/api/admin/announcements/4242",
		`{"kind":"operational","title_i18n":{"en":"t"}}`, AdminUpdateAnnouncement)
	expectSettingsError(t, recorder, body, http.StatusNotFound, "announcement_not_found")

	recorder, body = serveSettingsRequest(t, http.MethodPost, "/api/announcements/:id/dismiss", "/api/announcements/abc/dismiss",
		"", DismissAnnouncement)
	expectSettingsError(t, recorder, body, http.StatusBadRequest, "invalid_request")
	if body.Error.Details["field"] != "id" {
		t.Fatalf("details = %v", body.Error.Details)
	}
}

func TestEmailHandlersAnswerWithCatalogCodes(t *testing.T) {
	recorder, body := serveSettingsRequest(t, http.MethodPost, "/api/notification/email/send", "/api/notification/email/send",
		`{"subject":"s","content":"c"}`, SendBroadcastEmail)
	expectSettingsError(t, recorder, body, http.StatusBadRequest, "email_recipients_required")

	recorder, body = serveSettingsRequest(t, http.MethodGet, "/api/verification", "/api/verification?email=not-an-email",
		"", SendEmailVerification)
	expectSettingsError(t, recorder, body, http.StatusBadRequest, "invalid_request")
}

func TestPublicChangelogRejectsABadLimitWithItsField(t *testing.T) {
	recorder, body := serveSettingsRequest(t, http.MethodGet, "/api/public/changelog", "/api/public/changelog?limit=-1",
		"", GetPublicChangelog)
	expectSettingsError(t, recorder, body, http.StatusBadRequest, "invalid_request")
	if body.Error.Details["field"] != "limit" {
		t.Fatalf("details = %v", body.Error.Details)
	}
}

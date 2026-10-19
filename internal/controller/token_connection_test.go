package controller

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
)

func TestConnectOnlyKeepsTokenPolicyAndRejectsStaleVersions(t *testing.T) {
	db := setupMediaJobControllerTestDB(t)
	if err := db.AutoMigrate(&model.Log{}, &model.TokenAuditLog{}, &model.SpendBudgetAlert{}, &model.ProjectInvite{}); err != nil {
		t.Fatal(err)
	}
	oldLog := model.ConfiguredTelemetryDB()
	model.SetTelemetryDB(db)
	t.Cleanup(func() { model.SetTelemetryDB(oldLog) })
	t.Setenv("PROJECTS_ENABLED", "true")
	user := model.User{Username: "connect-owner", Quota: 50000000}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	token := model.Token{UserId: user.Id, Name: "keep", Key: strings.Repeat("c", 48), Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: 900, UsedQuota: 100, VendorGroups: model.TokenVendorGroupMap{"1": "cheap"}}
	if err := db.Create(&token).Error; err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set("id", user.Id) })
	router.PUT("/api/token/", UpdateToken)
	call := func(body string) *httptest.ResponseRecorder {
		r := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPut, "/api/token/?connect_only=true", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(r, req)
		return r
	}
	response := call(fmt.Sprintf(`{"id":"%d","expected_version":%d,"product":"codex","remain_quota":9999,"name":"discard","expired_time":123,"cap_microusd":1}`, token.Id, token.ProjectVersion))
	var body struct{ Success bool }
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || !body.Success {
		t.Fatalf("connect failed: %s", response.Body.String())
	}
	var saved model.Token
	if err := db.First(&saved, token.Id).Error; err != nil {
		t.Fatal(err)
	}
	if saved.Name != "keep" || saved.RemainQuota != 900 || saved.UsedQuota != 100 || saved.ExpiredTime != -1 || saved.VendorGroups["1"] != "cheap" || saved.Product != "codex" {
		t.Fatalf("unrelated fields changed: %+v", saved)
	}
	response = call(fmt.Sprintf(`{"id":"%d","expected_version":%d,"vendor_groups":{}}`, token.Id, token.ProjectVersion))
	if response.Code != http.StatusConflict {
		t.Fatalf("expected conflict: %d %s", response.Code, response.Body.String())
	}
}

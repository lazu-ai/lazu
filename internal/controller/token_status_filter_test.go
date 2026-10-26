package controller

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
)

// The key list and search filter by status before paginating, so a page of
// one disabled key is not crowded out by newer keys in other states, and the
// total counts only matching keys.
func TestTokenListAndSearchFilterStatusBeforePagination(t *testing.T) {
	db := setupMediaJobControllerTestDB(t)
	if err := db.AutoMigrate(&model.Log{}, &model.TokenAuditLog{}, &model.SpendBudgetAlert{}, &model.ProjectInvite{}); err != nil {
		t.Fatal(err)
	}
	oldLog := model.ConfiguredTelemetryDB()
	model.SetTelemetryDB(db)
	t.Cleanup(func() { model.SetTelemetryDB(oldLog) })
	t.Setenv("PROJECTS_ENABLED", "true")
	user := model.User{Username: "status-filter-owner", Quota: 50000000}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	var project model.Project
	if err := db.Where("owner_user_id = ?", user.Id).First(&project).Error; err != nil {
		t.Fatal(err)
	}
	// Inserted oldest first; the list orders by id DESC, so the two disabled
	// keys sit behind the newest key, which is expired.
	statuses := []int{common.TokenStatusEnabled, common.TokenStatusDisabled, common.TokenStatusDisabled, common.TokenStatusExpired}
	for index, status := range statuses {
		token := model.Token{ProjectID: project.ID, UserId: user.Id, Name: fmt.Sprintf("status-%d", index),
			Key: strings.Repeat(string(rune('a'+index)), 48), Status: status, ExpiredTime: -1, UnlimitedQuota: true}
		if err := db.Create(&token).Error; err != nil {
			t.Fatal(err)
		}
	}
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set("id", user.Id) })
	router.GET("/api/token/", GetAllTokens)
	router.GET("/api/token/search", SearchTokens)
	get := func(path string, out any) {
		t.Helper()
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest("GET", path, nil))
		if response.Code != 200 || !strings.Contains(response.Body.String(), `"success":true`) {
			t.Fatalf("GET %s: %d %s", path, response.Code, response.Body.String())
		}
		var envelope struct {
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(envelope.Data, out); err != nil {
			t.Fatal(err)
		}
	}
	suffix := "project_id=" + project.PublicID
	statusCode := func(item map[string]json.RawMessage) int {
		var code int
		if err := json.Unmarshal(item["status_code"], &code); err != nil {
			t.Fatalf("status_code: %v", err)
		}
		return code
	}

	var page struct {
		Total int                          `json:"total"`
		Items []map[string]json.RawMessage `json:"items"`
	}
	get(fmt.Sprintf("/api/token/?%s&status=%d&p=1&size=1", suffix, common.TokenStatusDisabled), &page)
	if page.Total != 2 || len(page.Items) != 1 || statusCode(page.Items[0]) != common.TokenStatusDisabled {
		t.Fatalf("filtered page = total %d, %d items", page.Total, len(page.Items))
	}

	var searched []map[string]json.RawMessage
	get(fmt.Sprintf("/api/token/search?%s&keyword=status-&status=%d", suffix, common.TokenStatusExpired), &searched)
	if len(searched) != 1 || statusCode(searched[0]) != common.TokenStatusExpired {
		t.Fatalf("filtered search returned %d keys", len(searched))
	}

	// An out-of-range status is ignored rather than matching nothing.
	get(fmt.Sprintf("/api/token/search?%s&keyword=status-&status=99", suffix), &searched)
	if len(searched) != len(statuses) {
		t.Fatalf("unfiltered search returned %d keys", len(searched))
	}
}

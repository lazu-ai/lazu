package controller

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	gormsqlite "github.com/glebarez/sqlite"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/middleware"
	"github.com/lazu-ai/lazu/internal/model"
	usagepkg "github.com/lazu-ai/lazu/internal/usage"
	"gorm.io/gorm"
)

func TestReceiptHTTPRequiresSameOwnerAndKeyRegardlessOfRole(t *testing.T) {
	previousDB, previousLogDB, previousRedis := model.CoreDB(), model.ConfiguredTelemetryDB(), common.RedisEnabled
	db, err := gorm.Open(gormsqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	model.SetCoreDB(db)
	model.SetTelemetryDB(db)
	common.RedisEnabled = false
	model.EnsureColumnNamesInitialized()
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() {
		_ = sqlDB.Close()
		model.SetCoreDB(previousDB)
		model.SetTelemetryDB(previousLogDB)
		common.RedisEnabled = previousRedis
	})
	if err := db.AutoMigrate(&model.Project{}, &model.ProjectMember{}, &model.ProjectRemoval{}, &model.ProjectRemovalKey{}, &model.User{}, &model.Token{}, &model.Log{}, &model.RoutingOutcome{}, &model.RoutingDecision{}); err != nil {
		t.Fatal(err)
	}
	for i, role := range []int{common.RoleCommonUser, common.RoleRootUser, common.RoleRootUser} {
		id := 92001 + i
		if err := db.Create(&model.User{Id: id, Username: fmt.Sprintf("receipt-user-%d", i), Role: role, Status: common.UserStatusEnabled}).Error; err != nil {
			t.Fatal(err)
		}
		if err := (&model.Token{Id: id, UserId: id, Key: fmt.Sprintf("receipt-key-%d", i), Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: 1000}).Insert(); err != nil {
			t.Fatal(err)
		}
	}
	if err := (&model.Token{Id: 92004, UserId: 92001, Key: "receipt-second-key", Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: 1000}).Insert(); err != nil {
		t.Fatal(err)
	}
	fact := usagepkg.FactFromDTO(&dto.Usage{PromptTokens: 12, CompletionTokens: 3, InputTokenSource: dto.UsageSourceLocalEstimate})
	var key model.Token
	if err := db.First(&key, 92001).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Log{ProjectID: key.ProjectID, UserId: 92001, TokenId: 92001, Type: model.LogTypeConsume, RequestID: "req-scope-fixture", PublicID: "log-scope-fixture", UsageFact: fact.JSONString(), Other: `{"admin_info":{"secret":"private-provider-field"}}`}).Error; err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.GET("/api/usage/requests/:request_id", middleware.TokenAuth(), middleware.RejectEphemeralCredentialOutsideRelay(), GetUsageRequestDetail)
	for _, tc := range []struct {
		name, key string
		status    int
	}{
		{"owner legacy key", "sk-receipt-key-0", 200},
		{"owner bare key", "receipt-key-0", 200},
		{"owner different key", "sk-receipt-second-key", 404},
		{"other-account administrator", "sk-receipt-key-1", 404},
		{"platform operator other owner", "sk-receipt-key-2", 404},
		{"anonymous", "", 401},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/api/usage/requests/req-scope-fixture", nil)
			if tc.key != "" {
				req.Header.Set("Authorization", "Bearer "+tc.key)
			}
			router.ServeHTTP(w, req)
			if w.Code != tc.status {
				t.Fatalf("status %d want %d body=%s", w.Code, tc.status, w.Body)
			}
			if strings.Contains(w.Body.String(), "private-provider-field") {
				t.Fatal("private provider field leaked")
			}
			if w.Code == 200 {
				var detail dto.UsageRequestDetailResponse
				if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil {
					t.Fatal(err)
				}
				if detail.Usage.Measurement == nil || detail.Usage.Measurement.Input != dto.UsageSourceLocalEstimate {
					t.Fatal("receipt lost local measurement provenance")
				}
			}
		})
	}
	for _, ids := range [][2]int{{0, 92001}, {92001, 0}, {0, 0}, {-1, 92001}} {
		if _, err := model.GetConsumeLogByRequestIDForToken(ids[0], ids[1], "req-scope-fixture"); err == nil {
			t.Fatal("missing identity broadened receipt lookup")
		}
	}
}

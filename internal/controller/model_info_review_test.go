package controller

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/types"
)

func TestAcceptFirstRunRejectsSupersededBrowserIDs(t *testing.T) {
	cleanup := setupUserModelsClassifiedTestDB(t)
	defer cleanup()
	if err := model.CoreDB().AutoMigrate(&model.ModelInfoReview{}); err != nil {
		t.Fatal(err)
	}
	rows := []model.ModelInfoReview{
		{Id: 100, ModelName: "model-a", Kind: model.ModelInfoReviewFirstRun, Status: model.ModelInfoReviewResolved},
		{Id: 101, ModelName: "model-a", Kind: model.ModelInfoReviewFirstRun, Status: model.ModelInfoReviewPending},
	}
	if err := model.CoreDB().Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{`{}`, `{"ids":[100]}`, `{"ids":[100,101]}`} {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest("POST", "/api/model_info/reviews/first_run/accept", strings.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
		AcceptAllModelInfoFirstRun(c)
		if body == `{}` {
			assertCatalogError(t, recorder, http.StatusBadRequest, types.ErrorCodeInvalidRequest)
		} else {
			assertCatalogError(t, recorder, http.StatusConflict, types.ErrorCodeVersionConflict)
		}
	}
	var current model.ModelInfoReview
	if err := model.CoreDB().First(&current, 101).Error; err != nil {
		t.Fatal(err)
	}
	if current.Status != model.ModelInfoReviewPending {
		t.Fatal("unseen revision was changed")
	}
}

func TestModelMetaEditRollsBackWhenReviewCleanupFails(t *testing.T) {
	db := setupModelsErrorTestDB(t)
	if err := db.AutoMigrate(&model.ModelInfoReview{}); err != nil {
		t.Fatal(err)
	}
	m := model.Model{ModelName: "atomic-edit", ContextLength: 100, InfoSources: model.ModelInfoSources{model.ModelInfoFieldContextLength: model.ModelInfoSourceModelsDev}}
	if err := db.Create(&m).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TRIGGER fail_review_cleanup BEFORE UPDATE ON model_info_reviews BEGIN SELECT RAISE(ABORT, 'cleanup failed'); END`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.ModelInfoReview{ModelName: m.ModelName, Kind: model.ModelInfoReviewDiff, Field: model.ModelInfoFieldContextLength}).Error; err != nil {
		t.Fatal(err)
	}
	after := m
	after.ContextLength = 200
	body, err := json.Marshal(after)
	if err != nil {
		t.Fatal(err)
	}
	response := callModelsHandler(UpdateModelMeta, handlerCall{method: http.MethodPut, target: "/api/models/", body: string(body)})
	assertCatalogError(t, response, http.StatusInternalServerError, types.ErrorCodeInternalError)
	var got model.Model
	if err := db.First(&got, m.Id).Error; err != nil {
		t.Fatal(err)
	}
	if got.ContextLength != 100 || got.InfoSources.Get(model.ModelInfoFieldContextLength) != model.ModelInfoSourceModelsDev {
		t.Fatalf("HTTP failure left a partial edit: %+v", got)
	}
}

func TestSavingCheapTierKeepsDirectTierGap(t *testing.T) {
	db := setupModelsErrorTestDB(t)
	if err := db.AutoMigrate(&model.ModelInfoReview{}); err != nil {
		t.Fatal(err)
	}
	gap := model.ModelInfoReview{ModelName: "tier-gap-model", Kind: model.ModelInfoReviewTierGap, Field: "context_tier"}
	if err := db.Create(&gap).Error; err != nil {
		t.Fatal(err)
	}
	for _, lane := range []string{"cheap", "direct"} {
		response := callModelsHandler(SaveModelOpsTierConfig, handlerCall{
			method: http.MethodPut, target: "/api/model_ops/tier_config",
			body:  fmt.Sprintf(`{"model":"tier-gap-model","tiers":[{"lane":%q,"price":{"input_per_mtok":2,"output_per_mtok":10,"context_tier_threshold":100,"context_tier_input_per_mtok":4}}]}`, lane),
			setup: func(c *gin.Context) { c.Set("role", common.RoleRootUser) },
		})
		if response.Code != http.StatusOK {
			t.Fatalf("save failed: %s", response.Body.String())
		}
		if err := db.First(&gap, gap.Id).Error; err != nil {
			t.Fatal(err)
		}
		want := model.ModelInfoReviewPending
		if lane == "direct" {
			want = model.ModelInfoReviewResolved
		}
		if gap.Status != want {
			t.Fatalf("%s save left gap %s, want %s", lane, gap.Status, want)
		}
	}
}

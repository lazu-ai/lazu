package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/types"
)

func TestRemovePricePlanMemberHonoursURLPlan(t *testing.T) {
	setupOIDCTestDB(t)
	user := model.User{Username: "moved-member", PricePlanID: 2}
	if err := model.CoreDB().Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodDelete, "/api/price-plans/1/members/moved-member", nil)
	c.Params = gin.Params{{Key: "id", Value: "1"}, {Key: "user", Value: user.Username}}
	RemovePricePlanMember(c)
	assertCatalogError(t, recorder, http.StatusConflict, types.ErrorCodeVersionConflict)
	var stored model.User
	if err := model.CoreDB().First(&stored, user.Id).Error; err != nil {
		t.Fatal(err)
	}
	if stored.PricePlanID != 2 {
		t.Fatalf("removed membership from another plan: %d", stored.PricePlanID)
	}
}

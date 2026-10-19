package controller

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/model"
)

func TestDeleteModelSellPriceAuditsAllRows(t *testing.T) {
	cleanup := setupUserModelsClassifiedTestDB(t)
	defer cleanup()
	if err := model.CoreDB().AutoMigrate(&model.AdminAuditLog{}); err != nil {
		t.Fatal(err)
	}
	rows := []model.ModelSellPrice{
		{ModelName: "audit-only", ChannelGroup: model.ChannelLaneDirect, PricingVersion: 1, Active: true},
		{ModelName: "audit-only", ChannelGroup: model.ChannelLaneDirect, PricingVersion: 2, Active: true},
		{ModelName: "audit-only", ChannelGroup: model.ChannelLaneAgent, PricingVersion: 1, Active: true},
	}
	if err := model.CoreDB().Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest("DELETE", "/api/pricing/sell_prices?model_name=audit-only", nil)
	DeleteModelSellPrice(c)
	var response struct {
		Success bool `json:"success"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !response.Success {
		t.Fatalf("delete failed: %s", recorder.Body.String())
	}
	var audit model.AdminAuditLog
	if err := model.CoreDB().Where("action = ?", "pricing.sell_price.delete").First(&audit).Error; err != nil {
		t.Fatal(err)
	}
	var diff struct {
		Prices struct {
			Before []model.ModelSellPrice `json:"before"`
		} `json:"prices"`
	}
	if err := json.Unmarshal([]byte(audit.DiffSummary), &diff); err != nil {
		t.Fatal(err)
	}
	if len(diff.Prices.Before) != 3 {
		t.Fatalf("incomplete audit: %s", audit.DiffSummary)
	}
	for i, row := range rows {
		if diff.Prices.Before[i].ID != row.ID {
			t.Fatal("audit row mismatch")
		}
	}
}

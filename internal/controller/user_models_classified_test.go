package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/gin-gonic/gin"
	gormsqlite "github.com/glebarez/sqlite"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
	"gorm.io/gorm"
)

func setupUserModelsClassifiedTestDB(t *testing.T) func() {
	t.Helper()

	previousDB := model.CoreDB()
	previousRedisEnabled := common.RedisEnabled
	common.RedisEnabled = false
	db, err := gorm.Open(gormsqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	model.SetCoreDB(db)

	if err := db.AutoMigrate(
		&model.User{},
		&model.Channel{},
		&model.Ability{},
		&model.Model{},
		&model.ModelMetadata{},
		&model.ModelSellPrice{},
		&model.Vendor{},
	); err != nil {
		t.Fatalf("failed to migrate test tables: %v", err)
	}

	return func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
		common.RedisEnabled = previousRedisEnabled
		model.SetCoreDB(previousDB)
		if previousDB != nil {
			model.RefreshPricing()
		}
	}
}

func TestGetUserModelsClassified(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cleanup := setupUserModelsClassifiedTestDB(t)
	defer cleanup()

	user := &model.User{
		Id:          1001,
		Username:    "classified_tester",
		Password:    "password123",
		DisplayName: "classified_tester",
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
	}
	if err := model.CoreDB().Create(user).Error; err != nil {
		t.Fatalf("failed to insert user: %v", err)
	}

	abilities := []model.Ability{
		{Group: "classified_group", Model: "tc-video-tag", ChannelId: 1, Enabled: true},
		{Group: "classified_group", Model: "tc-image-endpoint", ChannelId: 1, Enabled: true},
		{Group: "classified_group", Model: "tc-audio-tag", ChannelId: 1, Enabled: true},
		{Group: "classified_group", Model: "tc-hybrid", ChannelId: 1, Enabled: true},
		{Group: "classified_group", Model: "tc-plain-gpt", ChannelId: 1, Enabled: true},
	}
	if err := model.CoreDB().Create(&abilities).Error; err != nil {
		t.Fatalf("failed to insert abilities: %v", err)
	}

	modelMeta := []model.Model{
		{
			ModelName: "tc-video-tag",
			Tags:      "video generation",
			Status:    1,
			NameRule:  model.NameRuleExact,
		},
		{
			ModelName: "tc-image-endpoint",
			Endpoints: `{"image-generation":{"path":"/v1/images/generations","method":"POST"}}`,
			Status:    1,
			NameRule:  model.NameRuleExact,
		},
		{
			ModelName: "tc-audio-tag",
			Tags:      "audio generation",
			Status:    1,
			NameRule:  model.NameRuleExact,
		},
		{
			ModelName: "tc-hybrid",
			Tags:      "image generation",
			Endpoints: `{"image-generation":{"path":"/v1/images/generations","method":"POST"}}`,
			Status:    1,
			NameRule:  model.NameRuleExact,
		},
		{
			ModelName: "tc-plain-gpt",
			Status:    1,
			NameRule:  model.NameRuleExact,
		},
	}
	if err := model.CoreDB().Create(&modelMeta).Error; err != nil {
		t.Fatalf("failed to insert model metadata: %v", err)
	}

	model.RefreshPricing()

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/user/models/classified", nil)
	c.Set("id", user.Id)

	GetUserModelsClassified(c)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", recorder.Code)
	}

	var payload struct {
		Success bool                                   `json:"success"`
		Data    map[string][]model.ModelClassifiedItem `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if !payload.Success {
		t.Fatalf("expected success=true, got false, body=%s", recorder.Body.String())
	}

	extractNames := func(items []model.ModelClassifiedItem) []string {
		names := make([]string, len(items))
		for i, item := range items {
			names[i] = item.ModelName
		}
		return names
	}

	expectVideo := []string{"tc-video-tag"}
	expectImage := []string{"tc-hybrid", "tc-image-endpoint"}
	expectAudio := []string{"tc-audio-tag"}

	if got := extractNames(payload.Data["video"]); !reflect.DeepEqual(got, expectVideo) {
		t.Fatalf("video mismatch: got=%v want=%v", got, expectVideo)
	}
	if got := extractNames(payload.Data["image"]); !reflect.DeepEqual(got, expectImage) {
		t.Fatalf("image mismatch: got=%v want=%v", got, expectImage)
	}
	if got := extractNames(payload.Data["audio"]); !reflect.DeepEqual(got, expectAudio) {
		t.Fatalf("audio mismatch: got=%v want=%v", got, expectAudio)
	}

	for _, list := range payload.Data {
		for _, item := range list {
			if item.ModelName == "tc-plain-gpt" {
				t.Fatalf("plain model should not be classified, got=%v", payload.Data)
			}
		}
	}
}

package controller

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	sqlite "github.com/glebarez/sqlite"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/model"
	"gorm.io/gorm"
)

func TestRevealTokenKeyOwnerBoundary(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open("file:credential-reveal?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	old := model.CoreDB()
	model.SetCoreDB(db)
	t.Cleanup(func() { model.SetCoreDB(old); sql, _ := db.DB(); _ = sql.Close() })
	if err = db.AutoMigrate(&model.Project{}, &model.ProjectMember{}, &model.User{}, &model.Token{}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int{7, 8, 1} {
		if err := db.Create(&model.User{Id: id, Username: fmt.Sprintf("reveal-user-%d", id)}).Error; err != nil {
			t.Fatal(err)
		}
	}
	key := "sk-lazu-ui-review-owner-fixture-abcdefgh1234" // secret-scan: allow -- test fixture
	token := model.Token{UserId: 7, PublicID: "tok_reveal_fixture", Key: key, Name: "owner"}
	if err = db.Create(&token).Error; err != nil {
		t.Fatal(err)
	}
	for _, owner := range []int{7, 8, 1, 0} {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
		c.Params = gin.Params{{Key: "id", Value: token.PublicID}}
		c.Set("id", owner)
		RevealTokenKey(c)
		if recorder.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("secret response must not be cached")
		}
		if owner == 7 {
			if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), key) {
				t.Fatal("owner reveal failed")
			}
		} else if recorder.Code != http.StatusNotFound || strings.Contains(recorder.Body.String(), key) {
			t.Fatal("cross-user reveal was not denied")
		}
	}
	token.Key = "digest:unrecoverable" // secret-scan: allow -- legacy digest marker fixture
	if err = db.Model(&token).UpdateColumn("key", token.Key).Error; err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	c.Params = gin.Params{{Key: "id", Value: token.PublicID}}
	c.Set("id", 7)
	RevealTokenKey(c)
	if recorder.Code != http.StatusConflict {
		t.Fatal("legacy digest must not be returned as a secret")
	}
}

func TestSearchCredentialProjection(t *testing.T) {
	key := "search-upstream-fixture-abcdefgh1234" // secret-scan: allow -- test fixture
	channel := &model.Channel{Key: key, LastBusinessUsedAt: 1780000000}
	record := searchBackendRecordFromChannel(channel, false)
	raw, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), key) || record.APIKey != "" {
		t.Fatal("list exposed upstream secret")
	}
	if record.MaskedKey != "search-u••••••••1234" || record.LastUsedAt != channel.LastBusinessUsedAt {
		t.Fatal("incorrect hint or last business use")
	}
}

func TestRevealSearchKeyAuditsActorWithoutSecret(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open("file:search-reveal-audit?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	oldDB, oldLogDB := model.CoreDB(), model.ConfiguredTelemetryDB()
	model.SetCoreDB(db)
	model.SetTelemetryDB(db)
	t.Cleanup(func() { model.SetCoreDB(oldDB); model.SetTelemetryDB(oldLogDB); sql, _ := db.DB(); _ = sql.Close() })
	if err := db.AutoMigrate(&model.Channel{}, &model.Log{}, &model.User{}); err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(model.TelemetrySchemaModels()...); err != nil {
		t.Fatal(err)
	}
	key := "search-reveal-audit-fixture-1234" // secret-scan: allow -- test fixture
	for _, channelType := range []int{constant.ChannelTypeJina, constant.ChannelTypeOpenAI} {
		channel := model.Channel{Type: channelType, Key: key, PublicID: fmt.Sprintf("chn_reveal_%d", channelType)}
		if err := db.Create(&channel).Error; err != nil {
			t.Fatal(err)
		}
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
		c.Params = gin.Params{{Key: "id", Value: channel.PublicID}}
		c.Set("id", 42)
		RevealSearchBackendKey(c)
		if recorder.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("reveal must not be cached")
		}
		if channelType == constant.ChannelTypeJina {
			if !strings.Contains(recorder.Body.String(), key) {
				t.Fatal("search key not revealed")
			}
		} else if strings.Contains(recorder.Body.String(), key) {
			t.Fatal("non-search secret leaked")
		}
	}
	var logs []model.Log
	if err := db.Find(&logs).Error; err != nil {
		t.Fatal(err)
	}
	if len(logs) != 1 || logs[0].UserId != 42 || logs[0].Type != model.LogTypeSystem || !strings.Contains(logs[0].Content, "channel_id=") {
		t.Fatalf("missing actor audit: %#v", logs)
	}
	if strings.Contains(logs[0].Content, key) {
		t.Fatal("audit leaked key")
	}
}

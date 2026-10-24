package controller

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	sqlite "github.com/glebarez/sqlite"
	"github.com/lazu-ai/lazu/internal/model"
	"gorm.io/gorm"
)

func TestTokenStatUsesOwnedImmutableID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	oldDB, oldLogDB := model.CoreDB(), model.ConfiguredTelemetryDB()
	model.SetCoreDB(db)
	model.SetTelemetryDB(db)
	t.Cleanup(func() { model.SetCoreDB(oldDB); model.SetTelemetryDB(oldLogDB); sqlDB, _ := db.DB(); _ = sqlDB.Close() })
	if err := db.AutoMigrate(&model.Token{}, &model.Log{}, &model.User{}, &model.Project{}, &model.ProjectMember{}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int{7, 8} {
		if err := db.Create(&model.User{Id: id, Username: fmt.Sprintf("stat-user-%d", id)}).Error; err != nil {
			t.Fatal(err)
		}
	}
	tokens := []model.Token{{UserId: 7, Name: "prod"}, {UserId: 7, Name: "prod"}, {UserId: 8, Name: "prod"}}
	for i := range tokens {
		tokens[i].Key = fmt.Sprintf("stat-test-fixture-%d", i) // secret-scan: allow -- unique test fixture
		if err := db.Create(&tokens[i]).Error; err != nil {
			t.Fatal(err)
		}
		log := model.Log{ProjectID: tokens[i].ProjectID, UserId: tokens[i].UserId, TokenId: tokens[i].Id, TokenName: "old-name", Type: model.LogTypeConsume, CreatedAt: 100, AmountMicrousd: 100}
		if i > 0 {
			log.AmountMicrousd = 900
		}
		if err := db.Create(&log).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		id   string
		code int
	}{{tokens[0].PublicID, 200}, {tokens[2].PublicID, 404}, {"tok_missing", 404}} {
		r := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(r)
		c.Request = httptest.NewRequest("GET", "/api/log/self/stat?token_id="+tc.id+"&start_timestamp=1&end_timestamp=200", nil)
		c.Set("id", 7)
		GetLogsSelfStat(c)
		if r.Code != tc.code {
			t.Fatalf("%s: status %d: %s", tc.id, r.Code, r.Body.String())
		}
		if tc.code == 200 {
			var result struct {
				Data struct {
					Amount int64 `json:"amount_microusd"`
				} `json:"data"`
			}
			if err := json.Unmarshal(r.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if result.Data.Amount != 100 {
				t.Fatalf("combined another token or lost renamed history: %s", r.Body.String())
			}
		}
	}
}

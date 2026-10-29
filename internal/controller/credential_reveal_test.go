package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	sqlite "github.com/glebarez/sqlite"
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

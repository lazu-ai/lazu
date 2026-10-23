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
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/service"
	"gorm.io/gorm"
)

func setupTelegramLinkControllerDB(t *testing.T) *gorm.DB {
	t.Helper()
	name := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	db, err := gorm.Open(gormsqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", name)), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	previousDB := model.CoreDB()
	model.SetCoreDB(db)
	t.Cleanup(func() {
		model.SetCoreDB(previousDB)
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
	})
	if err := db.AutoMigrate(&model.User{}, &model.UserIdentity{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.User{Id: 7, Username: "carol", Password: "x"}).Error; err != nil {
		t.Fatal(err)
	}
	return db
}

func TestTelegramReminderNeedsALinkedAccount(t *testing.T) {
	setupTelegramLinkControllerDB(t)
	gin.SetMode(gin.TestMode)
	put := func() (*httptest.ResponseRecorder, settingsErrorBody) {
		router := gin.New()
		router.Use(func(c *gin.Context) { c.Set("id", 7) })
		router.PUT("/api/user/setting", UpdateUserSetting)
		req := httptest.NewRequest(http.MethodPut, "/api/user/setting", strings.NewReader(`{"notify_type":"telegram","quota_warning_threshold":5}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		var body settingsErrorBody
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		return rec, body
	}
	rec, body := put()
	expectSettingsError(t, rec, body, http.StatusConflict, "telegram_not_linked")

	if err := service.LinkTelegram(7, 123); err != nil {
		t.Fatal(err)
	}
	if rec, _ := put(); rec.Code != http.StatusOK {
		t.Fatalf("linked account could not choose Telegram: %d %s", rec.Code, rec.Body.String())
	}
}

func TestSignInIdentitiesLeaveTelegramOut(t *testing.T) {
	db := setupTelegramLinkControllerDB(t)
	if err := db.Create(&model.UserIdentity{UserID: 7, Provider: "github", Subject: "gh-7"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := service.LinkTelegram(7, 456); err != nil {
		t.Fatal(err)
	}
	identities, err := listUserIdentities(7)
	if err != nil {
		t.Fatal(err)
	}
	if len(identities) != 1 || identities[0].Provider != "github" {
		t.Fatalf("sign-in identities = %+v", identities)
	}
	if label := linkedProviderLabel(7); strings.Contains(strings.ToLower(label), "telegram") {
		t.Fatalf("mail would tell the user to sign in with %q", label)
	}
}

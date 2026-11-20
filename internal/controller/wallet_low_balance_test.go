package controller

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	gormsqlite "github.com/glebarez/sqlite"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/setting/email_setting"
	"github.com/lazu-ai/lazu/pkg/billing"
	"gorm.io/gorm"
)

func setupWalletLowBalanceTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	name := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	db, err := gorm.Open(gormsqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", name)), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	previousDB, previousRedis := model.CoreDB(), common.RedisEnabled
	model.SetCoreDB(db)
	common.RedisEnabled = false
	model.EnsureColumnNamesInitialized()
	t.Cleanup(func() {
		model.SetCoreDB(previousDB)
		common.RedisEnabled = previousRedis
		raw, _ := db.DB()
		_ = raw.Close()
	})
	if err := db.AutoMigrate(&model.User{}, &model.TopUp{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestLowBalanceMailStateUsesTheAccountsThresholdAndSwitch(t *testing.T) {
	db := setupWalletLowBalanceTestDB(t)
	cfg := email_setting.GetEmailSetting()
	oldEnabled, oldThreshold := cfg.LowBalanceEnabled, cfg.LowBalanceThresholdUSD
	cfg.LowBalanceEnabled, cfg.LowBalanceThresholdUSD = true, 5
	t.Cleanup(func() { cfg.LowBalanceEnabled, cfg.LowBalanceThresholdUSD = oldEnabled, oldThreshold })

	users := []model.User{
		{Id: 1, Username: "plain", Status: common.UserStatusEnabled},
		{Id: 2, Username: "own", Status: common.UserStatusEnabled, Setting: fmt.Sprintf(`{"quota_warning_threshold":%d,"low_balance_off":true}`, billing.USD(20))},
	}
	if err := db.Create(&users).Error; err != nil {
		t.Fatal(err)
	}

	plain := lowBalanceMailState(1)
	if plain["available"] != true || plain["on"] != true || plain["threshold_microusd"] != billing.USD(5) {
		t.Fatalf("platform default = %+v", plain)
	}
	own := lowBalanceMailState(2)
	if own["available"] != true || own["on"] != false || own["threshold_microusd"] != billing.USD(20) {
		t.Fatalf("account's own threshold and switch = %+v", own)
	}

	cfg.LowBalanceEnabled = false
	if off := lowBalanceMailState(1); off["available"] != false {
		t.Fatalf("platform mail off still available: %+v", off)
	}
}

func TestUpdateSelfMailPrefsStoresTheLowBalanceSwitch(t *testing.T) {
	db := setupWalletLowBalanceTestDB(t)
	if err := db.Create(&model.User{Id: 3, Username: "switch", Status: common.UserStatusEnabled, Setting: `{"weekly_digest_off":true}`}).Error; err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	put := func(body string) {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPut, "/api/user/self/mail-prefs", bytes.NewBufferString(body))
		c.Request.Header.Set("Content-Type", "application/json")
		c.Set("id", 3)
		UpdateSelfMailPrefs(c)
		if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"success":true`) {
			t.Fatalf("PUT %s = %d %s", body, recorder.Code, recorder.Body.String())
		}
	}
	read := func() map[string]any {
		var user model.User
		if err := db.First(&user, 3).Error; err != nil {
			t.Fatal(err)
		}
		out := map[string]any{}
		_ = json.Unmarshal([]byte(user.Setting), &out)
		return out
	}

	put(`{"low_balance":false}`)
	if got := read(); got["low_balance_off"] != true || got["weekly_digest_off"] != true {
		t.Fatalf("after turning off = %+v", got)
	}
	put(`{"low_balance":true}`)
	if got := read(); got["low_balance_off"] == true || got["weekly_digest_off"] != true {
		t.Fatalf("after turning on = %+v", got)
	}
}

func TestGetUserTopUpsRejectsAnUnboundedRange(t *testing.T) {
	setupWalletLowBalanceTestDB(t)
	gin.SetMode(gin.TestMode)
	for _, query := range []string{
		"start_timestamp=100",
		"end_timestamp=100",
		"start_timestamp=200&end_timestamp=100",
		fmt.Sprintf("start_timestamp=1&end_timestamp=%d", 1+401*24*60*60),
	} {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodGet, "/api/user/topup/self?"+query, nil)
		c.Set("id", 3)
		GetUserTopUps(c)
		if strings.Contains(recorder.Body.String(), `"success":true`) {
			t.Fatalf("%s accepted: %s", query, recorder.Body.String())
		}
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/user/topup/self?start_timestamp=100&end_timestamp=200", nil)
	c.Set("id", 3)
	GetUserTopUps(c)
	if !strings.Contains(recorder.Body.String(), `"success":true`) {
		t.Fatalf("bounded range refused: %s", recorder.Body.String())
	}
}

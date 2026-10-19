package model

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	gormsqlite "github.com/glebarez/sqlite"
	"github.com/go-redis/redis/v8"
	"github.com/lazu-ai/lazu/internal/common"
	"gorm.io/gorm"
)

func TestUserCacheHasToppedUpField(t *testing.T) {
	if !userCacheHasToppedUpField(map[string]string{"HasToppedUp": "true"}) {
		t.Fatalf("expected cache with HasToppedUp to be complete")
	}
	if userCacheHasToppedUpField(map[string]string{"Status": "1"}) {
		t.Fatalf("expected cache without HasToppedUp to be incomplete")
	}
}

func TestUserCacheGenerationRejectsFillCapturedBeforeSoftDeleteIntegration(t *testing.T) {
	redisURL := strings.TrimSpace(os.Getenv("TEST_REDIS_URL"))
	if redisURL == "" {
		t.Skip("TEST_REDIS_URL is not configured")
	}
	options, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatalf("parse Redis URL: %v", err)
	}
	client := redis.NewClient(options)
	if err := client.Ping(context.Background()).Err(); err != nil {
		t.Fatalf("ping Redis: %v", err)
	}

	name := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	db, err := gorm.Open(gormsqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", name)), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sqlite handle: %v", err)
	}
	if err := db.AutoMigrate(&User{}, &Token{}); err != nil {
		t.Fatalf("migrate auth rows: %v", err)
	}

	previousDB := coreDB
	previousRedisEnabled := common.RedisEnabled
	previousRedisClient := common.RDB
	coreDB = db
	common.RedisEnabled = true
	common.RDB = client
	EnsureColumnNamesInitialized()
	userID := int(time.Now().UnixNano()%900_000_000) + 100_000_000
	ctx := context.Background()
	t.Cleanup(func() {
		_ = client.Del(ctx, getUserCacheKey(userID), getUserCacheGenerationKey(userID)).Err()
		_ = client.Close()
		common.RedisEnabled = previousRedisEnabled
		common.RDB = previousRedisClient
		coreDB = previousDB
		if err := sqlDB.Close(); err != nil {
			t.Fatalf("close sqlite: %v", err)
		}
	})
	if err := client.Del(ctx, getUserCacheKey(userID), getUserCacheGenerationKey(userID)).Err(); err != nil {
		t.Fatalf("clear Redis fixture: %v", err)
	}

	user := User{
		Id:          userID,
		Username:    fmt.Sprintf("stale-fill-%d", userID),
		Password:    "placeholder-password-digest",
		DisplayName: "Stale Fill",
		Status:      common.UserStatusEnabled,
		Role:        common.RoleCommonUser,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}

	// This guard represents an API request that missed Redis and read an enabled
	// database row immediately before the account deletion committed.
	staleGuard, err := captureUserCacheFillGuard(user.Id)
	if err != nil {
		t.Fatalf("capture stale fill guard: %v", err)
	}
	staleSnapshot := user
	if _, err := DeleteSelfUser(user.Id); err != nil {
		t.Fatalf("soft delete user: %v", err)
	}
	if err := updateUserCache(staleSnapshot, staleGuard); err != nil {
		t.Fatalf("attempt stale cache fill: %v", err)
	}
	if exists, err := client.Exists(ctx, getUserCacheKey(user.Id)).Result(); err != nil || exists != 0 {
		t.Fatalf("pre-delete snapshot recreated user cache: exists=%d err=%v", exists, err)
	}

	var activeCount int64
	if err := db.Model(&User{}).Where("id = ?", user.Id).Count(&activeCount).Error; err != nil {
		t.Fatalf("count active users: %v", err)
	}
	if activeCount != 0 {
		t.Fatalf("expected soft delete to remain committed, active rows=%d", activeCount)
	}
}

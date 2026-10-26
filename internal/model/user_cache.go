package model

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/pkg/billing"

	"github.com/gin-gonic/gin"
)

type userQuotaCacheSnapshot struct {
	quota billing.MicroUSD
}

type userCacheFillGuard struct {
	userID     int
	generation string
}

const userCacheGenerationTTL = 24 * time.Hour

var invalidateUserCacheWithGenerationScript = redis.NewScript(`
local generation = redis.call("INCR", KEYS[2])
redis.call("DEL", KEYS[1])
redis.call("EXPIRE", KEYS[2], ARGV[1])
return generation
`)

// UserBase struct remains the same as it represents the cached data structure
type UserBase struct {
	Id            int              `json:"id"`
	Email         string           `json:"email"`
	Quota         billing.MicroUSD `json:"quota"`
	Status        int              `json:"status"`
	Username      string           `json:"username"`
	Setting       string           `json:"setting"`
	NeedsPassword bool             `json:"needs_password"`
	HasToppedUp   bool             `json:"has_topped_up"`
	PricePlanID   int              `json:"price_plan_id"`
	RPMLimit      int              `json:"rpm_limit"`
	TPMLimit      int              `json:"tpm_limit"`
}

func (user *UserBase) WriteContext(c *gin.Context) {
	common.SetContextKey(c, constant.ContextKeyUserQuota, user.Quota)
	common.SetContextKey(c, constant.ContextKeyUserStatus, user.Status)
	common.SetContextKey(c, constant.ContextKeyUserEmail, user.Email)
	common.SetContextKey(c, constant.ContextKeyUserName, user.Username)
	common.SetContextKey(c, constant.ContextKeyUserSetting, user.GetSetting())
	common.SetContextKey(c, constant.ContextKeyUserHasToppedUp, user.HasToppedUp)
	common.SetContextKey(c, constant.ContextKeyUserPricePlanID, user.PricePlanID)
	common.SetContextKey(c, constant.ContextKeyUserRPMLimit, user.RPMLimit)
	common.SetContextKey(c, constant.ContextKeyUserTPMLimit, user.TPMLimit)
}

func (user *UserBase) GetSetting() dto.UserSetting {
	setting := dto.UserSetting{}
	if user.Setting != "" {
		err := common.Unmarshal([]byte(user.Setting), &setting)
		if err != nil {
			common.SysLog("failed to unmarshal setting: " + err.Error())
		}
	}
	setting.NeedsPassword = user.NeedsPassword
	return setting
}

// getUserCacheKey returns the key for user cache
func getUserCacheKey(userId int) string {
	return fmt.Sprintf("user:%d", userId)
}

func getUserCacheGenerationKey(userID int) string {
	return fmt.Sprintf("user:%d:generation", userID)
}

// invalidateUserCache advances a generation fence before clearing the cached
// projection. A request that read the database before this invalidation cannot
// recreate an enabled user after a disable or soft delete commits.
func invalidateUserCache(userId int) error {
	if !common.RedisEnabled || common.RDB == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := invalidateUserCacheWithGenerationScript.Run(
		ctx,
		common.RDB,
		[]string{getUserCacheKey(userId), getUserCacheGenerationKey(userId)},
		int(userCacheGenerationTTL/time.Second),
	).Result(); err != nil {
		return fmt.Errorf("failed to invalidate user cache with generation fence: %w", err)
	}
	return nil
}

func InvalidateUserCache(userId int) error {
	return invalidateUserCache(userId)
}

func captureUserCacheFillGuard(userID int) (*userCacheFillGuard, error) {
	if !common.RedisEnabled || common.RDB == nil || userID <= 0 {
		return nil, errors.New("user cache fill guard is not available")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	generation, err := common.RDB.Get(ctx, getUserCacheGenerationKey(userID)).Result()
	if errors.Is(err, redis.Nil) {
		generation = "0"
		err = nil
	}
	if err != nil {
		return nil, err
	}
	return &userCacheFillGuard{userID: userID, generation: generation}, nil
}

// updateUserCache fills a missing cache entry only while the generation seen
// before the database read is still current. It also refuses to overwrite an
// entry another request may already have updated.
func updateUserCache(user User, guard *userCacheFillGuard) error {
	if !common.RedisEnabled {
		return nil
	}
	if guard == nil || guard.userID != user.Id || guard.generation == "" {
		return nil
	}
	_, err := common.RedisHSetObjIfAbsentIfGeneration(
		getUserCacheKey(user.Id),
		getUserCacheGenerationKey(user.Id),
		guard.generation,
		user.ToBaseUser(),
		time.Duration(common.RedisKeyCacheSeconds())*time.Second,
	)
	return err
}

// GetUserCache gets complete user cache from hash
func GetUserCache(userId int) (userCache *UserBase, err error) {
	var user *User
	var fromDB bool
	var fillGuard *userCacheFillGuard
	defer func() {
		// Update Redis cache asynchronously on successful CoreDB() read
		if shouldUpdateRedis(fromDB, err) && user != nil {
			common.SafeGo("user-cache-refresh", func() {
				if err := updateUserCache(*user, fillGuard); err != nil {
					common.SysLog("failed to update user status cache: " + err.Error())
				}
			})
		}
	}()

	timer := common.StartHotPathStage(common.HotPathStageAuthUserCache)
	userCache, err = cacheGetUserBase(userId)
	timer.Finish(err)
	if err == nil {
		common.IncrementHotPathCounter(common.HotPathCounterAuthUserCacheHit)
		return userCache, nil
	}
	common.IncrementHotPathCounter(common.HotPathCounterAuthUserCacheMiss)

	// If Redis fails, get from CoreDB()
	fromDB = true
	if common.RedisEnabled {
		var guardErr error
		fillGuard, guardErr = captureUserCacheFillGuard(userId)
		if guardErr != nil {
			// The database remains authoritative. Skipping this cache fill is safer
			// than allowing an unfenced snapshot to outlive a concurrent revocation.
			common.SysLog(fmt.Sprintf("failed to capture user cache fill guard for user %d: %v", userId, guardErr))
		}
	}
	common.IncrementHotPathCounter(common.HotPathCounterAuthUserDBFallback)
	dbTimer := common.StartHotPathStage(common.HotPathStageAuthUserDBFallback)
	user, err = GetUserById(userId, false)
	dbTimer.Finish(err)
	if err != nil {
		return nil, err // Return nil and error if CoreDB() lookup fails
	}

	// Create cache object from user data
	userCache = &UserBase{
		Id:            user.Id,
		Quota:         user.Quota,
		Status:        user.Status,
		Username:      user.Username,
		Setting:       user.Setting,
		Email:         user.Email,
		NeedsPassword: user.NeedsPassword,
		HasToppedUp:   user.HasToppedUp,
		PricePlanID:   user.PricePlanID,
		RPMLimit:      user.RPMLimit,
		TPMLimit:      user.TPMLimit,
	}

	return userCache, nil
}

func cacheGetUserBase(userId int) (*UserBase, error) {
	if !common.RedisEnabled {
		return nil, fmt.Errorf("redis is not enabled")
	}
	var userCache UserBase
	fields, err := common.RedisHGetObjWithFields(getUserCacheKey(userId), &userCache)
	if err != nil {
		return nil, err
	}
	if !userCacheHasToppedUpField(fields) {
		return nil, fmt.Errorf("cached user %d is missing has_topped_up", userId)
	}
	if !userCacheHasNeedsPasswordField(fields) {
		return nil, fmt.Errorf("cached user %d is missing needs_password", userId)
	}
	if _, ok := fields["PricePlanID"]; !ok {
		return nil, fmt.Errorf("cached user %d is missing price_plan_id", userId)
	}
	return &userCache, nil
}

func userCacheHasToppedUpField(fields map[string]string) bool {
	_, ok := fields["HasToppedUp"]
	return ok
}

func userCacheHasNeedsPasswordField(fields map[string]string) bool {
	_, ok := fields["NeedsPassword"]
	return ok
}

func captureUserQuotaCacheSnapshot(userId int) *userQuotaCacheSnapshot {
	if GatewayLedgerEnabled() || !common.RedisEnabled || common.RDB == nil {
		return nil
	}
	cache, err := cacheGetUserBase(userId)
	if err != nil || cache == nil {
		return nil
	}
	return &userQuotaCacheSnapshot{quota: cache.Quota}
}

func cacheAdjustUserQuotaIfUnchanged(userId int, expected billing.MicroUSD, delta billing.MicroUSD) (cacheUpdateResult, error) {
	return conditionalHashIncrIfEqual(getUserCacheKey(userId), []conditionalHashIncrField{
		{name: "Quota", expected: expected.Int64(), delta: delta.Int64()},
	})
}

// Helper functions to get individual fields if needed

func getUserQuotaCache(userId int) (billing.MicroUSD, error) {
	cache, err := GetUserCache(userId)
	if err != nil {
		return 0, err
	}
	return cache.Quota, nil
}

func getUserNameCache(userId int) (string, error) {
	cache, err := GetUserCache(userId)
	if err != nil {
		return "", err
	}
	return cache.Username, nil
}

func getUserSettingCache(userId int) (dto.UserSetting, error) {
	cache, err := GetUserCache(userId)
	if err != nil {
		return dto.UserSetting{}, err
	}
	return cache.GetSetting(), nil
}

func updateUserQuotaCache(userId int, quota billing.MicroUSD) error {
	if !common.RedisEnabled {
		return nil
	}
	return common.RedisHSetField(getUserCacheKey(userId), "Quota", fmt.Sprintf("%d", quota))
}

func updateUserNameCache(userId int, username string) error {
	if !common.RedisEnabled {
		return nil
	}
	return common.RedisHSetField(getUserCacheKey(userId), "Username", username)
}

func updateUserSettingCache(userId int, setting string) error {
	if !common.RedisEnabled {
		return nil
	}
	return common.RedisHSetField(getUserCacheKey(userId), "Setting", setting)
}

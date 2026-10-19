package model

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	gormsqlite "github.com/glebarez/sqlite"
	"github.com/go-redis/redis/v8"
	"github.com/lazu-ai/lazu/internal/common"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

type redisPipelineCounter struct {
	processCalls  int
	pipelineCalls int
}

func (counter *redisPipelineCounter) BeforeProcess(ctx context.Context, _ redis.Cmder) (context.Context, error) {
	counter.processCalls++
	return ctx, nil
}

func (counter *redisPipelineCounter) AfterProcess(context.Context, redis.Cmder) error {
	return nil
}

func (counter *redisPipelineCounter) BeforeProcessPipeline(ctx context.Context, _ []redis.Cmder) (context.Context, error) {
	counter.pipelineCalls++
	return ctx, nil
}

func (counter *redisPipelineCounter) AfterProcessPipeline(context.Context, []redis.Cmder) error {
	return nil
}

func setupTokenKeyStorageTestDB(t *testing.T, name string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(gormsqlite.Open("file:"+name+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("failed to get sql db: %v", err)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Fatalf("failed to close sqlite db: %v", err)
		}
	})
	if err := db.AutoMigrate(&Token{}); err != nil {
		t.Fatalf("failed to migrate tokens: %v", err)
	}
	return db
}

func TestTokenDigestUsesColumnUniqueConstraint(t *testing.T) {
	parsed, err := schema.Parse(&Token{}, &sync.Map{}, schema.NamingStrategy{})
	if err != nil {
		t.Fatalf("parse token schema: %v", err)
	}
	field := parsed.LookUpField("KeyDigest")
	if field == nil || !field.Unique {
		t.Fatal("key digest must be a column unique constraint for PostgreSQL legacy migration compatibility")
	}
}

func withTokenKeyStorageGlobals(t *testing.T, db *gorm.DB) {
	t.Helper()
	originalDB := coreDB
	originalRedisEnabled := common.RedisEnabled
	originalUsingSQLite := common.UsingSQLite
	originalUsingPostgreSQL := common.UsingPostgreSQL
	t.Cleanup(func() {
		coreDB = originalDB
		common.RedisEnabled = originalRedisEnabled
		common.UsingSQLite = originalUsingSQLite
		common.UsingPostgreSQL = originalUsingPostgreSQL
		initCol()
	})
	coreDB = db
	common.RedisEnabled = false
	common.UsingSQLite = true
	common.UsingPostgreSQL = false
	initCol()
}

func TestTokenInsertStoresPlaintextAndDigest(t *testing.T) {
	db := setupTokenKeyStorageTestDB(t, "token_key_storage_insert")
	withTokenKeyStorageGlobals(t, db)

	clearKey := "sk-lazu-abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUV" // secret-scan: allow -- test fixture
	token := &Token{UserId: 1, Name: "hashed", Key: clearKey, Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: 100}
	if err := token.Insert(); err != nil {
		t.Fatalf("insert token failed: %v", err)
	}
	if token.Key != clearKey {
		t.Fatalf("insert should preserve clear key in memory, got %q", token.Key)
	}

	var row Token
	if err := db.First(&row, token.Id).Error; err != nil {
		t.Fatalf("load stored token failed: %v", err)
	}
	if row.Key != clearKey {
		t.Fatalf("expected stored key to remain plaintext, got %q", row.Key)
	}
	if row.KeyDigest != TokenKeyDigest(clearKey) {
		t.Fatalf("unexpected key digest: %q", row.KeyDigest)
	}
	if row.KeyPrefix == "" || row.KeySuffix == "" {
		t.Fatalf("expected display key metadata, got prefix=%q suffix=%q", row.KeyPrefix, row.KeySuffix)
	}

	found, err := GetTokenByKey(clearKey, true)
	if err != nil {
		t.Fatalf("digest auth lookup failed: %v", err)
	}
	if found.Id != token.Id || found.Key != clearKey {
		t.Fatalf("unexpected token lookup result: %#v", found)
	}
}

func TestMigrateTokenKeyDigestsBackfillsLegacyWithoutClearing(t *testing.T) {
	db := setupTokenKeyStorageTestDB(t, "token_key_storage_backfill")
	withTokenKeyStorageGlobals(t, db)

	legacyKey := "sk-lazu-legacyabcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMN" // secret-scan: allow -- test fixture
	if err := db.Exec(`INSERT INTO tokens (id, user_id, public_id, "key", status, name, expired_time, remain_quota) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		7, 1, "tok_legacy", legacyKey, common.TokenStatusEnabled, "legacy", -1, 100).Error; err != nil {
		t.Fatalf("insert legacy token failed: %v", err)
	}
	if err := migrateTokenKeyDigests(db); err != nil {
		t.Fatalf("migrate token key digests failed: %v", err)
	}

	var row Token
	if err := db.First(&row, 7).Error; err != nil {
		t.Fatalf("load token failed: %v", err)
	}
	if row.Key != legacyKey {
		t.Fatalf("legacy key should remain during compatibility phase, got %q", row.Key)
	}
	if row.KeyDigest != TokenKeyDigest(legacyKey) {
		t.Fatalf("expected digest backfill, got %q", row.KeyDigest)
	}
}

func TestTokenUpdateSurfacesCacheFenceFailureAfterCommit(t *testing.T) {
	db := setupTokenKeyStorageTestDB(t, "token_update_cache_fence_failure")
	withTokenKeyStorageGlobals(t, db)

	token := &Token{
		UserId:         1,
		Name:           "policy-fence",
		Key:            "sk-lazu-policy-fence-abcdefghijklmnopqrstuvwxyz123456", // secret-scan: allow -- test fixture
		Status:         common.TokenStatusEnabled,
		ExpiredTime:    -1,
		RemainQuota:    100,
		UnlimitedQuota: true,
	}
	if err := token.Insert(); err != nil {
		t.Fatalf("insert token: %v", err)
	}

	previousRDB := common.RDB
	common.RedisEnabled = true
	common.RDB = nil
	t.Cleanup(func() { common.RDB = previousRDB })

	token.Status = common.TokenStatusDisabled
	err := token.Update()
	if err == nil || !strings.Contains(err.Error(), "cache fence failed") {
		t.Fatalf("expected synchronous cache fence failure, got %v", err)
	}
	if !IsTokenMutationCommittedError(err) {
		t.Fatalf("expected committed mutation error type, got %T", err)
	}

	var stored Token
	if err := db.First(&stored, token.Id).Error; err != nil {
		t.Fatalf("reload committed token: %v", err)
	}
	if stored.Status != common.TokenStatusDisabled {
		t.Fatalf("database mutation should remain committed, got status %d", stored.Status)
	}
}

func TestTokenMutationsSynchronouslyFenceMutableSnapshotIntegration(t *testing.T) {
	redisURL := os.Getenv("TEST_REDIS_URL")
	if redisURL == "" {
		t.Skip("TEST_REDIS_URL is not configured")
	}
	options, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatalf("parse Redis URL: %v", err)
	}
	client := redis.NewClient(options)
	ctx := context.Background()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatalf("ping Redis: %v", err)
	}

	db := setupTokenKeyStorageTestDB(t, "token_update_cache_fence_success")
	withTokenKeyStorageGlobals(t, db)
	token := &Token{
		UserId:         1,
		Name:           "policy-fence",
		Key:            "sk-lazu-policy-fence-integration-abcdefghijklmnop", // secret-scan: allow -- test fixture
		Status:         common.TokenStatusEnabled,
		ExpiredTime:    -1,
		RemainQuota:    100,
		UnlimitedQuota: true,
	}
	if err := token.Insert(); err != nil {
		t.Fatalf("insert token: %v", err)
	}

	previousRDB := common.RDB
	common.RedisEnabled = true
	common.RDB = client
	digest := token.CacheDigest()
	t.Cleanup(func() {
		_ = client.Del(ctx,
			tokenCacheGenerationKey(digest),
			"token:"+digest,
		).Err()
		_ = client.Close()
		common.RDB = previousRDB
	})

	if err := cacheSetToken(*token); err != nil {
		t.Fatalf("seed token snapshot: %v", err)
	}
	if err := client.Set(ctx, tokenCacheGenerationKey(digest), "7", time.Hour).Err(); err != nil {
		t.Fatalf("seed mutable token generation: %v", err)
	}
	staleEnabledToken := *token

	token.Status = common.TokenStatusDisabled
	if err := token.Update(); err != nil {
		t.Fatalf("update token with synchronous cache fence: %v", err)
	}
	if generation, err := client.Get(ctx, tokenCacheGenerationKey(digest)).Int(); err != nil || generation != 8 {
		t.Fatalf("update fence must invalidate the mutable token snapshot: generation=%d err=%v", generation, err)
	}
	cached, err := cacheGetTokenByKey(token.Key)
	if err != nil {
		t.Fatalf("read refreshed token snapshot: %v", err)
	}
	if cached.Status != common.TokenStatusDisabled {
		t.Fatalf("token snapshot must expose disabled status, got %d", cached.Status)
	}

	if err := cacheSetToken(*token); err != nil {
		t.Fatalf("reseed token snapshot before delete: %v", err)
	}
	staleFillGuard, err := captureTokenCacheFillGuard(digest)
	if err != nil {
		t.Fatalf("capture stale token fill guard: %v", err)
	}
	if err := token.Delete(); err != nil {
		t.Fatalf("delete token with synchronous cache fence: %v", err)
	}
	if err := cacheSetTokenIfMissing(staleEnabledToken, staleFillGuard); err != nil {
		t.Fatalf("attempt stale token snapshot fill: %v", err)
	}
	if generation, err := client.Get(ctx, tokenCacheGenerationKey(digest)).Int(); err != nil || generation != 9 {
		t.Fatalf("delete fence must invalidate the mutable token snapshot: generation=%d err=%v", generation, err)
	}
	if exists, err := client.Exists(ctx, "token:"+digest).Result(); err != nil || exists != 0 {
		t.Fatalf("all token caches must be gone before delete returns: exists=%d err=%v", exists, err)
	}
}

func TestBatchDeleteTokensPipelinesCacheFencesIntegration(t *testing.T) {
	redisURL := os.Getenv("TEST_REDIS_URL")
	if redisURL == "" {
		t.Skip("TEST_REDIS_URL is not configured")
	}
	options, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatalf("parse Redis URL: %v", err)
	}
	client := redis.NewClient(options)
	ctx := context.Background()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatalf("ping Redis: %v", err)
	}

	db := setupTokenKeyStorageTestDB(t, "token_batch_delete_cache_fence")
	withTokenKeyStorageGlobals(t, db)
	previousRDB := common.RDB
	common.RedisEnabled = true
	common.RDB = client

	tokens := make([]Token, 3)
	digests := make([]string, 0, len(tokens))
	for index := range tokens {
		tokens[index] = Token{
			UserId:         7,
			Name:           "batch-fence",
			Key:            fmt.Sprintf("sk-lazu-batch-fence-%d-abcdefghijklmnopqrstuvwxyz", index), // secret-scan: allow -- test fixture
			Status:         common.TokenStatusEnabled,
			ExpiredTime:    -1,
			RemainQuota:    100,
			UnlimitedQuota: true,
		}
		if err := tokens[index].Insert(); err != nil {
			t.Fatalf("insert token %d: %v", index, err)
		}
		digest := tokens[index].CacheDigest()
		digests = append(digests, digest)
		if err := cacheSetToken(tokens[index]); err != nil {
			t.Fatalf("seed token snapshot %d: %v", index, err)
		}
		if err := client.Set(ctx, tokenCacheGenerationKey(digest), "20", time.Hour).Err(); err != nil {
			t.Fatalf("seed generation %d: %v", index, err)
		}
	}

	t.Cleanup(func() {
		keys := make([]string, 0, len(digests)*3)
		for _, digest := range digests {
			keys = append(keys, tokenCacheGenerationKey(digest), "token:"+digest)
		}
		_ = client.Del(ctx, keys...).Err()
		_ = client.Close()
		common.RDB = previousRDB
	})

	counter := &redisPipelineCounter{}
	client.AddHook(counter)
	ids := make([]int, 0, len(tokens))
	for index := range tokens {
		ids = append(ids, tokens[index].Id)
	}
	deleted, err := BatchDeleteTokens(ids, 7)
	if err != nil {
		t.Fatalf("batch delete tokens: %v", err)
	}
	if deleted != len(tokens) {
		t.Fatalf("expected %d deleted tokens, got %d", len(tokens), deleted)
	}
	if counter.pipelineCalls != 1 || counter.processCalls != 0 {
		t.Fatalf("batch fence must use one Redis pipeline, process=%d pipeline=%d", counter.processCalls, counter.pipelineCalls)
	}

	for index, digest := range digests {
		if generation, err := client.Get(ctx, tokenCacheGenerationKey(digest)).Int(); err != nil || generation != 21 {
			t.Fatalf("token %d generation must advance once: generation=%d err=%v", index, generation, err)
		}
		if exists, err := client.Exists(ctx, "token:"+digest).Result(); err != nil || exists != 0 {
			t.Fatalf("token %d caches must be deleted: exists=%d err=%v", index, exists, err)
		}
	}
}

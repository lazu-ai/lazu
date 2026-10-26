package model

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/pkg/billing"
)

const tokenCacheGenerationTTL = 24 * time.Hour

func tokenCacheGenerationKey(digest string) string { return "token:generation:" + digest }

const tokenSnapshotsInvalidateScript = `
local generation = redis.call("INCR", KEYS[2])
redis.call("DEL", KEYS[1])
redis.call("EXPIRE", KEYS[2], ARGV[1])
return generation
`

// tokenCacheFillGuard prevents a database result captured before a token
// policy mutation or deletion from recreating the mutable token snapshot.
type tokenCacheFillGuard struct {
	digest     string
	generation string
}

type tokenQuotaCacheSnapshot struct {
	remain billing.MicroUSD
	used   billing.MicroUSD
}

func cacheSetTokenIfMissing(token Token, guard *tokenCacheFillGuard) error {
	key := token.CacheDigest()
	if key == "" {
		return fmt.Errorf("token cache digest is empty")
	}
	if guard == nil || guard.digest != key || guard.generation == "" {
		return nil
	}
	token.Clean()
	_, err := common.RedisHSetObjIfAbsentIfGeneration(
		fmt.Sprintf("token:%s", key),
		tokenCacheGenerationKey(key),
		guard.generation,
		&token,
		time.Duration(common.RedisKeyCacheSeconds())*time.Second,
	)
	return err
}

func captureTokenCacheFillGuard(digest string) (*tokenCacheFillGuard, error) {
	digest = strings.TrimSpace(digest)
	if !common.RedisEnabled || common.RDB == nil || digest == "" {
		return nil, errors.New("token cache fill guard is not available")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	generation, err := common.RDB.Get(ctx, tokenCacheGenerationKey(digest)).Result()
	if errors.Is(err, redis.Nil) {
		generation = "0"
		err = nil
	}
	if err != nil {
		return nil, err
	}
	return &tokenCacheFillGuard{digest: digest, generation: generation}, nil
}

// cacheInvalidateTokenSnapshotsWithFence invalidates legacy mutable snapshots
// and prevents stale fills; the current identity snapshot has its own fence.
func cacheInvalidateTokenSnapshotsWithFence(digests []string) error {
	if !common.RedisEnabled {
		return nil
	}

	uniqueDigests := make([]string, 0, len(digests))
	seen := make(map[string]struct{}, len(digests))
	for _, digest := range digests {
		digest = strings.TrimSpace(digest)
		if digest == "" {
			continue
		}
		if _, exists := seen[digest]; exists {
			continue
		}
		seen[digest] = struct{}{}
		uniqueDigests = append(uniqueDigests, digest)
	}
	if len(uniqueDigests) == 0 {
		return nil
	}
	if common.RDB == nil {
		return errors.New("redis client is not initialized")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := common.RDB.Pipelined(ctx, func(pipe redis.Pipeliner) error {
		for _, digest := range uniqueDigests {
			pipe.Eval(
				ctx,
				tokenSnapshotsInvalidateScript,
				[]string{fmt.Sprintf("token:%s", digest), tokenCacheGenerationKey(digest)},
				int(tokenCacheGenerationTTL/time.Second),
			)
		}
		return nil
	})

	if err != nil {
		return fmt.Errorf("failed to execute token cache fence pipeline: %w", err)
	}
	return nil
}

func cacheDeleteToken(key string) error {
	key = TokenKeyDigest(key)
	if key == "" {
		return nil
	}
	return cacheDeleteTokenByDigest(key)
}

// invalidateTokenQuotaCache clears the legacy mutable quota snapshot.
func invalidateTokenQuotaCache(key string) error {
	return cacheDeleteToken(key)
}

func cacheDeleteTokenByDigest(digest string) error {
	digest = strings.TrimSpace(digest)
	if digest == "" {
		return nil
	}
	err := common.RedisDelKey(fmt.Sprintf("token:%s", digest))
	if err != nil {
		return err
	}
	return nil
}

func cacheDeleteTokenForToken(token *Token) error {
	if token == nil {
		return nil
	}
	digest := token.CacheDigest()
	if digest == "" {
		return nil
	}
	err := common.RedisDelKey(fmt.Sprintf("token:%s", digest))
	if err != nil {
		return err
	}
	return nil
}

func captureTokenQuotaCacheSnapshot(key string) *tokenQuotaCacheSnapshot {
	if GatewayLedgerEnabled() || !common.RedisEnabled || common.RDB == nil || key == "" {
		return nil
	}
	token, err := cacheGetTokenByKey(key)
	if err != nil || token == nil || token.Status == 0 {
		return nil
	}
	return &tokenQuotaCacheSnapshot{
		remain: token.RemainQuota,
		used:   token.UsedQuota,
	}
}

func cacheAdjustTokenQuotaIfUnchanged(key string, snapshot *tokenQuotaCacheSnapshot, remainDelta billing.MicroUSD, usedDelta billing.MicroUSD) (cacheUpdateResult, error) {
	if snapshot == nil {
		return cacheUpdateMissing, nil
	}
	hashedKey := TokenKeyDigest(key)
	if hashedKey == "" {
		return cacheUpdateMissing, nil
	}
	return conditionalHashIncrIfEqual(fmt.Sprintf("token:%s", hashedKey), []conditionalHashIncrField{
		{name: constant.TokenFiledRemainQuota, expected: snapshot.remain.Int64(), delta: remainDelta.Int64()},
		{name: constant.TokenFieldUsedQuota, expected: snapshot.used.Int64(), delta: usedDelta.Int64()},
	})
}

// CacheGetTokenByKey 从缓存中获取 token，如果缓存中不存在，则从数据库中获取
func cacheGetTokenByKey(key string) (*Token, error) {
	hmacKey := TokenKeyDigest(key)
	if hmacKey == "" {
		return nil, fmt.Errorf("token key is empty")
	}
	if !common.RedisEnabled {
		return nil, fmt.Errorf("redis is not enabled")
	}
	var token Token
	fields, err := common.RedisHGetObjWithFields(fmt.Sprintf("token:%s", hmacKey), &token)
	if err != nil {
		return nil, err
	}
	_, hasDecisionPolicy := fields["DecisionQuestionGeneration"]
	if _, present := fields["ReasoningEffortCap"]; !present || !hasDecisionPolicy {
		// An older instance can refill this hash after a policy invalidation.
		// Reject the snapshot and fence it out so a current CoreDB() read can refill it.
		if err := cacheInvalidateTokenSnapshotsWithFence([]string{hmacKey}); err != nil {
			return nil, err
		}
		return nil, errors.New("token cache lacks reasoning effort policy")
	}
	token.Key = common.NormalizeAPIKey(key)
	return &token, nil
}

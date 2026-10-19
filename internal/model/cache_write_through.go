package model

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/lazu-ai/lazu/internal/common"
)

type cacheUpdateResult int

const (
	cacheUpdateMissing cacheUpdateResult = iota
	cacheUpdateMismatch
	cacheUpdateApplied
)

type conditionalHashIncrField struct {
	name     string
	expected int64
	delta    int64
}

var conditionalHashIncrIfEqualScript = redis.NewScript(`
local key = KEYS[1]
local ttl = redis.call('TTL', key)
if ttl <= 0 then
  return 0
end
for i = 1, #ARGV, 3 do
  local field = ARGV[i]
  local expected = ARGV[i + 1]
  local current = redis.call('HGET', key, field)
  if current == false then
    return 0
  end
  if tostring(current) ~= tostring(expected) then
    return -1
  end
end
for i = 1, #ARGV, 3 do
  local field = ARGV[i]
  local delta = tonumber(ARGV[i + 2])
  if delta ~= 0 then
    redis.call('HINCRBY', key, field, delta)
  end
end
redis.call('EXPIRE', key, ttl)
return 1
`)

func conditionalHashIncrIfEqual(key string, fields []conditionalHashIncrField) (cacheUpdateResult, error) {
	if !common.RedisEnabled || common.RDB == nil || key == "" || len(fields) == 0 {
		return cacheUpdateMissing, nil
	}
	args := make([]any, 0, len(fields)*3)
	for _, field := range fields {
		args = append(args, field.name, strconv.FormatInt(field.expected, 10), strconv.FormatInt(field.delta, 10))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	raw, err := conditionalHashIncrIfEqualScript.Run(ctx, common.RDB, []string{key}, args...).Int64()
	if err != nil {
		return cacheUpdateMissing, err
	}
	switch raw {
	case 1:
		return cacheUpdateApplied, nil
	case -1:
		return cacheUpdateMismatch, nil
	case 0:
		return cacheUpdateMissing, nil
	default:
		return cacheUpdateMissing, fmt.Errorf("unexpected cache update result %d", raw)
	}
}

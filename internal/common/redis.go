package common

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/go-redis/redis/v8"
	redisv9 "github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

var RDB *redis.Client
var RedisEnabled = true

func ensureRedisClient() error {
	if !RedisEnabled {
		return errors.New("redis is not enabled")
	}
	if RDB == nil {
		return errors.New("redis client is not initialized")
	}
	return nil
}

func RedisKeyCacheSeconds() int {
	return SyncFrequency
}

// InitRedisClient This function is called after init()
func InitRedisClient() (err error) {
	if os.Getenv("REDIS_CONN_STRING") == "" {
		RedisEnabled = false
		SysDebug("REDIS_CONN_STRING not set, Redis is not enabled")
		return nil
	}
	if os.Getenv("SYNC_FREQUENCY") == "" {
		SysDebug("SYNC_FREQUENCY not set, using default value 60")
		SyncFrequency = 60
	}
	SysDebug("Redis is enabled")
	opt, err := redis.ParseURL(os.Getenv("REDIS_CONN_STRING"))
	if err != nil {
		FatalLog("failed to parse Redis connection string: " + err.Error())
	}
	opt.PoolSize = GetEnvOrDefault("REDIS_POOL_SIZE", 10)
	RDB = redis.NewClient(opt)
	if HotPathMetricsEnabled {
		RDB.AddHook(redisDispatchMetricsHook{})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err = RDB.Ping(ctx).Result()
	if err != nil {
		FatalLog("Redis ping test failed: " + err.Error())
	}
	SysDebug(fmt.Sprintf("Redis connected to %s", opt.Addr))
	SysDebug(fmt.Sprintf("Redis database: %d", opt.DB))
	return err
}

type requestRedisMetricsKey struct{}
type redisDispatchObserverKey struct{}
type redisDispatchStartedKey struct{}

// WithRedisDispatchObserver measures the complete client dispatch, including
// connection-pool wait and retries. The callback runs before the call returns.
func WithRedisDispatchObserver(ctx context.Context, observe func(time.Duration)) context.Context {
	return context.WithValue(ctx, redisDispatchObserverKey{}, observe)
}

// WithRequestRedisMetrics tags required request-path dispatches, including
// detached settlement work. Optional background telemetry remains distinct.
func WithRequestRedisMetrics(ctx context.Context) context.Context {
	return context.WithValue(ctx, requestRedisMetricsKey{}, true)
}

type redisDispatchMetricsHook struct{}

func (redisDispatchMetricsHook) BeforeProcess(ctx context.Context, _ redis.Cmder) (context.Context, error) {
	IncrementHotPathCounter("redis.client_dispatches")
	if marked, _ := ctx.Value(requestRedisMetricsKey{}).(bool); marked {
		IncrementHotPathCounter("redis.request_dispatches")
	}
	if observe, _ := ctx.Value(redisDispatchObserverKey{}).(func(time.Duration)); observe != nil {
		ctx = context.WithValue(ctx, redisDispatchStartedKey{}, time.Now())
	}
	return ctx, nil
}
func (redisDispatchMetricsHook) AfterProcess(ctx context.Context, _ redis.Cmder) error {
	if observe, _ := ctx.Value(redisDispatchObserverKey{}).(func(time.Duration)); observe != nil {
		if started, ok := ctx.Value(redisDispatchStartedKey{}).(time.Time); ok {
			observe(time.Since(started))
		}
	}
	return nil
}
func (redisDispatchMetricsHook) BeforeProcessPipeline(ctx context.Context, commands []redis.Cmder) (context.Context, error) {
	if len(commands) != 0 {
		return (redisDispatchMetricsHook{}).BeforeProcess(ctx, nil)
	}
	return ctx, nil
}
func (redisDispatchMetricsHook) AfterProcessPipeline(ctx context.Context, _ []redis.Cmder) error {
	return (redisDispatchMetricsHook{}).AfterProcess(ctx, nil)
}

func RedisSet(key string, value string, expiration time.Duration) error {
	if DebugEnabled {
		SysLog(fmt.Sprintf("Redis SET: key=%s, value=%s, expiration=%v", key, value, expiration))
	}
	if err := ensureRedisClient(); err != nil {
		return err
	}
	ctx := context.Background()
	return RDB.Set(ctx, key, value, expiration).Err()
}

func RedisGet(key string) (string, error) {
	if DebugEnabled {
		SysLog(fmt.Sprintf("Redis GET: key=%s", key))
	}
	if err := ensureRedisClient(); err != nil {
		return "", err
	}
	ctx := context.Background()
	val, err := RDB.Get(ctx, key).Result()
	return val, err
}

//func RedisExpire(key string, expiration time.Duration) error {
//	ctx := context.Background()
//	return RDB.Expire(ctx, key, expiration).Err()
//}
//
//func RedisGetEx(key string, expiration time.Duration) (string, error) {
//	ctx := context.Background()
//	return RDB.GetSet(ctx, key, expiration).Result()
//}

func RedisDel(key string) error {
	if DebugEnabled {
		SysLog(fmt.Sprintf("Redis DEL: key=%s", key))
	}
	if err := ensureRedisClient(); err != nil {
		return err
	}
	ctx := context.Background()
	return RDB.Del(ctx, key).Err()
}

func RedisDelKey(key string) error {
	if DebugEnabled {
		SysLog(fmt.Sprintf("Redis DEL Key: key=%s", key))
	}
	if err := ensureRedisClient(); err != nil {
		return err
	}
	ctx := context.Background()
	return RDB.Del(ctx, key).Err()
}

func RedisHSetObj(key string, obj interface{}, expiration time.Duration) error {
	if DebugEnabled {
		SysLog(fmt.Sprintf("Redis HSET: key=%s, obj=%+v, expiration=%v", key, obj, expiration))
	}
	if err := ensureRedisClient(); err != nil {
		return err
	}
	ctx := context.Background()

	data, err := redisHashObjectData(obj)
	if err != nil {
		return err
	}

	txn := RDB.TxPipeline()
	txn.HSet(ctx, key, data)

	// 只有在 expiration 大于 0 时才设置过期时间
	if expiration > 0 {
		txn.Expire(ctx, key, expiration)
	}

	_, err = txn.Exec(ctx)
	if err != nil {
		return fmt.Errorf("failed to execute transaction: %w", err)
	}
	return nil
}

// RedisHSetObjIfAbsent atomically creates a hash without overwriting a cache
// entry that another request may already have adjusted.
func RedisHSetObjIfAbsent(key string, obj interface{}, expiration time.Duration) (bool, error) {
	if DebugEnabled {
		SysLog(fmt.Sprintf("Redis HSETNX object: key=%s, expiration=%v", key, expiration))
	}
	if err := ensureRedisClient(); err != nil {
		return false, err
	}
	data, err := redisHashObjectData(obj)
	if err != nil {
		return false, err
	}

	args := make([]interface{}, 0, 1+len(data)*2)
	args = append(args, int64(expiration/time.Second))
	for field, value := range data {
		args = append(args, field, value)
	}
	const script = `
if redis.call("EXISTS", KEYS[1]) ~= 0 then
	return 0
end
redis.call("HSET", KEYS[1], unpack(ARGV, 2))
local ttl = tonumber(ARGV[1])
if ttl ~= nil and ttl > 0 then
	redis.call("EXPIRE", KEYS[1], ttl)
end
return 1
`
	created, err := RDB.Eval(context.Background(), script, []string{key}, args...).Int()
	if err != nil {
		return false, fmt.Errorf("failed to create Redis hash atomically: %w", err)
	}
	return created == 1, nil
}

// RedisHSetObjIfAbsentIfGeneration atomically creates a hash only while a
// companion generation key still matches the value captured before the
// caller read its authoritative source. A missing generation key is treated
// as generation zero.
func RedisHSetObjIfAbsentIfGeneration(key string, generationKey string, expectedGeneration string, obj interface{}, expiration time.Duration) (bool, error) {
	if DebugEnabled {
		SysLog(fmt.Sprintf("Redis generation-guarded HSETNX object: key=%s, generation_key=%s, expiration=%v", key, generationKey, expiration))
	}
	if err := ensureRedisClient(); err != nil {
		return false, err
	}
	if generationKey == "" || expectedGeneration == "" {
		return false, errors.New("generation key and expected generation are required")
	}
	data, err := redisHashObjectData(obj)
	if err != nil {
		return false, err
	}

	args := make([]interface{}, 0, 2+len(data)*2)
	args = append(args, expectedGeneration, int64(expiration/time.Second))
	for field, value := range data {
		args = append(args, field, value)
	}
	const script = `
local current_generation = redis.call("GET", KEYS[2])
if current_generation == false then
	current_generation = "0"
end
if current_generation ~= ARGV[1] then
	return 0
end
if redis.call("EXISTS", KEYS[1]) ~= 0 then
	return 0
end
redis.call("HSET", KEYS[1], unpack(ARGV, 3))
local ttl = tonumber(ARGV[2])
if ttl ~= nil and ttl > 0 then
	redis.call("EXPIRE", KEYS[1], ttl)
end
return 1
`
	created, err := RDB.Eval(context.Background(), script, []string{key, generationKey}, args...).Int()
	if err != nil {
		return false, fmt.Errorf("failed to create generation-guarded Redis hash atomically: %w", err)
	}
	return created == 1, nil
}

func redisHashObjectData(obj interface{}) (map[string]interface{}, error) {
	value := reflect.ValueOf(obj)
	if value.Kind() != reflect.Ptr || value.IsNil() {
		return nil, fmt.Errorf("obj must be a non-nil pointer to a struct, got %T", obj)
	}
	v := value.Elem()
	if v.Kind() != reflect.Struct {
		return nil, fmt.Errorf("obj must point to a struct, got %T", obj)
	}

	data := make(map[string]interface{}, v.NumField())
	t := v.Type()
	for i := 0; i < v.NumField(); i++ {
		field := t.Field(i)
		value := v.Field(i)

		// Skip DeletedAt field
		if field.Type.String() == "gorm.DeletedAt" {
			continue
		}

		// 处理指针类型
		if value.Kind() == reflect.Ptr {
			if value.IsNil() {
				data[field.Name] = ""
				continue
			}
			value = value.Elem()
		}

		// 处理布尔类型
		if value.Kind() == reflect.Bool {
			data[field.Name] = strconv.FormatBool(value.Bool())
			continue
		}

		// Preserve collection fields as JSON so token policy slices, maps, and
		// raw JSON metadata can be reconstructed on cache reads.
		if value.Kind() == reflect.Slice || value.Kind() == reflect.Map || value.Kind() == reflect.Array || value.Kind() == reflect.Struct {
			encoded, err := Marshal(value.Interface())
			if err != nil {
				return nil, fmt.Errorf("failed to serialize field %s: %w", field.Name, err)
			}
			data[field.Name] = string(encoded)
			continue
		}

		// 其他类型直接转换为字符串
		data[field.Name] = fmt.Sprintf("%v", value.Interface())
	}
	return data, nil
}

func RedisHGetObj(key string, obj interface{}) error {
	_, err := RedisHGetObjWithFields(key, obj)
	return err
}

func RedisHGetObjWithFields(key string, obj interface{}) (map[string]string, error) {
	if DebugEnabled {
		SysLog(fmt.Sprintf("Redis HGETALL: key=%s", key))
	}
	if err := ensureRedisClient(); err != nil {
		return nil, err
	}
	ctx := context.Background()

	result, err := RDB.HGetAll(ctx, key).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to load hash from Redis: %w", err)
	}

	if len(result) == 0 {
		return nil, fmt.Errorf("key %s not found in Redis", key)
	}

	if err := populateRedisHashObj(result, obj); err != nil {
		return nil, err
	}
	return result, nil
}

func populateRedisHashObj(result map[string]string, obj interface{}) error {
	// Handle both pointer and non-pointer values
	val := reflect.ValueOf(obj)
	if val.Kind() != reflect.Ptr {
		return fmt.Errorf("obj must be a pointer to a struct, got %T", obj)
	}

	v := val.Elem()
	if v.Kind() != reflect.Struct {
		return fmt.Errorf("obj must be a pointer to a struct, got pointer to %T", v.Interface())
	}

	t := v.Type()
	for i := 0; i < v.NumField(); i++ {
		field := t.Field(i)
		fieldName := field.Name
		if value, ok := result[fieldName]; ok {
			fieldValue := v.Field(i)

			// Handle pointer types
			if fieldValue.Kind() == reflect.Ptr {
				if value == "" {
					continue
				}
				if fieldValue.IsNil() {
					fieldValue.Set(reflect.New(fieldValue.Type().Elem()))
				}
				fieldValue = fieldValue.Elem()
			}

			// Enhanced type handling for Token struct
			switch fieldValue.Kind() {
			case reflect.String:
				fieldValue.SetString(value)
			case reflect.Int, reflect.Int64:
				intValue, err := strconv.ParseInt(value, 10, 64)
				if err != nil {
					return fmt.Errorf("failed to parse int field %s: %w", fieldName, err)
				}
				fieldValue.SetInt(intValue)
			case reflect.Bool:
				boolValue, err := strconv.ParseBool(value)
				if err != nil {
					return fmt.Errorf("failed to parse bool field %s: %w", fieldName, err)
				}
				fieldValue.SetBool(boolValue)
			case reflect.Slice, reflect.Map, reflect.Array:
				if !fieldValue.CanAddr() {
					return fmt.Errorf("collection field %s is not addressable", fieldName)
				}
				if err := Unmarshal([]byte(value), fieldValue.Addr().Interface()); err != nil {
					return fmt.Errorf("failed to parse collection field %s: %w", fieldName, err)
				}
			case reflect.Struct:
				// Special handling for gorm.DeletedAt
				if fieldValue.Type().String() == "gorm.DeletedAt" {
					if value != "" {
						timeValue, err := time.Parse(time.RFC3339, value)
						if err != nil {
							return fmt.Errorf("failed to parse DeletedAt field %s: %w", fieldName, err)
						}
						fieldValue.Set(reflect.ValueOf(gorm.DeletedAt{Time: timeValue, Valid: true}))
					}
				} else if err := Unmarshal([]byte(value), fieldValue.Addr().Interface()); err != nil {
					return fmt.Errorf("failed to parse struct field %s: %w", fieldName, err)
				}
			default:
				return fmt.Errorf("unsupported field type: %s for field %s", fieldValue.Kind(), fieldName)
			}
		}
	}

	return nil
}

// RedisIncr Add this function to handle atomic increments
func RedisIncr(key string, delta int64) error {
	if DebugEnabled {
		SysLog(fmt.Sprintf("Redis INCR: key=%s, delta=%d", key, delta))
	}
	if err := ensureRedisClient(); err != nil {
		return err
	}
	// 检查键的剩余生存时间
	ttlCmd := RDB.TTL(context.Background(), key)
	ttl, err := ttlCmd.Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return fmt.Errorf("failed to get TTL: %w", err)
	}

	// 只有在 key 存在且有 TTL 时才需要特殊处理
	if ttl > 0 {
		ctx := context.Background()
		// 开始一个Redis事务
		txn := RDB.TxPipeline()

		// 减少余额
		decrCmd := txn.IncrBy(ctx, key, delta)
		if err := decrCmd.Err(); err != nil {
			return err // 如果减少失败，则直接返回错误
		}

		// 重新设置过期时间，使用原来的过期时间
		txn.Expire(ctx, key, ttl)

		// 执行事务
		_, err = txn.Exec(ctx)
		return err
	}
	return nil
}

func RedisHIncrBy(key, field string, delta int64) error {
	if DebugEnabled {
		SysLog(fmt.Sprintf("Redis HINCRBY: key=%s, field=%s, delta=%d", key, field, delta))
	}
	if err := ensureRedisClient(); err != nil {
		return err
	}
	ttlCmd := RDB.TTL(context.Background(), key)
	ttl, err := ttlCmd.Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return fmt.Errorf("failed to get TTL: %w", err)
	}

	if ttl > 0 {
		ctx := context.Background()
		txn := RDB.TxPipeline()

		incrCmd := txn.HIncrBy(ctx, key, field, delta)
		if err := incrCmd.Err(); err != nil {
			return err
		}

		txn.Expire(ctx, key, ttl)

		_, err = txn.Exec(ctx)
		return err
	}
	return nil
}

func RedisHSetField(key, field string, value interface{}) error {
	if DebugEnabled {
		SysLog(fmt.Sprintf("Redis HSET field: key=%s, field=%s, value=%v", key, field, value))
	}
	if err := ensureRedisClient(); err != nil {
		return err
	}
	ttlCmd := RDB.TTL(context.Background(), key)
	ttl, err := ttlCmd.Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return fmt.Errorf("failed to get TTL: %w", err)
	}

	if ttl > 0 {
		ctx := context.Background()
		txn := RDB.TxPipeline()

		hsetCmd := txn.HSet(ctx, key, field, value)
		if err := hsetCmd.Err(); err != nil {
			return err
		}

		txn.Expire(ctx, key, ttl)

		_, err = txn.Exec(ctx)
		return err
	}
	return nil
}

// go-redis v9 (v9.12+) tries to enable CLIENT MAINT_NOTIFICATIONS on every new
// connection so it can coordinate failover with Redis Enterprise / ElastiCache.
// Against plain open-source Redis the server replies "ERR unknown subcommand",
// v9 silently disables the feature for that connection and prints
//
//	auto mode fallback: maintnotifications disabled due to handshake error: ...
//
// as a WARNING. It fires once per connection attempt, so the pool warmup spams
// the logs at startup.
//
// We drop that one message and pass everything else through to the default
// stderr logger.
type filteredRedisLogger struct {
	inner *log.Logger
}

func (l *filteredRedisLogger) Printf(_ context.Context, format string, v ...interface{}) {
	msg := fmt.Sprintf(format, v...)
	if strings.Contains(msg, "maintnotifications disabled") {
		return
	}
	_ = l.inner.Output(2, msg)
}

func init() {
	redisv9.SetLogger(&filteredRedisLogger{
		inner: log.New(os.Stderr, "redis: ", log.LstdFlags|log.Lshortfile),
	})
}

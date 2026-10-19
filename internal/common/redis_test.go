package common

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
)

func TestRedisHelpersReturnErrorWhenClientNil(t *testing.T) {
	oldEnabled := RedisEnabled
	oldRDB := RDB
	RedisEnabled = true
	RDB = nil
	defer func() {
		RedisEnabled = oldEnabled
		RDB = oldRDB
	}()

	assertNotInitialized := func(name string, err error) {
		t.Helper()
		if err == nil {
			t.Fatalf("%s returned nil error", name)
		}
		if !strings.Contains(err.Error(), "redis client is not initialized") {
			t.Fatalf("%s returned unexpected error: %v", name, err)
		}
	}

	_, err := RedisGet("missing")
	assertNotInitialized("RedisGet", err)
	assertNotInitialized("RedisSet", RedisSet("missing", "value", time.Minute))
	assertNotInitialized("RedisDelKey", RedisDelKey("missing"))
	assertNotInitialized("RedisHGetObj", RedisHGetObj("missing", &struct{ Value string }{}))
	_, err = RedisHSetObjIfAbsent("missing", &struct{ Value string }{}, time.Minute)
	assertNotInitialized("RedisHSetObjIfAbsent", err)
	_, err = RedisHSetObjIfAbsentIfGeneration("missing", "generation", "0", &struct{ Value string }{}, time.Minute)
	assertNotInitialized("RedisHSetObjIfAbsentIfGeneration", err)
	assertNotInitialized("RedisHSetField", RedisHSetField("missing", "Value", "value"))
	assertNotInitialized("RedisHIncrBy", RedisHIncrBy("missing", "Value", 1))
}

func TestRedisHashObjectData(t *testing.T) {
	type cachedToken struct {
		Id       int
		Enabled  bool
		Optional *string
		Models   []string
		Groups   map[string]string
		Metadata json.RawMessage
	}
	data, err := redisHashObjectData(&cachedToken{
		Id:       42,
		Enabled:  true,
		Models:   []string{"gpt-5.4-mini", "gemini-3.5-flash"},
		Groups:   map[string]string{"openai": "direct"},
		Metadata: json.RawMessage(`{"scope":"smoke"}`),
	})
	if err != nil {
		t.Fatalf("redisHashObjectData returned error: %v", err)
	}
	if data["Id"] != "42" || data["Enabled"] != "true" || data["Optional"] != "" {
		t.Fatalf("unexpected hash data: %#v", data)
	}
	if data["Models"] != `["gpt-5.4-mini","gemini-3.5-flash"]` ||
		data["Groups"] != `{"openai":"direct"}` ||
		data["Metadata"] != `{"scope":"smoke"}` {
		t.Fatalf("collection fields were not serialized as JSON: %#v", data)
	}

	var decoded cachedToken
	if err := populateRedisHashObj(map[string]string{
		"Id":       data["Id"].(string),
		"Enabled":  data["Enabled"].(string),
		"Models":   data["Models"].(string),
		"Groups":   data["Groups"].(string),
		"Metadata": data["Metadata"].(string),
	}, &decoded); err != nil {
		t.Fatalf("populateRedisHashObj returned error: %v", err)
	}
	if decoded.Id != 42 || !decoded.Enabled ||
		!reflect.DeepEqual(decoded.Models, []string{"gpt-5.4-mini", "gemini-3.5-flash"}) ||
		!reflect.DeepEqual(decoded.Groups, map[string]string{"openai": "direct"}) ||
		string(decoded.Metadata) != `{"scope":"smoke"}` {
		t.Fatalf("collection fields did not round-trip: %#v", decoded)
	}

	if _, err := redisHashObjectData(cachedToken{}); err == nil {
		t.Fatal("expected a non-pointer object to fail")
	}
	var nilToken *cachedToken
	if _, err := redisHashObjectData(nilToken); err == nil {
		t.Fatal("expected a nil pointer to fail")
	}
}

func TestRedisHSetObjIfAbsentIntegration(t *testing.T) {
	url := os.Getenv("TEST_REDIS_URL")
	if url == "" {
		t.Skip("TEST_REDIS_URL is not configured")
	}
	options, err := redis.ParseURL(url)
	if err != nil {
		t.Fatalf("parse Redis URL: %v", err)
	}
	client := redis.NewClient(options)
	oldEnabled := RedisEnabled
	oldRDB := RDB
	RedisEnabled = true
	RDB = client
	defer func() {
		RedisEnabled = oldEnabled
		RDB = oldRDB
		_ = client.Close()
	}()

	ctx := context.Background()
	key := fmt.Sprintf("test:hset-if-absent:%d", time.Now().UnixNano())
	defer client.Del(ctx, key)
	type quotaSnapshot struct {
		RemainQuota int64
		UsedQuota   int64
		Models      []string
		Groups      map[string]string
		Metadata    json.RawMessage
	}
	created, err := RedisHSetObjIfAbsent(
		key,
		&quotaSnapshot{
			RemainQuota: 100,
			UsedQuota:   10,
			Models:      []string{"gpt-5.4-mini"},
			Groups:      map[string]string{"openai": "direct"},
			Metadata:    json.RawMessage(`{"scope":"smoke"}`),
		},
		time.Minute,
	)
	if err != nil || !created {
		t.Fatalf("first write: created=%t err=%v", created, err)
	}
	created, err = RedisHSetObjIfAbsent(
		key,
		&quotaSnapshot{
			RemainQuota: 999,
			UsedQuota:   999,
			Models:      []string{"stale-model"},
			Groups:      map[string]string{"openai": "stale"},
			Metadata:    json.RawMessage(`{"scope":"stale"}`),
		},
		time.Minute,
	)
	if err != nil || created {
		t.Fatalf("second write: created=%t err=%v", created, err)
	}
	values, err := client.HGetAll(ctx, key).Result()
	if err != nil {
		t.Fatalf("read hash: %v", err)
	}
	if values["RemainQuota"] != "100" || values["UsedQuota"] != "10" {
		t.Fatalf("existing quota snapshot was overwritten: %#v", values)
	}
	var decoded quotaSnapshot
	if err := RedisHGetObj(key, &decoded); err != nil {
		t.Fatalf("read cached object: %v", err)
	}
	if decoded.RemainQuota != 100 || decoded.UsedQuota != 10 ||
		!reflect.DeepEqual(decoded.Models, []string{"gpt-5.4-mini"}) ||
		!reflect.DeepEqual(decoded.Groups, map[string]string{"openai": "direct"}) ||
		string(decoded.Metadata) != `{"scope":"smoke"}` {
		t.Fatalf("cached object did not round-trip: %#v", decoded)
	}
	if ttl, err := client.TTL(ctx, key).Result(); err != nil || ttl <= 0 {
		t.Fatalf("expected a positive TTL, got ttl=%s err=%v", ttl, err)
	}

	guardedKey := key + ":guarded"
	generationKey := key + ":generation"
	defer client.Del(ctx, guardedKey, generationKey)
	if err := client.Set(ctx, generationKey, "3", time.Minute).Err(); err != nil {
		t.Fatalf("seed generation: %v", err)
	}
	created, err = RedisHSetObjIfAbsentIfGeneration(
		guardedKey,
		generationKey,
		"3",
		&quotaSnapshot{RemainQuota: 300},
		time.Minute,
	)
	if err != nil || !created {
		t.Fatalf("guarded write at current generation: created=%t err=%v", created, err)
	}
	if err := client.Del(ctx, guardedKey).Err(); err != nil {
		t.Fatalf("delete guarded hash: %v", err)
	}
	if err := client.Set(ctx, generationKey, "4", time.Minute).Err(); err != nil {
		t.Fatalf("advance generation: %v", err)
	}
	created, err = RedisHSetObjIfAbsentIfGeneration(
		guardedKey,
		generationKey,
		"3",
		&quotaSnapshot{RemainQuota: 999},
		time.Minute,
	)
	if err != nil || created {
		t.Fatalf("stale guarded write: created=%t err=%v", created, err)
	}
	if exists, err := client.Exists(ctx, guardedKey).Result(); err != nil || exists != 0 {
		t.Fatalf("stale guarded write recreated hash: exists=%d err=%v", exists, err)
	}
}

func TestPopulateRedisHashObj(t *testing.T) {
	type cachedUser struct {
		Id          int
		Username    string
		HasToppedUp bool
	}
	var user cachedUser
	if err := populateRedisHashObj(map[string]string{
		"Id":          "42",
		"Username":    "alice",
		"HasToppedUp": "true",
	}, &user); err != nil {
		t.Fatalf("populateRedisHashObj returned error: %v", err)
	}
	if user.Id != 42 || user.Username != "alice" || !user.HasToppedUp {
		t.Fatalf("unexpected populated user: %#v", user)
	}
}

package common

import (
	"context"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
)

func TestGenerateNumericVerificationCodeIsAllDigits(t *testing.T) {
	for attempt := 0; attempt < 200; attempt++ {
		code, err := GenerateNumericVerificationCode(6)
		if err != nil {
			t.Fatalf("generate: %v", err)
		}
		if len(code) != 6 {
			t.Fatalf("expected 6 characters, got %q", code)
		}
		for _, character := range code {
			if character < '0' || character > '9' {
				// A hex code cannot be typed into a numeric one-time-code field,
				// which is what the sign-in screen renders.
				t.Fatalf("code must be digits only, got %q", code)
			}
		}
	}
	if _, err := GenerateNumericVerificationCode(0); err == nil {
		t.Fatal("a zero-length code should be refused")
	}
}

func useVerificationStore(t *testing.T, remote bool) *redis.Client {
	t.Helper()
	previousMode, previousRedis, previousClient := RedisVerificationCodeEnabled, RedisEnabled, RDB
	t.Cleanup(func() { RedisVerificationCodeEnabled, RedisEnabled, RDB = previousMode, previousRedis, previousClient })
	RedisVerificationCodeEnabled, RedisEnabled, RDB = remote, remote, nil
	if !remote {
		return nil
	}
	url := os.Getenv("TEST_REDIS_URL")
	if url == "" {
		t.Skip("TEST_REDIS_URL is not configured")
	}
	options, err := redis.ParseURL(url)
	if err != nil {
		t.Fatal(err)
	}
	RDB = redis.NewClient(options)
	client := RDB
	t.Cleanup(func() { _ = client.Close() })
	if err = client.Ping(context.Background()).Err(); err != nil {
		t.Fatal(err)
	}
	return client
}

func TestVerificationStores(t *testing.T) {
	for _, mode := range []string{"memory", "redis"} {
		t.Run(mode, func(t *testing.T) {
			client := useVerificationStore(t, mode == "redis")
			key := GeneratePublicID("test") + "@example.test"
			first := VerificationCode{Purpose: "new-address", Code: "234567"}
			second := VerificationCode{Purpose: "old-address", Code: "345678"}
			wrong := VerificationCode{Purpose: second.Purpose, Code: "000000"}
			issue := func(codes ...VerificationCode) {
				t.Helper()
				if err := IssueVerificationCodes(key, codes...); err != nil {
					t.Fatal(err)
				}
			}
			check := func(want VerificationResult, codes ...VerificationCode) {
				t.Helper()
				got, err := ConsumeVerificationCodes(strings.ToUpper(key), codes...)
				if err != nil || got != want {
					t.Fatalf("result=%v want=%v error=%v", got, want, err)
				}
			}
			issue(first, second)
			check(VerificationInvalid, first, wrong)
			check(VerificationAccepted, first, second)
			check(VerificationInvalid, first)
			issue(second)
			for i := 1; i < MaxVerificationAttempts; i++ {
				check(VerificationInvalid, wrong)
			}
			check(VerificationExhausted, wrong)
			check(VerificationInvalid, second)
			issue(wrong)
			for i := 1; i < MaxVerificationAttempts; i++ {
				check(VerificationInvalid, second)
			}
			issue(second)
			check(VerificationInvalid, wrong)
			check(VerificationAccepted, second)
			// A mailbox with no code follows the same attempt-limit responses.
			for i := 1; i < MaxVerificationAttempts; i++ {
				check(VerificationInvalid, wrong)
			}
			check(VerificationExhausted, wrong)
			reset := VerificationCode{Purpose: PasswordResetPurpose, Code: "test-reset-token"}
			issue(reset)
			for i := 0; i < MaxVerificationAttempts+1; i++ {
				check(VerificationInvalid, VerificationCode{Purpose: PasswordResetPurpose, Code: "wrong"})
			}
			check(VerificationAccepted, reset)
			issue(first)
			entries, err := verificationEntries(key, []VerificationCode{first})
			if err != nil {
				t.Fatal(err)
			}
			entry := entries[0]
			if strings.Contains(entry.key, key) || strings.Contains(entry.key, "example.test") {
				t.Fatal("key contains identity")
			}
			if client != nil {
				ctx := context.Background()
				hash, err := client.HGetAll(ctx, entry.key).Result()
				if err != nil || hash["digest"] != entry.digest || hash["digest"] == first.Code || len(hash) != 2 {
					t.Fatalf("unexpected stored fields (err=%v)", err)
				}
				ttl, err := client.TTL(ctx, entry.key).Result()
				if err != nil || ttl < 9*time.Minute || ttl > 10*time.Minute {
					t.Fatalf("issue TTL=%v err=%v", ttl, err)
				}
				if err := client.PExpire(ctx, entry.key, 200*time.Millisecond).Err(); err != nil {
					t.Fatal(err)
				}
				check(VerificationInvalid, VerificationCode{Purpose: first.Purpose, Code: "wrong"})
				time.Sleep(250 * time.Millisecond)
			} else {
				verificationMemory.Lock()
				v := verificationMemory.values[entry.key]
				v.expires = time.Now().Add(-time.Minute)
				verificationMemory.values[entry.key] = v
				verificationMemory.Unlock()
			}
			check(VerificationInvalid, first)
			issue(first)
			const contenders = 12
			var successes atomic.Int32
			var wg sync.WaitGroup
			for range contenders {
				wg.Add(1)
				go func() {
					defer wg.Done()
					got, err := ConsumeVerificationCodes(key, first)
					if err != nil {
						t.Error(err)
					}
					if got == VerificationAccepted {
						successes.Add(1)
					}
				}()
			}
			wg.Wait()
			if successes.Load() != 1 {
				t.Fatalf("accepted %d times", successes.Load())
			}
		})
	}
}

func TestVerificationUnavailableNeverUsesMemory(t *testing.T) {
	useVerificationStore(t, false)
	key := GeneratePublicID("test")
	proof := VerificationCode{Purpose: EmailAuthPurpose, Code: "112233"}
	if err := IssueVerificationCodes(key, proof); err != nil {
		t.Fatal(err)
	}
	RedisVerificationCodeEnabled = true
	if err := IssueVerificationCodes(key, proof); err == nil {
		t.Fatal("unavailable store accepted issue")
	}
	if _, err := ConsumeVerificationCodes(key, proof); err == nil {
		t.Fatal("redis verify read memory")
	}
	RedisEnabled = true
	RDB = redis.NewClient(&redis.Options{Addr: "127.0.0.1:0", MaxRetries: -1})
	_ = RDB.Close()
	if err := IssueVerificationCodes(key, proof); err == nil {
		t.Fatal("closed store accepted issue")
	}
	if _, err := ConsumeVerificationCodes(key, proof); err == nil {
		t.Fatal("closed store allowed verification")
	}
	RedisVerificationCodeEnabled = false
	if got, err := ConsumeVerificationCodes(key, proof); got != VerificationAccepted || err != nil {
		t.Fatal("unavailable Redis changed memory state")
	}
}

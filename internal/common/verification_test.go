package common

import (
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
)

func resetVerificationTestState(t *testing.T) {
	t.Helper()
	originalRedisVerification := RedisVerificationCodeEnabled
	originalRedisEnabled := RedisEnabled
	originalRDB := RDB
	originalValidMinutes := VerificationValidMinutes
	verificationMutex.Lock()
	originalMap := verificationMap
	originalAttempts := verificationAttempts
	verificationMap = make(map[string]verificationValue)
	verificationAttempts = make(map[string]verificationAttempt)
	verificationMutex.Unlock()
	t.Cleanup(func() {
		RedisVerificationCodeEnabled = originalRedisVerification
		RedisEnabled = originalRedisEnabled
		RDB = originalRDB
		VerificationValidMinutes = originalValidMinutes
		verificationMutex.Lock()
		verificationMap = originalMap
		verificationAttempts = originalAttempts
		verificationMutex.Unlock()
	})
	RedisVerificationCodeEnabled = false
	RedisEnabled = false
	RDB = nil
	VerificationValidMinutes = 10
}

func TestVerificationAttemptsExpireInMemory(t *testing.T) {
	resetVerificationTestState(t)
	verificationMutex.Lock()
	verificationAttempts["aexpired@example.com"] = verificationAttempt{
		count:     4,
		expiresAt: time.Now().Add(-time.Minute),
	}
	verificationMutex.Unlock()

	if attempts := recordVerificationFailureMemory("fresh@example.com", EmailAuthPurpose); attempts != 1 {
		t.Fatalf("fresh counter should start at one, got %d", attempts)
	}
	verificationMutex.Lock()
	_, expiredStillPresent := verificationAttempts["aexpired@example.com"]
	verificationMutex.Unlock()
	if expiredStillPresent {
		t.Fatal("expired verification-attempt entry was not removed")
	}
}

func TestVerificationCodeMemoryFallbackIsOneTime(t *testing.T) {
	resetVerificationTestState(t)

	RegisterVerificationCodeWithKey("user@example.com", "123456", EmailVerificationPurpose)
	if !VerifyCodeWithKey("user@example.com", "123456", EmailVerificationPurpose) {
		t.Fatal("expected verification code to pass")
	}
	if VerifyCodeWithKey("user@example.com", "123456", EmailVerificationPurpose) {
		t.Fatal("expected verification code to be one-time")
	}
}

func TestVerificationCodeMemoryFallbackIsAtomicallyConsumed(t *testing.T) {
	resetVerificationTestState(t)

	RegisterVerificationCodeWithKey("user@example.com", "123456", PasswordResetPurpose)
	const contenders = 32
	start := make(chan struct{})
	var successes atomic.Int32
	var wg sync.WaitGroup
	for range contenders {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if VerifyCodeWithKey("user@example.com", "123456", PasswordResetPurpose) {
				successes.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()

	if got := successes.Load(); got != 1 {
		t.Fatalf("expected exactly one successful token consumer, got %d", got)
	}
}

func TestVerificationCodeMemorySurvivesWrongAttempt(t *testing.T) {
	resetVerificationTestState(t)

	RegisterVerificationCodeWithKey("user@example.com", "123456", EmailVerificationPurpose)
	if VerifyCodeWithKey("user@example.com", "000000", EmailVerificationPurpose) {
		t.Fatal("expected wrong verification code to fail")
	}
	if !VerifyCodeWithKey("user@example.com", "123456", EmailVerificationPurpose) {
		t.Fatal("expected verification code to remain valid after a wrong attempt")
	}
}

func TestVerificationCodeRedisDisabledFallsBackToMemory(t *testing.T) {
	resetVerificationTestState(t)
	RedisVerificationCodeEnabled = true
	RedisEnabled = false

	RegisterVerificationCodeWithKey("user@example.com", "abcdef", PasswordResetPurpose)
	if !VerifyCodeWithKey("user@example.com", "abcdef", PasswordResetPurpose) {
		t.Fatal("expected memory fallback to pass when redis is disabled")
	}
}

func TestVerificationCodeRedisErrorDoesNotAcceptMemoryFallback(t *testing.T) {
	resetVerificationTestState(t)
	client := redis.NewClient(&redis.Options{
		Addr:         "127.0.0.1:0",
		DialTimeout:  time.Millisecond,
		ReadTimeout:  time.Millisecond,
		WriteTimeout: time.Millisecond,
		MaxRetries:   -1,
	})
	t.Cleanup(func() {
		_ = client.Close()
	})
	RedisVerificationCodeEnabled = true
	RedisEnabled = true
	RDB = client

	registerVerificationCodeMemory("user@example.com", "abcdef", PasswordResetPurpose)
	if VerifyCodeWithKey("user@example.com", "abcdef", PasswordResetPurpose) {
		t.Fatal("expected redis verification errors to fail closed instead of accepting memory fallback")
	}

	RedisEnabled = false
	if !VerifyCodeWithKey("user@example.com", "abcdef", PasswordResetPurpose) {
		t.Fatal("expected failed-closed redis attempt to leave memory fallback unconsumed")
	}
}

func TestVerificationRedisKeyDoesNotExposeEmail(t *testing.T) {
	resetVerificationTestState(t)

	key := verificationRedisKey("User@Example.com", EmailVerificationPurpose)
	if key == "" || key == "User@Example.com" {
		t.Fatalf("unexpected redis key: %q", key)
	}
	if strings.Contains(strings.ToLower(key), "example.com") || strings.Contains(strings.ToLower(key), "user") {
		t.Fatalf("redis key should not expose email: %q", key)
	}
}

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

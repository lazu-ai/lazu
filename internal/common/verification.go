package common

import (
	"context"
	crand "crypto/rand"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"
)

type verificationValue struct {
	code string
	time time.Time
}

const (
	EmailVerificationPurpose = "v"
	PasswordResetPurpose     = "r"
	// EmailAuthPurpose backs the one code that both signs in and signs up.
	// It is deliberately a separate purpose from EmailVerificationPurpose: a
	// code minted to prove a new address must not also open an existing
	// session, and vice versa.
	EmailAuthPurpose = "a"
)

// MaxVerificationAttempts burns a code after this many wrong guesses. Six
// digits is only 10^6 wide, so per-request rate limiting alone would leave a
// patient attacker with a workable search; the code itself has to expire.
const MaxVerificationAttempts = 5

type verificationAttempt struct {
	count     int
	expiresAt time.Time
}

var verificationAttempts = make(map[string]verificationAttempt)

var verificationMutex sync.Mutex
var verificationMap map[string]verificationValue
var verificationMapMaxSize = 10
var VerificationValidMinutes = 10

var verificationGetDelScript = redis.NewScript(`
local value = redis.call("GET", KEYS[1])
if value == ARGV[1] then
  redis.call("DEL", KEYS[1])
end
return value
`)

func GenerateVerificationCode(length int) string {
	code := uuid.New().String()
	code = strings.Replace(code, "-", "", -1)
	if length == 0 {
		return code
	}
	return code[:length]
}

// GenerateNumericVerificationCode returns a digits-only code drawn from
// crypto/rand. Digits rather than hex so the code can be typed into a numeric
// one-time-code field: it brings up the number pad on a phone, autofills from
// the message, and has no 0/O or 1/l to misread. The narrower space is covered
// by MaxVerificationAttempts burning the code, not by making it longer.
func GenerateNumericVerificationCode(length int) (string, error) {
	if length <= 0 {
		return "", errors.New("verification code length must be positive")
	}
	digits := make([]byte, length)
	if _, err := crand.Read(digits); err != nil {
		return "", err
	}
	for index, value := range digits {
		digits[index] = '0' + value%10
	}
	return string(digits), nil
}

func RegisterVerificationCodeWithKey(key string, code string, purpose string) {
	if registerVerificationCodeRedis(key, code, purpose) {
		registerVerificationCodeMemory(key, code, purpose)
		return
	}
	registerVerificationCodeMemory(key, code, purpose)
}

func registerVerificationCodeMemory(key string, code string, purpose string) {
	verificationMutex.Lock()
	defer verificationMutex.Unlock()
	verificationMap[purpose+key] = verificationValue{
		code: code,
		time: time.Now(),
	}
	if len(verificationMap) > verificationMapMaxSize {
		removeExpiredPairs()
	}
}

func VerifyCodeWithKey(key string, code string, purpose string) bool {
	if RedisVerificationCodeEnabled && RedisEnabled && RDB != nil {
		ok, handled := verifyCodeRedis(key, code, purpose)
		if handled {
			if ok {
				deleteVerificationCodeMemory(key, purpose)
			}
			return ok
		}
		return false
	}
	return verifyCodeMemory(key, code, purpose)
}

func verifyCodeMemory(key string, code string, purpose string) bool {
	verificationMutex.Lock()
	defer verificationMutex.Unlock()
	mapKey := purpose + key
	value, okay := verificationMap[mapKey]
	now := time.Now()
	if !okay {
		return false
	}
	if int(now.Sub(value.time).Seconds()) >= VerificationValidMinutes*60 {
		delete(verificationMap, mapKey)
		return false
	}
	if code != value.code {
		return false
	}
	// Comparing and consuming must happen under the same lock. Splitting the
	// two operations lets concurrent reset requests both observe a valid token
	// before either one deletes it.
	delete(verificationMap, mapKey)
	return true
}

// RecordVerificationFailure counts one wrong code and reports whether that
// exhausted the allowance. When it has, the code is deleted: the person has to
// request a new one, which resets the search space the attacker was narrowing.
func RecordVerificationFailure(key string, purpose string) bool {
	attempts := recordVerificationFailureRedis(key, purpose)
	if attempts == 0 {
		attempts = recordVerificationFailureMemory(key, purpose)
	}
	if attempts >= MaxVerificationAttempts {
		DeleteKey(key, purpose)
		ClearVerificationFailures(key, purpose)
		return true
	}
	return false
}

// ClearVerificationFailures resets the allowance, called when a code is
// consumed successfully or replaced by a freshly issued one.
func ClearVerificationFailures(key string, purpose string) {
	if RedisVerificationCodeEnabled && RedisEnabled && RDB != nil {
		if err := RedisDel(verificationAttemptsRedisKey(key, purpose)); err != nil {
			SysLog(fmt.Sprintf("failed to clear redis verification attempts: %v", err))
		}
	}
	verificationMutex.Lock()
	defer verificationMutex.Unlock()
	delete(verificationAttempts, purpose+key)
}

func recordVerificationFailureMemory(key string, purpose string) int {
	verificationMutex.Lock()
	defer verificationMutex.Unlock()
	now := time.Now()
	for attemptKey, attempt := range verificationAttempts {
		if !now.Before(attempt.expiresAt) {
			delete(verificationAttempts, attemptKey)
		}
	}
	mapKey := purpose + key
	attempt := verificationAttempts[mapKey]
	if !now.Before(attempt.expiresAt) {
		attempt = verificationAttempt{
			expiresAt: now.Add(time.Duration(VerificationValidMinutes) * time.Minute),
		}
	}
	attempt.count++
	verificationAttempts[mapKey] = attempt
	return attempt.count
}

// recordVerificationFailureRedis returns 0 when Redis is unavailable, so the
// caller falls back to the in-process counter instead of counting nothing.
func recordVerificationFailureRedis(key string, purpose string) int {
	if !RedisVerificationCodeEnabled || !RedisEnabled || RDB == nil {
		return 0
	}
	redisKey := verificationAttemptsRedisKey(key, purpose)
	attempts, err := RDB.Incr(context.Background(), redisKey).Result()
	if err != nil {
		SysLog(fmt.Sprintf("failed to count redis verification attempt: %v", err))
		return 0
	}
	if attempts == 1 {
		if err := RDB.Expire(context.Background(), redisKey, time.Duration(VerificationValidMinutes)*time.Minute).Err(); err != nil {
			SysLog(fmt.Sprintf("failed to expire redis verification attempts: %v", err))
		}
	}
	return int(attempts)
}

func verificationAttemptsRedisKey(key string, purpose string) string {
	return verificationRedisKey(key, purpose) + ":attempts"
}

func DeleteKey(key string, purpose string) {
	if RedisVerificationCodeEnabled && RedisEnabled && RDB != nil {
		if err := RedisDel(verificationRedisKey(key, purpose)); err != nil {
			SysLog(fmt.Sprintf("failed to delete redis verification code: %v", err))
		}
	}
	deleteVerificationCodeMemory(key, purpose)
}

func deleteVerificationCodeMemory(key string, purpose string) {
	verificationMutex.Lock()
	defer verificationMutex.Unlock()
	delete(verificationMap, purpose+key)
}

func registerVerificationCodeRedis(key string, code string, purpose string) bool {
	if !RedisVerificationCodeEnabled || !RedisEnabled || RDB == nil {
		return false
	}
	err := RedisSet(verificationRedisKey(key, purpose), code, time.Duration(VerificationValidMinutes)*time.Minute)
	if err != nil {
		SysLog(fmt.Sprintf("failed to register redis verification code: %v", err))
		return false
	}
	return true
}

func verifyCodeRedis(key string, code string, purpose string) (bool, bool) {
	result, err := verificationGetDelScript.Run(context.Background(), RDB, []string{verificationRedisKey(key, purpose)}, code).Result()
	if err == redis.Nil {
		return false, true
	}
	if err != nil {
		SysLog(fmt.Sprintf("failed to verify redis verification code: %v", err))
		return false, false
	}
	stored, ok := result.(string)
	if !ok {
		return false, true
	}
	return stored == code, true
}

func verificationRedisKey(key string, purpose string) string {
	key = strings.ToLower(strings.TrimSpace(key))
	purpose = strings.TrimSpace(purpose)
	return "verification:" + purpose + ":" + GenerateHMAC(purpose+":"+key)
}

// no lock inside, so the caller must lock the verificationMap before calling!
func removeExpiredPairs() {
	now := time.Now()
	for key := range verificationMap {
		if int(now.Sub(verificationMap[key].time).Seconds()) >= VerificationValidMinutes*60 {
			delete(verificationMap, key)
		}
	}
}

func init() {
	verificationMutex.Lock()
	defer verificationMutex.Unlock()
	verificationMap = make(map[string]verificationValue)
}

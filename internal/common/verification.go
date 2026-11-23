package common

import (
	"context"
	"crypto/hmac"
	crand "crypto/rand"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"
)

const (
	PasswordResetPurpose = "r"
	// EmailAuthPurpose backs the one code that both signs in and signs up.
	// It is deliberately a separate purpose from email binding: a
	// code minted to prove a new address must not also open an existing
	// session, and vice versa.
	EmailAuthPurpose = "a"
)

// MaxVerificationAttempts burns a code after this many wrong guesses. Six
// digits is only 10^6 wide, so per-request rate limiting alone would leave a
// patient attacker with a workable search; the code itself has to expire.
const MaxVerificationAttempts = 5

const VerificationValidMinutes = 10

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

// VerificationCode binds one proof to its purpose under the caller's identity.
// Replacing an email address can require two proofs for the same operation.
type VerificationCode struct {
	Purpose string
	Code    string
}

type VerificationResult int

const (
	VerificationInvalid VerificationResult = iota
	VerificationAccepted
	VerificationExhausted
)

type verificationEntry struct {
	key    string
	digest string
	limit  int
}

func verificationEntries(identity string, codes []VerificationCode) ([]verificationEntry, error) {
	identity = strings.ToLower(strings.TrimSpace(identity))
	if identity == "" || len(codes) == 0 {
		return nil, errors.New("missing verification identity or purpose")
	}
	scope := GenerateHMAC("verification/identity/v2\x00" + identity)
	entries := make([]verificationEntry, len(codes))
	for i, proof := range codes {
		if proof.Purpose == "" {
			return nil, errors.New("empty verification purpose")
		}
		key := "verification:v2:{" + scope + "}:" + proof.Purpose
		limit := MaxVerificationAttempts
		// Reset links are high-entropy and bound to the account/session version.
		// A wrong token must not revoke someone else's outstanding reset link.
		if proof.Purpose == PasswordResetPurpose {
			limit = 0
		}
		entries[i] = verificationEntry{key: key, digest: GenerateHMAC("verification/proof/v2\x00" + key + "\x00" + proof.Code), limit: limit}
	}
	return entries, nil
}

// A single script writes every required proof before either email goes out.
var issueVerificationScript = redis.NewScript(`
for i, key in ipairs(KEYS) do
  redis.call("HSET", key, "digest", ARGV[2*i], "failures", 0)
  redis.call("EXPIRE", key, ARGV[1])
end
return 1
`)

// Check all proofs before consuming any. For numeric codes a missing proof
// has the same failure allowance as a live one, regardless of mailbox state.
var consumeVerificationScript = redis.NewScript(`
for i, key in ipairs(KEYS) do
  local digest = redis.call("HGET", key, "digest")
  if digest ~= ARGV[2*i] then
    local limit = tonumber(ARGV[2*i+1])
    if limit > 0 then
      local failures = redis.call("HINCRBY", key, "failures", 1)
      if failures >= limit then
        redis.call("DEL", key)
        return 2
      end
      if not digest and failures == 1 then redis.call("EXPIRE", key, ARGV[1]) end
    end
    return 0
  end
end
redis.call("DEL", unpack(KEYS))
return 1
`)

func runVerificationScript(script *redis.Script, entries []verificationEntry) (VerificationResult, error) {
	if err := ensureRedisClient(); err != nil {
		return VerificationInvalid, err
	}
	keys := make([]string, 0, len(entries))
	args := []interface{}{VerificationValidMinutes * 60}
	for _, entry := range entries {
		keys = append(keys, entry.key)
		args = append(args, entry.digest, entry.limit)
	}
	result, err := script.Run(context.Background(), RDB, keys, args...).Int()
	return VerificationResult(result), err
}

type memoryVerification struct {
	digest   string
	failures int
	expires  time.Time
}

var verificationMemory = struct {
	sync.Mutex
	values      map[string]memoryVerification
	nextCleanup time.Time
}{values: make(map[string]memoryVerification)}

func IssueVerificationCodes(identity string, codes ...VerificationCode) error {
	for _, proof := range codes {
		if proof.Code == "" {
			return errors.New("empty verification code")
		}
	}
	entries, err := verificationEntries(identity, codes)
	if err != nil {
		return err
	}
	if RedisVerificationCodeEnabled {
		_, err = runVerificationScript(issueVerificationScript, entries)
		return err
	}
	verificationMemory.Lock()
	defer verificationMemory.Unlock()
	now := time.Now()
	cleanExpiredVerification(now)
	for _, entry := range entries {
		verificationMemory.values[entry.key] = memoryVerification{digest: entry.digest, expires: now.Add(VerificationValidMinutes * time.Minute)}
	}
	return nil
}

// Caller holds the memory store lock. Each read also checks its own expiry.
func cleanExpiredVerification(now time.Time) {
	if now.Before(verificationMemory.nextCleanup) {
		return
	}
	for k, v := range verificationMemory.values {
		if !now.Before(v.expires) {
			delete(verificationMemory.values, k)
		}
	}
	verificationMemory.nextCleanup = now.Add(VerificationValidMinutes * time.Minute)
}

func ConsumeVerificationCodes(identity string, codes ...VerificationCode) (VerificationResult, error) {
	entries, err := verificationEntries(identity, codes)
	if err != nil {
		return VerificationInvalid, err
	}
	if RedisVerificationCodeEnabled {
		return runVerificationScript(consumeVerificationScript, entries)
	}
	verificationMemory.Lock()
	defer verificationMemory.Unlock()
	now := time.Now()
	cleanExpiredVerification(now)
	for _, entry := range entries {
		v := verificationMemory.values[entry.key]
		if !now.Before(v.expires) {
			delete(verificationMemory.values, entry.key)
			v = memoryVerification{expires: now.Add(VerificationValidMinutes * time.Minute)}
		}
		if hmac.Equal([]byte(v.digest), []byte(entry.digest)) {
			continue
		}
		if entry.limit > 0 {
			v.failures++
			if v.failures >= entry.limit {
				delete(verificationMemory.values, entry.key)
				return VerificationExhausted, nil
			}
			verificationMemory.values[entry.key] = v
		}
		return VerificationInvalid, nil
	}
	for _, entry := range entries {
		delete(verificationMemory.values, entry.key)
	}
	return VerificationAccepted, nil
}

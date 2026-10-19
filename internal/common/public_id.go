package common

import (
	"strings"

	"github.com/oklog/ulid/v2"
)

const (
	APITokenPrefix        = "sk-lazu-"
	ChatCompletionPrefix  = "chatcmpl-lazu-"
	FileIDPrefix          = "file-lazu-"
	RequestIDPrefix       = "req_lazu_"
	PublicUserIDPrefix    = "usr"
	PublicTokenIDPrefix   = "tok"
	PublicChannelIDPrefix = "ch"
	PublicLogIDPrefix     = "log"
	PublicVendorIDPrefix  = "vnd"
	PublicAuditIDPrefix   = "aud"
)

func GenerateULID() string {
	return ulid.Make().String()
}

func GeneratePublicID(prefix string) string {
	return prefix + "_" + GenerateULID()
}

func GenerateChatCompletionID() string {
	return ChatCompletionPrefix + GenerateULID()
}

func GenerateFileID() string {
	return FileIDPrefix + GenerateULID()
}

func GenerateRequestID() string {
	return RequestIDPrefix + GenerateULID()
}

func NormalizeAPIKey(raw string) string {
	key := strings.TrimSpace(raw)
	if key == "" {
		return ""
	}
	parts := strings.Fields(key)
	if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
		key = strings.TrimSpace(parts[1])
	}
	if strings.HasPrefix(key, APITokenPrefix) {
		return key
	}
	if strings.HasPrefix(key, "sk-") {
		return APITokenPrefix + strings.TrimPrefix(key, "sk-")
	}
	// Legacy keys were stored as raw 48-character base62 strings.
	if len(key) == 48 {
		return APITokenPrefix + key
	}
	return key
}

func APIKeyLookupCandidates(raw string) []string {
	normalized := NormalizeAPIKey(raw)
	if normalized == "" {
		return nil
	}
	candidates := []string{normalized}
	if strings.HasPrefix(normalized, APITokenPrefix) {
		legacy := strings.TrimPrefix(normalized, APITokenPrefix)
		if legacy != "" {
			candidates = append(candidates, legacy)
			candidates = append(candidates, "sk-"+legacy)
		}
	}
	seen := make(map[string]struct{}, len(candidates))
	unique := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		unique = append(unique, candidate)
	}
	return unique
}

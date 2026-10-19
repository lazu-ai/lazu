package model

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/lazu-ai/lazu/internal/common"

	"gorm.io/gorm"
)

const channelKeyEnvelopePrefix = "lazu:v1:aesgcm:"
const channelKeyDigestNoSearchMatch = "lazu:no-channel-key-digest-match"
const MediaJobProviderCredentialSnapshotKey = "_provider_credential"

var channelKeyAAD = []byte("lazu:channel-key:v1")

var errChannelKeyStableSecretRequired = errors.New("CRYPTO_SECRET or SESSION_SECRET is required for channel key encryption")

func IsEncryptedChannelKey(value string) bool {
	return strings.HasPrefix(value, channelKeyEnvelopePrefix)
}

func channelKeyRootSecret() (string, error) {
	secret := strings.TrimSpace(common.CryptoSecret)
	if secret == "" || secret == "random_string" || !channelKeyStableSecretConfigured() {
		return "", errChannelKeyStableSecretRequired
	}
	return secret, nil
}

func channelKeyStableSecretConfigured() bool {
	if strings.TrimSpace(os.Getenv("CRYPTO_SECRET")) != "" ||
		strings.TrimSpace(os.Getenv("SESSION_SECRET")) != "" {
		return true
	}
	return strings.TrimSpace(common.CryptoSecret) != "" && common.CryptoSecret != common.SessionSecret
}

func channelKeyMaterial(label string) ([]byte, error) {
	secret, err := channelKeyRootSecret()
	if err != nil {
		return nil, err
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(label))
	return mac.Sum(nil), nil
}

func channelKeyAEAD() (cipher.AEAD, error) {
	key, err := channelKeyMaterial("lazu/channel-key/aes-gcm/v1")
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func channelKeyDigest(clearKey string) (string, error) {
	clearKey = strings.TrimSpace(clearKey)
	if clearKey == "" {
		return "", nil
	}
	key, err := channelKeyMaterial("lazu/channel-key/digest/v1")
	if err != nil {
		return "", err
	}
	return common.GenerateHMACWithKey(key, clearKey), nil
}

func channelKeyDigestSet(clearKey string) (string, string, error) {
	digest, err := channelKeyDigest(clearKey)
	if err != nil || digest == "" {
		return digest, "", err
	}
	candidates := splitChannelKeyCandidates(clearKey)
	if len(candidates) == 0 {
		return digest, channelKeyDigestEnvelope([]string{digest}), nil
	}
	seen := map[string]struct{}{}
	digests := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		candidateDigest, err := channelKeyDigest(candidate)
		if err != nil {
			return "", "", err
		}
		if candidateDigest == "" {
			continue
		}
		if _, ok := seen[candidateDigest]; ok {
			continue
		}
		seen[candidateDigest] = struct{}{}
		digests = append(digests, candidateDigest)
	}
	if len(digests) == 0 {
		digests = append(digests, digest)
	}
	return digest, channelKeyDigestEnvelope(digests), nil
}

func splitChannelKeyCandidates(clearKey string) []string {
	trimmed := strings.TrimSpace(clearKey)
	if trimmed == "" {
		return nil
	}
	if strings.HasPrefix(trimmed, "[") {
		var arr []json.RawMessage
		if err := common.Unmarshal([]byte(trimmed), &arr); err == nil {
			candidates := make([]string, 0, len(arr))
			for _, raw := range arr {
				var text string
				if err := common.Unmarshal(raw, &text); err == nil {
					candidates = append(candidates, text)
					continue
				}
				candidates = append(candidates, string(raw))
			}
			return compactChannelKeyCandidates(candidates)
		}
	}
	return compactChannelKeyCandidates(strings.Split(strings.Trim(clearKey, "\n"), "\n"))
}

func compactChannelKeyCandidates(candidates []string) []string {
	out := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate != "" {
			out = append(out, candidate)
		}
	}
	return out
}

func channelKeyDigestEnvelope(digests []string) string {
	if len(digests) == 0 {
		return ""
	}
	return "\n" + strings.Join(digests, "\n") + "\n"
}

func channelKeyDigestSearchPattern(digest string) string {
	if digest == "" {
		return ""
	}
	return "%\n" + digest + "\n%"
}

func channelKeyDigestSearchValues(keyword string) (string, string, error) {
	if strings.TrimSpace(keyword) == "" {
		return "", "", nil
	}
	digest, err := channelKeyDigest(keyword)
	if err != nil {
		if errors.Is(err, errChannelKeyStableSecretRequired) {
			return channelKeyDigestNoSearchMatch, channelKeyDigestSearchPattern(channelKeyDigestNoSearchMatch), nil
		}
		return "", "", err
	}
	return digest, channelKeyDigestSearchPattern(digest), nil
}

func encryptChannelKey(clearKey string) (string, error) {
	if clearKey == "" {
		return "", nil
	}
	if IsEncryptedChannelKey(clearKey) {
		return clearKey, nil
	}
	aead, err := channelKeyAEAD()
	if err != nil {
		if errors.Is(err, errChannelKeyStableSecretRequired) {
			return clearKey, nil
		}
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ciphertext := aead.Seal(nil, nonce, []byte(clearKey), channelKeyAAD)
	payload := make([]byte, 0, len(nonce)+len(ciphertext))
	payload = append(payload, nonce...)
	payload = append(payload, ciphertext...)
	return channelKeyEnvelopePrefix + base64.RawURLEncoding.EncodeToString(payload), nil
}

func decryptChannelKey(storedKey string) (string, error) {
	if storedKey == "" || !IsEncryptedChannelKey(storedKey) {
		return storedKey, nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(storedKey, channelKeyEnvelopePrefix))
	if err != nil {
		return "", fmt.Errorf("decrypt channel key: invalid envelope: %w", err)
	}
	aead, err := channelKeyAEAD()
	if err != nil {
		return "", err
	}
	if len(payload) <= aead.NonceSize() {
		return "", errors.New("decrypt channel key: invalid envelope size")
	}
	nonce := payload[:aead.NonceSize()]
	ciphertext := payload[aead.NonceSize():]
	clearKey, err := aead.Open(nil, nonce, ciphertext, channelKeyAAD)
	if err != nil {
		return "", fmt.Errorf("decrypt channel key: %w", err)
	}
	return string(clearKey), nil
}

func EncryptMediaJobProviderCredential(clearKey string) (string, error) {
	if strings.TrimSpace(clearKey) == "" {
		return "", errors.New("media job provider credential is required")
	}
	storedKey, err := encryptChannelKey(clearKey)
	if err != nil {
		return "", err
	}
	if !IsEncryptedChannelKey(storedKey) {
		return "", errChannelKeyStableSecretRequired
	}
	return storedKey, nil
}

func DecryptMediaJobProviderCredential(storedKey string) (string, error) {
	if !IsEncryptedChannelKey(storedKey) {
		return "", errors.New("media job provider credential is not encrypted")
	}
	return decryptChannelKey(storedKey)
}

func prepareChannelKeyForStorage(clearKey string) (storedKey string, digest string, digests string, err error) {
	if clearKey == "" {
		return "", "", "", nil
	}
	plainKey := clearKey
	if IsEncryptedChannelKey(clearKey) {
		plainKey, err = decryptChannelKey(clearKey)
		if err != nil {
			return "", "", "", err
		}
		storedKey = clearKey
	} else {
		storedKey, err = encryptChannelKey(clearKey)
		if err != nil {
			return "", "", "", err
		}
	}
	digest, digests, err = channelKeyDigestSet(plainKey)
	if err != nil {
		if errors.Is(err, errChannelKeyStableSecretRequired) {
			return storedKey, "", "", nil
		}
		return "", "", "", err
	}
	return storedKey, digest, digests, nil
}

func (channel *Channel) prepareKeyForStorage(clearKey string) error {
	if channel == nil || clearKey == "" {
		return nil
	}
	storedKey, digest, digests, err := prepareChannelKeyForStorage(clearKey)
	if err != nil {
		return err
	}
	channel.Key = storedKey
	channel.KeyDigest = digest
	channel.KeyDigests = digests
	return nil
}

func (channel *Channel) storageCopyWithEncryptedKey() (*Channel, error) {
	if channel == nil {
		return nil, errors.New("channel cannot be nil")
	}
	storage := *channel
	if err := storage.prepareKeyForStorage(storage.Key); err != nil {
		return nil, err
	}
	return &storage, nil
}

func (channel *Channel) decryptKeyForUse() error {
	if channel == nil || channel.Key == "" {
		return nil
	}
	clearKey, err := decryptChannelKey(channel.Key)
	if err != nil {
		return err
	}
	channel.Key = clearKey
	return nil
}

// DecryptChannelKeysForUse converts encrypted channel keys to plaintext for
// relay paths that intentionally need the upstream credential in memory.
func DecryptChannelKeysForUse(channels []*Channel) error {
	for _, channel := range channels {
		if err := channel.decryptKeyForUse(); err != nil {
			channelID := 0
			if channel != nil {
				channelID = channel.Id
			}
			return fmt.Errorf("channel_id=%d: %w", channelID, err)
		}
	}
	return nil
}

func decryptChannelKeysForUse(channels []*Channel) error {
	return DecryptChannelKeysForUse(channels)
}

func migrateChannelKeysToEncrypted(db *gorm.DB) error {
	if db == nil || !db.Migrator().HasTable(&Channel{}) || !db.Migrator().HasColumn(&Channel{}, "key") {
		return nil
	}
	if _, err := channelKeyRootSecret(); err != nil {
		if errors.Is(err, errChannelKeyStableSecretRequired) {
			common.SysLog("skipping channel key encryption migration: " + err.Error())
			return nil
		}
		return err
	}

	hasDigest := db.Migrator().HasColumn(&Channel{}, "key_digest")
	hasDigests := db.Migrator().HasColumn(&Channel{}, "key_digests")
	selectFields := "id, " + commonKeyCol
	if hasDigest {
		selectFields += ", key_digest"
	}
	if hasDigests {
		selectFields += ", key_digests"
	}
	var rows []struct {
		ID         int    `gorm:"column:id"`
		Key        string `gorm:"column:key"`
		KeyDigest  string `gorm:"column:key_digest"`
		KeyDigests string `gorm:"column:key_digests"`
	}
	if err := db.Table("channels").
		Select(selectFields).
		Where(commonKeyCol + " IS NOT NULL AND " + commonKeyCol + " != ''").
		Find(&rows).Error; err != nil {
		return err
	}

	migrated := 0
	for _, row := range rows {
		if row.ID <= 0 || row.Key == "" {
			continue
		}
		clearKey := row.Key
		storedKey := row.Key
		var err error
		if IsEncryptedChannelKey(row.Key) {
			clearKey, err = decryptChannelKey(row.Key)
			if err != nil {
				return fmt.Errorf("validate encrypted channel key channel_id=%d: %w", row.ID, err)
			}
		} else {
			storedKey, err = encryptChannelKey(row.Key)
			if err != nil {
				return fmt.Errorf("encrypt legacy channel key channel_id=%d: %w", row.ID, err)
			}
		}
		digest, digests, err := channelKeyDigestSet(clearKey)
		if err != nil {
			return fmt.Errorf("digest channel key channel_id=%d: %w", row.ID, err)
		}

		updates := map[string]interface{}{}
		if storedKey != row.Key {
			updates["key"] = storedKey
		}
		if hasDigest && row.KeyDigest != digest {
			updates["key_digest"] = digest
		}
		if hasDigests && row.KeyDigests != digests {
			updates["key_digests"] = digests
		}
		if len(updates) == 0 {
			continue
		}
		if err := db.Table("channels").Where("id = ?", row.ID).Updates(updates).Error; err != nil {
			return err
		}
		migrated++
	}
	if migrated > 0 {
		common.SysLog(fmt.Sprintf("migrated %d channel keys to encrypted storage", migrated))
	}
	return nil
}

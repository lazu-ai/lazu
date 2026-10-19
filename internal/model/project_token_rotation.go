package model

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Secondary credentials have no independent budget or resource identity.
type TokenCredential struct {
	ID        uint   `gorm:"primaryKey"`
	TokenID   int    `gorm:"not null;index"`
	Digest    string `gorm:"size:64;not null;uniqueIndex"`
	ExpiresAt int64  `gorm:"not null;index"`
	CreatedAt int64  `gorm:"autoCreateTime"`
}
type TokenRotation struct {
	ID           uint   `gorm:"primaryKey"`
	TokenID      int    `gorm:"not null;uniqueIndex:idx_token_rotation_operation,priority:1"`
	OperationKey string `gorm:"size:128;not null;uniqueIndex:idx_token_rotation_operation,priority:2"`
	ActorID      int    `gorm:"not null"`
	OldExpires   string `gorm:"size:8;not null"`
	OldExpiresAt int64
	KeyCipher    string `json:"-" gorm:"type:text;not null"`
	Version      int64
	CreatedAt    int64 `gorm:"autoCreateTime"`
}
type TokenRotationResult struct {
	Key          string `json:"key"`
	OldExpiresAt int64  `json:"old_expires_at"`
	Version      int64  `json:"version"`
}

func tokenRotationAAD(tokenID int, operationKey string) []byte {
	return []byte(fmt.Sprintf("rotation:%d:%s", tokenID, operationKey))
}
func sealTokenRotationKey(tokenID int, operationKey, key string) (string, error) {
	aead, err := projectNotificationCipher()
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	return base64.RawStdEncoding.EncodeToString(aead.Seal(nonce, nonce, []byte(key), tokenRotationAAD(tokenID, operationKey))), nil
}
func openTokenRotationKey(row TokenRotation) (string, error) {
	raw, err := base64.RawStdEncoding.DecodeString(row.KeyCipher)
	if err != nil {
		return "", errors.New("invalid rotation receipt")
	}
	aead, err := projectNotificationCipher()
	if err != nil {
		return "", err
	}
	if len(raw) < aead.NonceSize() {
		return "", errors.New("invalid rotation receipt")
	}
	result, err := aead.Open(nil, raw[:aead.NonceSize()], raw[aead.NonceSize():], tokenRotationAAD(row.TokenID, row.OperationKey))
	if err != nil {
		return "", errors.New("unable to recover rotation receipt")
	}
	return string(result), nil
}

func RotateProjectToken(projectID, viewerID, tokenID int, operationKey, oldExpires string, now time.Time) (TokenRotationResult, error) {
	var result TokenRotationResult
	if strings.TrimSpace(operationKey) == "" || len(operationKey) > 128 || oldExpires != "now" && oldExpires != "24h" {
		return result, ErrProjectInvalidInput
	}
	var updated Token
	err := CoreDB().Transaction(func(tx *gorm.DB) error {
		var project Project
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&project, projectID).Error; err != nil {
			return err
		}
		var token Token
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND project_id = ?", tokenID, projectID).First(&token).Error; err != nil {
			return err
		}
		if _, err := CheckProjectTokenAction(tx, projectID, viewerID, tokenID, ProjectManagePersonalKey); err != nil {
			return err
		}
		var receipt TokenRotation
		err := tx.Where("token_id = ? AND operation_key = ?", tokenID, operationKey).First(&receipt).Error
		if err == nil {
			if receipt.ActorID != viewerID || receipt.OldExpires != oldExpires {
				return ErrProjectStateConflict
			}
			// A later rotation supersedes this operation's secret. Never expose
			// a different current credential as the result of the old request.
			key, err := openTokenRotationKey(receipt)
			if err != nil {
				return err
			}
			if TokenKeyDigest(key) != token.KeyDigest {
				return ErrProjectStateConflict
			}
			result = TokenRotationResult{Key: key, OldExpiresAt: receipt.OldExpiresAt, Version: receipt.Version}
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		expires := now.Unix()
		if oldExpires == "24h" {
			expires = now.Add(24 * time.Hour).Unix()
		}
		if token.ExpiredTime != -1 && token.ExpiredTime < expires {
			expires = token.ExpiredTime
		}
		if expires > now.Unix() {
			// A row written before Keys were stored as digests keeps its plaintext
			// in `key`; hashing it preserves the requested overlap. When neither a
			// digest nor a readable plaintext exists the old credential cannot be
			// kept alive at all, so report the window honestly instead of promising
			// 24 hours and cutting the caller off immediately.
			oldDigest := token.KeyDigest
			if oldDigest == "" && token.CanRevealKey() {
				oldDigest = TokenKeyDigest(token.Key)
			}
			if oldDigest == "" {
				expires = now.Unix()
			} else if err := tx.Create(&TokenCredential{TokenID: token.Id, Digest: oldDigest, ExpiresAt: expires}).Error; err != nil {
				return err
			}
		}
		key, err := common.GenerateKey()
		if err != nil {
			return err
		}
		if err := token.prepareKeyForStorage(key); err != nil {
			return err
		}
		token.ProjectVersion++
		if err := tx.Model(&token).Select("key", "key_digest", "key_prefix", "key_suffix", "project_version").Updates(&token).Error; err != nil {
			return err
		}
		sealed, err := sealTokenRotationKey(tokenID, operationKey, key)
		if err != nil {
			return err
		}
		receipt = TokenRotation{TokenID: tokenID, OperationKey: operationKey, ActorID: viewerID, OldExpires: oldExpires, OldExpiresAt: expires, KeyCipher: sealed, Version: token.ProjectVersion}
		if err := tx.Create(&receipt).Error; err != nil {
			return err
		}
		var actor User
		if err := tx.Select("username").First(&actor, viewerID).Error; err != nil {
			return err
		}
		if err := tx.Create(&TokenAuditLog{TokenID: token.Id, TokenPublicID: token.PublicID, UserID: viewerID, Username: actor.Username, Action: "token.rotate", BeforeState: "{}", AfterState: "{}"}).Error; err != nil {
			return err
		}
		recipient := token.UserId
		if recipient != viewerID {
			var member ProjectMember
			if err := tx.Where("project_id = ? AND user_id = ?", projectID, recipient).First(&member).Error; err != nil {
				return err
			}
			if err := queueProjectMemberNoticeTx(tx, project, member, projectNotice{Kind: noticeKeyRotated, ActorID: viewerID, TokenName: projectNoticeTokenName(token)}, fmt.Sprintf("rotation:%d", receipt.ID)); err != nil {
				return err
			}
		}
		updated = token
		result = TokenRotationResult{Key: key, OldExpiresAt: expires, Version: token.ProjectVersion}
		return nil
	})
	if err != nil {
		return result, err
	}
	if updated.Id > 0 {
		if err := synchronizeTokenCachesAfterUpdate(&updated); err != nil {
			return result, &TokenMutationCommittedError{Operation: "rotation", Cause: err}
		}
	}
	return result, nil
}

// ResolveTokenCredential reads the current logical Key on every request. This
// prevents stale alias/projection caches from extending credential lifetime or
// retaining old policy after a Key update.
func ResolveTokenCredential(tokenID int, key string, now time.Time) (*Token, error) {
	var token Token
	if tokenID <= 0 {
		return nil, gorm.ErrRecordNotFound
	}
	if err := CoreDB().First(&token, tokenID).Error; err != nil {
		return nil, err
	}
	candidates := common.APIKeyLookupCandidates(key)
	digests := tokenKeyLookupDigests(candidates)
	if len(digests) == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	current := false
	for _, digest := range digests {
		if token.KeyDigest == digest || TokenKeyDigest(token.Key) == digest {
			current = true
			break
		}
	}
	if !current {
		var count int64
		if err := CoreDB().Model(&TokenCredential{}).Where("token_id = ? AND digest IN ? AND expires_at > ?", tokenID, digests, now.Unix()).Count(&count).Error; err != nil {
			return nil, err
		}
		if count != 1 {
			return nil, gorm.ErrRecordNotFound
		}
	}
	if token.IsInternal() {
		return nil, gorm.ErrRecordNotFound
	}
	token.Key = common.NormalizeAPIKey(key)
	return &token, nil
}

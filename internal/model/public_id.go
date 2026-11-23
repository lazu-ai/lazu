package model

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/lazu-ai/lazu/internal/common"

	"gorm.io/gorm"
)

func (user *User) BeforeCreate(tx *gorm.DB) error {
	if user.PublicID == "" {
		user.PublicID = common.GeneratePublicID(common.PublicUserIDPrefix)
	}
	// A row created now is a real registration; only the schema-15 backfill
	// writes an inferred source. Sign-up attribution, referral sign-ups and
	// the home-surface mark all require an exact, recent created_at.
	if user.CreatedAt == 0 {
		user.CreatedAt = time.Now().Unix()
	}
	if user.CreatedAtSource == "" {
		user.CreatedAtSource = UserCreatedAtSourceExact
	}
	return nil
}

func (token *Token) BeforeCreate(tx *gorm.DB) error {
	if token.ProjectID == 0 && tx.Migrator().HasTable(&Project{}) {
		var project Project
		if err := tx.Where("owner_user_id = ?", token.UserId).First(&project).Error; err != nil {
			return err
		}
		token.ProjectID = project.ID
	}
	if token.PublicID == "" {
		token.PublicID = common.GeneratePublicID(common.PublicTokenIDPrefix)
	}
	return token.prepareKeyForStorage(token.Key)
}

func (channel *Channel) BeforeCreate(tx *gorm.DB) error {
	if channel.PublicID == "" {
		channel.PublicID = common.GeneratePublicID(common.PublicChannelIDPrefix)
	}
	return channel.prepareKeyForStorage(channel.Key)
}

func (channel *Channel) BeforeUpdate(tx *gorm.DB) error {
	if !tx.Statement.Changed("Key") {
		return nil
	}
	return channel.prepareKeyForStorage(channel.Key)
}

func (log *Log) BeforeCreate(tx *gorm.DB) error {
	if log.RequestTS == 0 {
		log.RequestTS = requestTimestamp(log.RequestID, log.CreatedAt)
	}
	ensureConsumeLogPublicID(log)
	if log.PublicID == "" {
		log.PublicID = common.GeneratePublicID(common.PublicLogIDPrefix)
	}
	return nil
}

func (vendor *Vendor) BeforeCreate(tx *gorm.DB) error {
	if vendor.PublicID == "" {
		vendor.PublicID = common.GeneratePublicID(common.PublicVendorIDPrefix)
	}
	return nil
}

func (file *File) BeforeCreate(tx *gorm.DB) error {
	if file.ProjectID == 0 {
		principal, err := captureLegacyResourceIdentity(tx, file.UserID, file.TokenID)
		if err != nil {
			return err
		}
		file.ProjectID, file.PayerUserID, file.ActorUserID = principal.ProjectID, principal.PayerUserID, principal.ActorUserID
	}
	if file.ID == "" {
		file.ID = common.GenerateFileID()
	}
	if file.PublicID == "" {
		file.PublicID = file.ID
	}
	return nil
}

func ResolvePublicIntID(table string, identifier string) (int, error) {
	identifier = strings.TrimSpace(identifier)
	if identifier == "" {
		return 0, errors.New("id is required")
	}
	if id, err := strconv.Atoi(identifier); err == nil {
		return id, nil
	}
	var id int
	if err := CoreDB().Table(table).Select("id").Where("public_id = ?", identifier).Scan(&id).Error; err != nil {
		return 0, err
	}
	if id == 0 {
		return 0, gorm.ErrRecordNotFound
	}
	return id, nil
}

func ResolvePublicIntIDs(table string, identifiers []string) ([]int, error) {
	ids := make([]int, 0, len(identifiers))
	for _, identifier := range identifiers {
		id, err := ResolvePublicIntID(table, identifier)
		if err != nil {
			return nil, fmt.Errorf("resolve %s id %q: %w", table, identifier, err)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func migrateLazuLogPublicIDs(db *gorm.DB) error {
	return backfillIntPublicIDs(db, "logs", common.PublicLogIDPrefix)
}

func backfillIntPublicIDs(db *gorm.DB, table string, prefix string) error {
	if db == nil || !db.Migrator().HasTable(table) || !db.Migrator().HasColumn(table, "public_id") {
		return nil
	}
	var rows []struct {
		ID int `gorm:"column:id"`
	}
	if err := db.Table(table).
		Select("id").
		Where("public_id IS NULL OR public_id = ''").
		Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		if row.ID <= 0 {
			continue
		}
		if err := db.Table(table).
			Where("id = ? AND (public_id IS NULL OR public_id = '')", row.ID).
			Update("public_id", common.GeneratePublicID(prefix)).Error; err != nil {
			return err
		}
	}
	return nil
}

func backfillTokenKeyDigest(tokenID int, clearKey string) {
	if CoreDB() == nil || tokenID <= 0 || clearKey == "" {
		return
	}
	if err := backfillTokenKeyDigestWithDB(CoreDB(), tokenID, clearKey); err != nil {
		common.SysLog(fmt.Sprintf("failed to backfill token key digest token_id=%d: %v", tokenID, err))
	}
}

func backfillTokenKeyDigestWithDB(db *gorm.DB, tokenID int, clearKey string) error {
	clearKey = common.NormalizeAPIKey(clearKey)
	digest := TokenKeyDigest(clearKey)
	if db == nil || tokenID <= 0 || digest == "" {
		return nil
	}
	var existing Token
	err := db.Select("id").Where("key_digest = ? AND id <> ?", digest, tokenID).First(&existing).Error
	if err == nil && existing.Id > 0 {
		common.SysWarn(fmt.Sprintf("skip token key digest backfill for token_id=%d: digest already exists on token_id=%d", tokenID, existing.Id))
		return nil
	}
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	updates := map[string]interface{}{
		"key_digest": digest,
		"key_prefix": tokenKeyPrefix(clearKey),
		"key_suffix": tokenKeySuffix(clearKey),
	}
	return db.Table("tokens").Where("id = ? AND (key_digest IS NULL OR key_digest = '')", tokenID).Updates(updates).Error
}

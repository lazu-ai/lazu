package model

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrLegacySettlementWaived = errors.New("legacy additional settlement was waived by the platform, not paid")

// LedgerLegacySettlementWaiver retains a separate, permanent disposition. It
// neither resolves the original failure nor changes any wallet/key balance.
// Sensitive source values are retained only as SHA256 commitments.
type LedgerLegacySettlementWaiver struct {
	FailureID       uint      `gorm:"primaryKey;autoIncrement:false" json:"failure_id"`
	OperationSHA256 string    `gorm:"size:64;not null;uniqueIndex" json:"operation_sha256"`
	RequestID       string    `gorm:"size:64;not null;uniqueIndex" json:"request_id"`
	UserID          int       `gorm:"not null;index" json:"user_id"`
	ProjectID       int       `gorm:"not null" json:"project_id"`
	ActorUserID     int       `gorm:"not null" json:"actor_user_id"`
	TokenID         int       `gorm:"not null" json:"token_id"`
	TokenUnlimited  bool      `json:"token_unlimited"`
	IsPlayground    bool      `json:"is_playground"`
	QuotaDelta      int64     `gorm:"not null" json:"quota_delta"`
	PreConsumed     int64     `gorm:"not null" json:"pre_consumed"`
	RefType         string    `gorm:"size:64;not null" json:"ref_type"`
	RefID           string    `gorm:"size:191;not null" json:"ref_id"`
	PricingVersion  int64     `gorm:"not null" json:"pricing_version"`
	SourceSHA256    string    `gorm:"size:64;not null" json:"source_sha256"`
	TokenKeySHA256  string    `gorm:"size:64;not null" json:"token_key_sha256"`
	MetadataSHA256  string    `gorm:"size:64;not null" json:"metadata_sha256"`
	WaivedMicrousd  int64     `gorm:"not null" json:"waived_microusd"`
	Reason          string    `gorm:"size:512;not null" json:"reason"`
	Actor           string    `gorm:"size:191;not null" json:"actor"`
	EvidenceSHA256  string    `gorm:"size:64;not null" json:"evidence_sha256"`
	CreatedAt       time.Time `gorm:"not null" json:"created_at"`
	RecordSHA256    string    `gorm:"size:64;not null" json:"record_sha256"`
}

func (*LedgerLegacySettlementWaiver) BeforeUpdate(*gorm.DB) error {
	return errors.New("legacy settlement waiver is immutable")
}
func (*LedgerLegacySettlementWaiver) BeforeDelete(*gorm.DB) error {
	return errors.New("legacy settlement waiver is permanent")
}

type LedgerLegacySettlementWaiverItem struct {
	FailureID      uint   `json:"failure_id"`
	SourceSHA256   string `json:"source_sha256"`
	AmountMicrousd int64  `json:"amount_microusd"`
}
type LedgerLegacySettlementWaiverBatch struct {
	Items                 []LedgerLegacySettlementWaiverItem `json:"items"`
	ExpectedTotalMicrousd int64                              `json:"expected_total_microusd"`
	MaxTotalMicrousd      int64                              `json:"max_total_microusd"`
	Reason                string                             `json:"reason"`
	Actor                 string                             `json:"actor"`
	EvidenceSHA256        string                             `json:"evidence_sha256"`
	At                    time.Time                          `json:"at"`
	WritersStopped        bool                               `json:"writers_stopped"`
}
type LedgerLegacySettlementWaiverReport struct {
	Failures       int   `json:"failures"`
	Created        int   `json:"created"`
	Existing       int   `json:"existing"`
	WaivedMicrousd int64 `json:"waived_microusd"`
}

func legacyWaiverHash(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return legacyWaiverStringHash(string(encoded)), nil
}
func legacyWaiverStringHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
func legacyWaiverValidHash(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size && value == strings.ToLower(value)
}

// LedgerLegacySettlementFailureHash commits to EVERY persisted source field,
// including status/retry state and secret-bearing fields, without returning them.
func LedgerLegacySettlementFailureHash(row SettlementFailure) (string, error) {
	row.CreatedAt, row.UpdatedAt = row.CreatedAt.UTC(), row.UpdatedAt.UTC()
	if row.NextRetryAt != nil {
		at := row.NextRetryAt.UTC()
		row.NextRetryAt = &at
	}
	if row.ResolvedAt != nil {
		at := row.ResolvedAt.UTC()
		row.ResolvedAt = &at
	}
	return legacyWaiverHash(struct {
		Version     int
		ProjectID   int
		ActorUserID int
		Failure     SettlementFailure
	}{1, row.ProjectID, row.ActorUserID, row})
}

func legacyWaiverOperationHash(userID int, refType, refID string) string {
	value, _ := legacyWaiverHash(struct {
		UserID         int
		RefType, RefID string
	}{userID, refType, refID})
	return value
}

func legacyWaiverRecord(row SettlementFailure, batch LedgerLegacySettlementWaiverBatch) (LedgerLegacySettlementWaiver, error) {
	source, err := LedgerLegacySettlementFailureHash(row)
	if err != nil {
		return LedgerLegacySettlementWaiver{}, err
	}
	record := LedgerLegacySettlementWaiver{
		FailureID: row.ID, OperationSHA256: legacyWaiverOperationHash(row.UserID, row.RefType, row.RefID),
		RequestID: row.RequestID, UserID: row.UserID, ProjectID: row.ProjectID, ActorUserID: row.ActorUserID,
		TokenID: row.TokenID, TokenUnlimited: row.TokenUnlimited, IsPlayground: row.IsPlayground,
		QuotaDelta: int64(row.QuotaDelta), PreConsumed: int64(row.PreConsumed), RefType: row.RefType, RefID: row.RefID, PricingVersion: row.PricingVersion,
		SourceSHA256: source, TokenKeySHA256: legacyWaiverStringHash(row.TokenKey), MetadataSHA256: legacyWaiverStringHash(row.Metadata),
		WaivedMicrousd: int64(row.QuotaDelta), Reason: batch.Reason, Actor: batch.Actor, EvidenceSHA256: batch.EvidenceSHA256, CreatedAt: batch.At.UTC().Truncate(time.Microsecond),
	}
	record.RecordSHA256, err = legacyWaiverHash(record)
	return record, err
}

func validateLegacyWaiverSource(tx *gorm.DB, row SettlementFailure) error {
	if row.ID == 0 || row.UserID <= 0 || row.TokenID <= 0 || row.RequestID == "" || row.RefID == "" || row.RefType != "relay_settlement" || row.QuotaDelta <= 0 || row.PreConsumed < 0 || int64(row.QuotaDelta) > math.MaxInt64-int64(row.PreConsumed) || row.Status != SettlementFailureStatusManualReview || row.ResolvedAt != nil || row.NextRetryAt != nil {
		return fmt.Errorf("settlement failure %d is outside the positive manual-review relay settlement scope", row.ID)
	}
	var count int64
	if err := tx.Model(&SettlementFailure{}).Where("id <> ? AND ((user_id = ? AND ref_type = ? AND ref_id = ?) OR request_id = ?)", row.ID, row.UserID, row.RefType, row.RefID, row.RequestID).Count(&count).Error; err != nil {
		return err
	}
	if count != 0 {
		return fmt.Errorf("settlement failure %d has duplicate financial identity", row.ID)
	}
	// Keep the same broad conflict semantics while using the existing composite
	// identity/EventID indexes and the explicit migration's RefID index. Avoid
	// repeatedly scanning all historical wallet rows for each reviewed failure.
	for _, query := range []*gorm.DB{
		tx.Model(&WalletLedger{}).Where("user_id = ? AND ref_type = ? AND ref_id = ?", row.UserID, row.RefType, row.RefID),
		tx.Model(&WalletLedger{}).Where("ref_id IN ? AND ref_type <> ?", []string{row.RefID, row.RequestID}, "relay_preconsume"),
		tx.Model(&WalletLedger{}).Where("event_id = ? AND ref_type <> ?", row.RequestID, "relay_preconsume"),
	} {
		if err := query.Count(&count).Error; err != nil {
			return err
		}
		if count != 0 {
			return fmt.Errorf("settlement failure %d already has a terminal wallet fact", row.ID)
		}
	}
	if err := tx.Model(&RedisLedgerOperation{}).Where("request_id IN ?", []string{row.RequestID, row.RefID}).Count(&count).Error; err != nil {
		return err
	}
	if count != 0 {
		return fmt.Errorf("settlement failure %d conflicts with a permanent ledger operation", row.ID)
	}
	if err := tx.Model(&SettlementOutbox{}).Where("(user_id = ? AND ref_type = ? AND ref_id = ?) OR request_id = ?", row.UserID, row.RefType, row.RefID, row.RequestID).Count(&count).Error; err != nil {
		return err
	}
	if count != 0 {
		return fmt.Errorf("settlement failure %d conflicts with a settlement outbox", row.ID)
	}
	return nil
}

// ApplyLegacySettlementWaivers is an offline operation. The caller must stop
// every writer and supply the precise reviewed IDs, full source hashes and cap.
// Schema registration belongs to startup/migrations, never this operation.
func ApplyLegacySettlementWaivers(ctx context.Context, db *gorm.DB, batch LedgerLegacySettlementWaiverBatch) (LedgerLegacySettlementWaiverReport, error) {
	var report LedgerLegacySettlementWaiverReport
	if db == nil || !batch.WritersStopped || len(batch.Items) == 0 || batch.At.IsZero() || strings.TrimSpace(batch.Reason) == "" || len(batch.Reason) > 512 || strings.TrimSpace(batch.Actor) == "" || len(batch.Actor) > 191 || !legacyWaiverValidHash(batch.EvidenceSHA256) || batch.ExpectedTotalMicrousd <= 0 || batch.MaxTotalMicrousd <= 0 || batch.ExpectedTotalMicrousd > batch.MaxTotalMicrousd {
		return report, errors.New("waiver requires stopped writers, exact items, positive bounded total, actor, reason, evidence SHA256 and time")
	}
	seen := map[uint]bool{}
	var total int64
	for _, item := range batch.Items {
		if item.FailureID == 0 || seen[item.FailureID] || !legacyWaiverValidHash(item.SourceSHA256) || item.AmountMicrousd <= 0 || total > math.MaxInt64-item.AmountMicrousd {
			return report, errors.New("invalid, duplicate or overflowing legacy waiver item")
		}
		seen[item.FailureID] = true
		total += item.AmountMicrousd
	}
	if total != batch.ExpectedTotalMicrousd || total > batch.MaxTotalMicrousd {
		return report, errors.New("legacy waiver exact total or cap does not match")
	}
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, item := range batch.Items {
			var source SettlementFailure
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&source, item.FailureID).Error; err != nil {
				return fmt.Errorf("load legacy waiver source %d: %w", item.FailureID, err)
			}
			if err := validateLegacyWaiverSource(tx, source); err != nil {
				return err
			}
			record, err := legacyWaiverRecord(source, batch)
			if err != nil {
				return err
			}
			if record.SourceSHA256 != item.SourceSHA256 || record.WaivedMicrousd != item.AmountMicrousd {
				return fmt.Errorf("legacy waiver source %d changed or its exact amount differs", item.FailureID)
			}
			var existing LedgerLegacySettlementWaiver
			if err := tx.Where("failure_id = ?", item.FailureID).Find(&existing).Error; err != nil {
				return err
			}
			if existing.FailureID != 0 {
				existing.CreatedAt = existing.CreatedAt.UTC()
				if existing != record {
					return fmt.Errorf("legacy waiver %d conflicts with its retained disposition", item.FailureID)
				}
				report.Existing++
			} else {
				if err := tx.Create(&record).Error; err != nil {
					return err
				}
				report.Created++
			}
		}
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return LedgerLegacySettlementWaiverReport{}, err
	}
	report.Failures, report.WaivedMicrousd = len(batch.Items), total
	return report, nil
}

// InventoryLegacySettlementWaiverItems produces only explicit IDs, exact source
// hashes and additional amounts. It never returns credentials or metadata.
func InventoryLegacySettlementWaiverItems(ctx context.Context, db *gorm.DB, ids []uint) ([]LedgerLegacySettlementWaiverItem, error) {
	if db == nil || len(ids) == 0 {
		return nil, errors.New("legacy waiver inventory requires a database and exact failure IDs")
	}
	items := make([]LedgerLegacySettlementWaiverItem, 0, len(ids))
	seen := map[uint]bool{}
	for _, id := range ids {
		if id == 0 || seen[id] {
			return nil, errors.New("legacy waiver inventory contains invalid or duplicate IDs")
		}
		seen[id] = true
		var row SettlementFailure
		if err := db.WithContext(ctx).First(&row, id).Error; err != nil {
			return nil, err
		}
		if err := validateLegacyWaiverSource(db.WithContext(ctx), row); err != nil {
			return nil, err
		}
		fingerprint, err := LedgerLegacySettlementFailureHash(row)
		if err != nil {
			return nil, err
		}
		items = append(items, LedgerLegacySettlementWaiverItem{FailureID: id, SourceSHA256: fingerprint, AmountMicrousd: int64(row.QuotaDelta)})
	}
	return items, nil
}

func verifyLegacySettlementWaiver(tx *gorm.DB, retained LedgerLegacySettlementWaiver) error {
	var source SettlementFailure
	if err := tx.First(&source, retained.FailureID).Error; err != nil {
		return fmt.Errorf("legacy waiver %d source is absent: %w", retained.FailureID, err)
	}
	if err := validateLegacyWaiverSource(tx, source); err != nil {
		return err
	}
	if retained.CreatedAt.IsZero() || strings.TrimSpace(retained.Reason) == "" || strings.TrimSpace(retained.Actor) == "" || !legacyWaiverValidHash(retained.EvidenceSHA256) {
		return fmt.Errorf("legacy waiver %d has invalid audit fields", retained.FailureID)
	}
	expected, err := legacyWaiverRecord(source, LedgerLegacySettlementWaiverBatch{Reason: retained.Reason, Actor: retained.Actor, EvidenceSHA256: retained.EvidenceSHA256, At: retained.CreatedAt})
	if err != nil {
		return err
	}
	retained.CreatedAt = retained.CreatedAt.UTC()
	if expected != retained {
		return fmt.Errorf("legacy waiver %d or its immutable source changed", retained.FailureID)
	}
	return nil
}

func VerifyLegacySettlementWaivers(ctx context.Context, db *gorm.DB) ([]LedgerLegacySettlementWaiver, error) {
	if db == nil {
		return nil, errors.New("core database is unavailable")
	}
	var rows []LedgerLegacySettlementWaiver
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Order("failure_id").Find(&rows).Error; err != nil {
			return err
		}
		for _, row := range rows {
			if err := verifyLegacySettlementWaiver(tx, row); err != nil {
				return err
			}
		}
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelSerializable, ReadOnly: true})
	return rows, err
}

func VerifiedUnresolvedSettlementFailureIDs(ctx context.Context, db *gorm.DB) ([]uint, error) {
	if db == nil {
		return nil, errors.New("core database is unavailable")
	}
	var ids []uint
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		query, err := verifiedUnresolvedSettlementFailureQuery(tx)
		if err != nil {
			return err
		}
		return query.Order("id").Pluck("id", &ids).Error
	}, &sql.TxOptions{Isolation: sql.LevelSerializable, ReadOnly: true})
	return ids, err
}

func verifiedUnresolvedSettlementFailureQuery(tx *gorm.DB) (*gorm.DB, error) {
	var waivers []LedgerLegacySettlementWaiver
	if err := tx.Order("failure_id").Find(&waivers).Error; err != nil {
		return nil, err
	}
	for _, row := range waivers {
		if err := verifyLegacySettlementWaiver(tx, row); err != nil {
			return nil, err
		}
	}
	query := tx.Model(&SettlementFailure{}).Where("status <> ?", SettlementFailureStatusResolved)
	if len(waivers) != 0 {
		waived := make([]uint, 0, len(waivers))
		for _, row := range waivers {
			waived = append(waived, row.FailureID)
		}
		query = query.Where("id NOT IN ?", waived)
	}
	return query, nil
}

func VerifiedUnresolvedSettlementFailureCount(ctx context.Context, db *gorm.DB, userID int) (int64, error) {
	if db == nil {
		return 0, errors.New("core database is unavailable")
	}
	var count int64
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		query, err := verifiedUnresolvedSettlementFailureQuery(tx)
		if err != nil {
			return err
		}
		return query.Where("user_id = ?", userID).Count(&count).Error
	}, &sql.TxOptions{Isolation: sql.LevelSerializable, ReadOnly: true})
	return count, err
}

// LegacySettlementWaiverBlocksOperation must run before claiming/replaying an
// old failure or outbox. Changed source or a new duplicate is an error, never a
// permission to debit. A different future request remains entirely unaffected.
func LegacySettlementWaiverBlocksOperation(ctx context.Context, db *gorm.DB, userID int, refType, refID, requestID string) (bool, error) {
	if db == nil {
		return false, errors.New("core database is unavailable")
	}
	var rows []LedgerLegacySettlementWaiver
	query := db.WithContext(ctx).Where("operation_sha256 = ?", legacyWaiverOperationHash(userID, refType, refID))
	if requestID != "" {
		query = query.Or("request_id = ?", requestID)
	}
	if err := query.Find(&rows).Error; err != nil {
		return false, err
	}
	for _, row := range rows {
		if err := verifyLegacySettlementWaiver(db.WithContext(ctx), row); err != nil {
			return false, err
		}
	}
	return len(rows) != 0, nil
}

// LegacySettlementFailureWaiverBlocksReplay first matches the permanent source
// ID. Changing BOTH request/ref identities must not evade an existing waiver.
// The operation check additionally catches a different/new duplicate failure.
func LegacySettlementFailureWaiverBlocksReplay(ctx context.Context, db *gorm.DB, source SettlementFailure) (bool, error) {
	if db == nil {
		return false, errors.New("core database is unavailable")
	}
	var record LedgerLegacySettlementWaiver
	if err := db.WithContext(ctx).Where("failure_id = ?", source.ID).Find(&record).Error; err != nil {
		return false, err
	}
	if record.FailureID != 0 {
		if err := verifyLegacySettlementWaiver(db.WithContext(ctx), record); err != nil {
			return false, err
		}
		return true, nil
	}
	return LegacySettlementWaiverBlocksOperation(ctx, db, source.UserID, source.RefType, source.RefID, source.RequestID)
}

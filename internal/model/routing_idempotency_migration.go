package model

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/lazu-ai/lazu/internal/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const routingDecisionBackfillAttempts = 3
const routingDecisionBackfillMaxRows = 1000

var errRoutingDecisionBackfillTooLarge = errors.New("routing decision history requires offline backfill")

// backfillRoutingDecisionIdempotencyKeys makes a bounded legacy history
// participate in replay protection. Oversized histories stay NULL for an
// offline migration so startup remains safe. Duplicate request IDs are retained
// for auditability: the oldest row owns the canonical primary key and later rows
// receive a deterministic legacy key.
func backfillRoutingDecisionIdempotencyKeys(db *gorm.DB) error {
	return backfillRoutingDecisionIdempotencyKeysWithLimit(db, routingDecisionBackfillMaxRows)
}

func backfillRoutingDecisionIdempotencyKeysWithLimit(db *gorm.DB, maxRows int) error {
	if db == nil || !db.Migrator().HasTable(&RoutingDecision{}) {
		return nil
	}
	if maxRows < 1 {
		return fmt.Errorf("routing decision backfill limit must be positive")
	}

	// A serializable transaction keeps the candidate scan and updates in one
	// snapshot. This matters during rolling upgrades: an old instance can still
	// insert a legacy NULL key while a new instance is starting. Databases that
	// detect that overlap abort the transaction; retrying makes the next scan
	// include the late legacy row instead of recording a partial migration.
	var lastErr error
	for attempt := 0; attempt < routingDecisionBackfillAttempts; attempt++ {
		tx := db.Begin(&sql.TxOptions{Isolation: sql.LevelSerializable})
		if tx.Error != nil {
			return tx.Error
		}
		if err := backfillRoutingDecisionIdempotencyKeysTx(tx, maxRows); err != nil {
			tx.Rollback()
			if errors.Is(err, errRoutingDecisionBackfillTooLarge) {
				common.SysWarn(fmt.Sprintf(
					"skipping startup routing decision idempotency backfill: more than %d legacy rows require an offline migration",
					maxRows,
				))
				return nil
			}
			lastErr = err
			if isRetryableRoutingBackfillError(err) {
				continue
			}
			return err
		}
		if err := tx.Commit().Error; err != nil {
			lastErr = err
			if isRetryableRoutingBackfillError(err) {
				continue
			}
			return err
		}
		return nil
	}
	return fmt.Errorf("backfill routing decision idempotency keys after %d attempts: %w", routingDecisionBackfillAttempts, lastErr)
}

func backfillRoutingDecisionIdempotencyKeysTx(tx *gorm.DB, maxRows int) error {
	// This migration runs before the HTTP server starts. Bound the locking read
	// inside every serializable retry so rows inserted during a rolling upgrade
	// cannot turn a previously safe scan into an unbounded allocation.
	var rows []RoutingDecision
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("is_primary = ? AND request_id <> ? AND idempotency_key IS NULL", true, "").
		Order("id ASC").
		Limit(maxRows + 1).
		Find(&rows).Error; err != nil {
		return err
	}
	if len(rows) > maxRows {
		return errRoutingDecisionBackfillTooLarge
	}
	if len(rows) == 0 {
		return nil
	}

	seenRequestIDs := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		key := "primary:" + row.RequestID
		if _, seen := seenRequestIDs[row.RequestID]; seen {
			key = "legacy-primary:" + strconv.FormatInt(row.ID, 10)
		} else {
			var existing RoutingDecision
			err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id").Where("idempotency_key = ?", key).First(&existing).Error
			switch {
			case err == nil:
				key = "legacy-primary:" + strconv.FormatInt(row.ID, 10)
			case !errors.Is(err, gorm.ErrRecordNotFound):
				return fmt.Errorf("find existing routing decision key: %w", err)
			}
			seenRequestIDs[row.RequestID] = struct{}{}
		}

		if err := tx.Model(&RoutingDecision{}).
			Where("id = ? AND idempotency_key IS NULL", row.ID).
			Update("idempotency_key", key).Error; err != nil {
			return fmt.Errorf("backfill routing decision %d: %w", row.ID, err)
		}
	}
	return nil
}

func isRetryableRoutingBackfillError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	for _, marker := range []string{
		"serialization",
		"could not serialize",
		"deadlock",
		"database is locked",
		"lock timeout",
		"sqlstate 40001",
		"sqlstate 40p01",
	} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

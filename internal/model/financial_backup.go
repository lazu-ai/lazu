package model

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/ledger"
	"gorm.io/gorm"
)

type GatewayBackupAudit struct {
	ObservedAt                time.Time    `json:"observed_at"`
	Ledger                    LedgerHealth `json:"ledger"`
	CreditSyncPending         int64        `json:"credit_sync_pending"`
	CounterRefreshPending     int64        `json:"counter_refresh_pending"`
	WalletsVerified           int          `json:"wallets_verified"`
	KeysVerified              int          `json:"keys_verified"`
	SpoolDirectories          []string     `json:"spool_directories"`
	WritersStoppedAttestation bool         `json:"writers_stopped_attestation"`
	Continuity                string       `json:"continuity"`
}

// AuditGatewayBackup never changes financial state. The explicit attestation
// covers every API, external-credit callback, and jobs writer; maintenance alone
// only blocks new monetary admission, so it cannot establish a stable snapshot.
func AuditGatewayBackup(ctx context.Context, db *gorm.DB, spoolDirectories []string, writersStopped bool) (GatewayBackupAudit, error) {
	report := GatewayBackupAudit{ObservedAt: time.Now().UTC(), SpoolDirectories: spoolDirectories, WritersStoppedAttestation: writersStopped}
	if !writersStopped {
		return report, errors.New("stop every API, callback, and jobs writer; provide -writers-stopped only after this is verified")
	}
	if db == nil || common.RDB == nil {
		return report, errors.New("core database and Redis are required for a financial backup")
	}
	state, err := loadLedgerContinuity(ctx, db)
	if err != nil {
		return report, err
	}
	if state.Status != "active" {
		return report, errors.New("backup requires active, fully recovered financial continuity")
	}
	if err := verifyContinuityOpening(ctx, db, state, true); err != nil {
		return report, err
	}
	if db.Migrator().HasTable(&SettlementFailure{}) {
		continuity, err := ReportLedgerContinuity(ctx, db, spoolDirectories)
		if err != nil {
			return report, err
		}
		if len(continuity.UnresolvedSettlementFailureIDs) > 0 || len(continuity.UnresolvedSettlementOutboxIDs) > 0 {
			return report, errors.New("unresolved legacy settlement failures or outboxes prevent a financial backup")
		}
	}
	report.Continuity = state.HotValue()
	engine := ledger.New(common.RDB, nil)
	engine.Continuity = report.Continuity
	if err := engine.CheckContinuity(ctx); err != nil {
		return report, err
	}
	nodes, err := ledgerScan(ctx, ledger.Prefix+"nodes:*")
	if err != nil {
		return report, err
	}
	if len(nodes) > 0 {
		return report, fmt.Errorf("%d financial writer leases remain live; stop writers and wait for lease expiry", len(nodes))
	}
	if len(spoolDirectories) == 0 {
		return report, errors.New("provide the spool directory of every API and jobs instance")
	}
	for _, directory := range spoolDirectories {
		entries, err := os.ReadDir(directory)
		if err != nil {
			return report, fmt.Errorf("spool %s: %w", directory, err)
		}
		if len(entries) != 0 {
			return report, fmt.Errorf("spool %s has %d entries; replay or classify all entries before backup", directory, len(entries))
		}
	}
	maintenance, err := common.RDB.Exists(ctx, ledger.Prefix+"maintenance").Result()
	if err != nil {
		return report, err
	}
	if maintenance != 1 {
		return report, errors.New("pause ledger admission before the backup quiet window")
	}
	report.Ledger, err = ReadLedgerHealth(ctx)
	if err != nil {
		return report, err
	}
	if report.Ledger.Uncommitted != 0 || report.Ledger.Reservations != 0 || report.Ledger.Exposure != 0 || report.Ledger.Dead != 0 {
		return report, fmt.Errorf("financial ledger is not drained: %+v", report.Ledger)
	}
	if err := verifyFinancialDeadStreamEmpty(ctx); err != nil {
		return report, err
	}
	if err := VerifyRelayEventsDrained(ctx); err != nil {
		return report, err
	}
	pending, err := common.RDB.XPending(ctx, ledger.Stream, ledger.Group).Result()
	if err != nil && !errors.Is(err, redis.Nil) && !strings.Contains(err.Error(), "NOGROUP") {
		return report, err
	}
	if pending != nil && pending.Count != 0 {
		return report, fmt.Errorf("%d financial stream deliveries remain pending", pending.Count)
	}
	streamLength, err := common.RDB.XLen(ctx, ledger.Stream).Result()
	if err != nil {
		return report, err
	}
	if streamLength > 0 {
		groups, err := common.RDB.Do(ctx, "XINFO", "GROUPS", ledger.Stream).Slice()
		if err != nil {
			return report, err
		}
		found := false
		for _, rawGroup := range groups {
			fields, ok := rawGroup.([]interface{})
			if !ok || len(fields)%2 != 0 {
				return report, errors.New("invalid financial stream group metadata")
			}
			name, delivered := "", ""
			for i := 0; i < len(fields); i += 2 {
				switch fields[i] {
				case "name":
					name, _ = fields[i+1].(string)
				case "last-delivered-id":
					delivered, _ = fields[i+1].(string)
				}
			}
			if name != ledger.Group {
				continue
			}
			if delivered == "" {
				return report, errors.New("financial stream group has no delivery checkpoint")
			}
			found = true
			unread, err := common.RDB.XRangeN(ctx, ledger.Stream, "("+delivered, "+", 1).Result()
			if err != nil {
				return report, err
			}
			if len(unread) > 0 {
				return report, errors.New("financial stream has unread events; finish the writer drain before backup")
			}
		}
		if !found {
			return report, errors.New("financial stream has events but no writer consumer group")
		}
	}
	if err := db.WithContext(ctx).Model(&LedgerCreditSync{}).Where("applied_at IS NULL").Count(&report.CreditSyncPending).Error; err != nil {
		return report, err
	}
	if err := db.WithContext(ctx).Model(&LedgerCounterRefresh{}).Where("applied_at IS NULL").Count(&report.CounterRefreshPending).Error; err != nil {
		return report, err
	}
	if report.CreditSyncPending != 0 || report.CounterRefreshPending != 0 {
		return report, errors.New("pending credits or administrative counter refreshes must be applied before backup")
	}
	for _, kind := range []string{"wallet", "token"} {
		seen := map[string]bool{}
		var cursor uint64
		for {
			keys, next, err := common.RDB.Scan(ctx, cursor, ledger.Prefix+kind+":*", 500).Result()
			if err != nil {
				return report, err
			}
			for _, key := range keys {
				if seen[key] {
					continue
				}
				seen[key] = true
				id, err := strconv.Atoi(strings.TrimPrefix(key, ledger.Prefix+kind+":"))
				if err != nil {
					return report, fmt.Errorf("invalid financial key identity: %s", key)
				}
				if kind == "wallet" {
					var user User
					if err := db.WithContext(ctx).Unscoped().Select("id", "quota", "credit_seq").First(&user, id).Error; err != nil {
						return report, err
					}
					hot, err := common.RDB.HGetAll(ctx, key).Result()
					if err != nil {
						return report, err
					}
					if hot["avail"] != strconv.FormatInt(int64(user.Quota), 10) || hot["credit_seq"] != strconv.FormatInt(user.CreditSeq, 10) {
						return report, fmt.Errorf("wallet %d differs between Redis and committed core; reconcile before backup", id)
					}
					report.WalletsVerified++
				} else {
					var token Token
					if err := db.WithContext(ctx).Unscoped().Select("id", "remain_quota").First(&token, id).Error; err != nil {
						return report, err
					}
					hot, err := common.RDB.Get(ctx, key).Result()
					if err != nil {
						return report, err
					}
					if hot != strconv.FormatInt(int64(token.RemainQuota), 10) {
						return report, fmt.Errorf("key %d differs between Redis and committed core; reconcile before backup", id)
					}
					report.KeysVerified++
				}
			}
			cursor = next
			if cursor == 0 {
				break
			}
		}
	}
	return report, nil
}

func verifyFinancialDeadStreamEmpty(ctx context.Context) error {
	length, err := common.RDB.XLen(ctx, ledger.DeadStream).Result()
	if err != nil {
		return err
	}
	if length != 0 {
		return fmt.Errorf("%d financial dead-letter payloads remain; replay or classify them before backup or resume", length)
	}
	return nil
}

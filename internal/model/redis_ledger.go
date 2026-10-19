package model

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/ledger"
	"github.com/lazu-ai/lazu/pkg/billing"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// These are permanent financial records. Retention of telemetry never removes
// the operation identity or a pending credit.
type RedisLedgerOperation struct {
	ProjectID int       `gorm:"not null;default:0;index"`
	ActorID   int       `gorm:"not null;default:0"`
	ID        string    `gorm:"size:191;primaryKey"`
	RequestID string    `gorm:"size:191;not null;index"`
	Kind      string    `gorm:"size:16;not null"`
	PayerID   int       `gorm:"not null;index"`
	TokenID   int       `gorm:"not null"`
	Amount    int64     `gorm:"type:bigint;not null"`
	BatchID   string    `gorm:"size:64;not null;index"`
	Input     string    `gorm:"type:text;not null"`
	CreatedAt time.Time `gorm:"not null;index"`
}
type LedgerCreditSync struct {
	ID        uint       `gorm:"primaryKey"`
	UserID    int        `gorm:"not null;uniqueIndex:idx_credit_sequence,priority:1"`
	Sequence  int64      `gorm:"not null;uniqueIndex:idx_credit_sequence,priority:2"`
	Amount    int64      `gorm:"type:bigint;not null"`
	AppliedAt *time.Time `gorm:"index"`
	CreatedAt time.Time
}
type LedgerCounterRefresh struct {
	ID        uint   `gorm:"primaryKey"`
	TableName string `gorm:"size:64;not null"`
	ObjectID  int    `gorm:"not null"`
	ProjectID int
	UserID    int
	TokenID   int
	AppliedAt *time.Time `gorm:"index"`
	CreatedAt time.Time
}

func RedisLedgerModels() []interface{} {
	return []interface{}{&RedisLedgerOperation{}, &LedgerCreditSync{}, &LedgerCounterRefresh{}, &LedgerContinuityState{}, &LedgerLegacyPrechargeRecord{}, &LedgerLegacySettlementWaiver{}, &LedgerOpeningManifest{}, &LedgerOpeningChunk{}}
}

var gatewayLedger atomic.Pointer[ledger.Engine]

func GatewayLedger() *ledger.Engine { return gatewayLedger.Load() }
func GatewayLedgerEnabled() bool    { return GatewayLedger() != nil }

// The inventory cache keeps filesystem checks off ordinary admission. Durable
// settlement and replay do not consult this new-request admission gate.
func bindLedgerSpoolAdmission(engine *ledger.Engine) (check func(), quarantined func(ledger.QuarantinedOperation)) {
	var exposure atomic.Int64
	var unavailable atomic.Bool
	var unknown atomic.Bool
	var quarantineGeneration atomic.Uint64
	var inventoryUpdate sync.Mutex
	engine.HardLimitValue = func() int64 {
		return max(int64(0), ledgerOptionAmount(LedgerBacklogHardLimitOption, 100_000_000)-exposure.Load())
	}
	engine.AdmissionCheck = func() error {
		if unavailable.Load() || unknown.Load() {
			return ledger.ErrUnavailable
		}
		return nil
	}
	check = func() {
		generation := quarantineGeneration.Load()
		usage, err := engine.SpoolUsage()
		exposure.Store(usage.UnresolvedExposure)
		if err == nil {
			err = engine.CheckSpoolWritable()
		}
		unavailable.Store(err != nil || usage.Files >= 100_000 || usage.Bytes >= 1<<30 || usage.FreeBytes < 256<<20)
		// An inventory started before a new quarantine must not clear its
		// immediate fence. The next complete inventory can resolve that fence.
		inventoryUpdate.Lock()
		unknown.Store(usage.UnknownExposureFiles > 0 || quarantineGeneration.Load() != generation)
		inventoryUpdate.Unlock()
		if unavailable.Load() || unknown.Load() {
			common.SysError(fmt.Sprintf("ledger spool unavailable: files=%d bytes=%d free_bytes=%d unknown_exposure_files=%d err=%v", usage.Files, usage.Bytes, usage.FreeBytes, usage.UnknownExposureFiles, err))
		}
	}
	quarantined = func(operation ledger.QuarantinedOperation) {
		if !operation.AmountKnown {
			inventoryUpdate.Lock()
			quarantineGeneration.Add(1)
			unknown.Store(true)
			inventoryUpdate.Unlock()
		}
	}
	return check, quarantined
}

// Startup calls this after schema validation and Redis initialization. SQLite
// intentionally retains synchronous billing for embedded development.
func InitGatewayLedger() error {
	if CoreDB() != nil && CoreDB().Dialector.Name() == "postgres" {
		if err := validateGatewayStoreSeparation(context.Background(), CoreDB(), TelemetryDB()); err != nil {
			return err
		}
		if _, ready := currentPricingSnapshot(); !ready {
			return errors.New("Postgres gateway requires a loaded pricing registry before startup")
		}
		if err := InstallStoreOwnershipGuards(); err != nil {
			return err
		}
	}
	if CoreDB() == nil || CoreDB().Dialector.Name() == "sqlite" {
		return nil
	}
	if !common.RedisEnabled || common.RDB == nil {
		return errors.New("Postgres gateway requires Redis financial admission; configure REDIS_CONN_STRING before startup")
	}
	if err := validateLedgerCutover(context.Background(), CoreDB()); err != nil {
		return err
	}
	engine := ledger.New(common.RDB, redisLedgerStorage{})
	engine.SpoolDir = os.Getenv("LEDGER_SPOOL_DIR")
	if engine.SpoolDir == "" {
		engine.SpoolDir = filepath.Join(common.GetDataDir(), "ledger-spool")
	}
	if err := os.MkdirAll(engine.SpoolDir, 0700); err != nil {
		return err
	}
	if err := bootstrapEmptyGatewayLedger(context.Background(), CoreDB(), engine); err != nil {
		return err
	}
	if err := BindLedgerContinuity(context.Background(), CoreDB(), engine); err != nil {
		return err
	}
	if err := StartLedgerNodeLease(engine); err != nil {
		return err
	}
	engine.TelemetryStream = common.RelayEventStream
	engine.TelemetryMaxLength = relayEventMaxLength()
	engine.TelemetryEncode = EncodeFinancialTelemetry
	engine.Maintenance = ProcessLedgerCounterRefresh
	engine.Observer = MonitorGatewayLedger

	checkSpool, quarantinedSpool := bindLedgerSpoolAdmission(engine)
	var durabilityFailed atomic.Bool
	var failureRecorded atomic.Bool
	engine.SpoolFailure = func(err error) {
		durabilityFailed.Store(true)
		common.SysError("ledger spool persistence failure: " + err.Error())
		// This is an exceptional durability failure, not financial fallback.
		// Attempt the incident marker before returning, rather than leaving a
		// normal disk-only failure exposed until the next background tick.
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if markErr := MarkLedgerDurabilityFailure(ctx, engine.Continuity); markErr == nil {
			failureRecorded.Store(true)
		} else {
			common.SysError("ledger durability incident marker: " + markErr.Error())
		}
		_ = engine.Redis.Eval(ctx, `if redis.call('GET',KEYS[1])==ARGV[1] then redis.call('SET',KEYS[2],'durability-failure');return 1 end return 0`, []string{ledger.ContinuityKey, ledger.Prefix + "maintenance"}, engine.Continuity).Err()
	}
	spoolAdmission := engine.AdmissionCheck
	engine.AdmissionCheck = func() error {
		if durabilityFailed.Load() {
			return ledger.ErrUnavailable
		}
		return spoolAdmission()
	}
	checkSpool()
	engine.SpoolQuarantine = func(operation ledger.QuarantinedOperation) {
		quarantinedSpool(operation)
		common.IncrementOperationalCounter("ledger.spool.quarantined")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		notifyLedgerAlert(ctx, "spool-quarantine", fmt.Sprintf("Unresolved ledger evidence quarantined: file=%s operation_id=%s amount_microusd=%d amount_known=%t reason=%s; inspect and retain the payload/manifest before financial recovery", operation.File, operation.OperationID, operation.Amount, operation.AmountKnown, operation.Reason))
	}
	gatewayLedger.Store(engine)
	SetIdentityCounterRefreshOutbox(func(tx *gorm.DB, m IdentityCounterMutation) error {
		return tx.Create(&LedgerCounterRefresh{TableName: m.Table, ObjectID: m.ObjectID, ProjectID: m.ProjectID, UserID: m.UserID, TokenID: m.TokenID}).Error
	})
	common.SafeCtxGo(context.Background(), "ledger-spool-replay", func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for range ticker.C {
			checkSpool()
			if durabilityFailed.Load() {
				// A writable disk is not proof that an accepted, failed fsync
				// intent has been recovered. Persist an off-path incident marker
				// and keep all admission paused until explicit classification.
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				if !failureRecorded.Load() {
					if err := MarkLedgerDurabilityFailure(ctx, engine.Continuity); err == nil {
						failureRecorded.Store(true)
					} else {
						common.SysError("ledger durability incident marker: " + err.Error())
					}
				}
				_ = engine.Redis.Eval(ctx, `if redis.call('GET',KEYS[1])==ARGV[1] then redis.call('SET',KEYS[2],'durability-failure');return 1 end return 0`, []string{ledger.ContinuityKey, ledger.Prefix + "maintenance"}, engine.Continuity).Err()
				cancel()
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			err := engine.ReplaySpool(ctx)
			cancel()
			if err != nil {
				common.SysLog("ledger spool replay: " + err.Error())
			}
		}
	})
	return nil
}

type gatewayDatabaseIdentity struct {
	Database           string
	ClusterID          string
	CanIdentifyCluster bool
}

func readGatewayDatabaseIdentity(ctx context.Context, db *gorm.DB) (gatewayDatabaseIdentity, error) {
	var value gatewayDatabaseIdentity
	if err := db.WithContext(ctx).Raw("SELECT current_database() AS database, has_function_privilege('pg_catalog.pg_control_system()','EXECUTE') AS can_identify_cluster").Scan(&value).Error; err != nil {
		return value, err
	}
	// Permission is checked before executing the control function. Restricted
	// roles can still prove separation using distinct actual database names.
	if value.CanIdentifyCluster {
		if err := db.WithContext(ctx).Raw("SELECT system_identifier::text FROM pg_catalog.pg_control_system()").Scan(&value.ClusterID).Error; err != nil {
			return value, err
		}
	}
	return value, nil
}

func compareGatewayDatabaseIdentities(core, telemetry gatewayDatabaseIdentity) error {
	if core.Database != "" && telemetry.Database != "" && core.Database != telemetry.Database {
		return nil
	}
	if core.Database == "" || telemetry.Database == "" || core.ClusterID == "" || telemetry.ClusterID == "" {
		return errors.New("cannot prove Core and Telemetry database separation: matching database names require pg_control_system execution privilege on both stores")
	}
	if core.ClusterID == telemetry.ClusterID {
		return errors.New("Core and Telemetry connect to the same actual PostgreSQL database; configure separate databases before startup")
	}
	return nil
}

// ValidateGatewayStoreSeparation is the read-only PostgreSQL startup boundary
// also used by explicit deployment preflight. It never binds stores or installs
// runtime workers, and must not be called from a request path.
func ValidateGatewayStoreSeparation(ctx context.Context, core, telemetry *gorm.DB) error {
	return validateGatewayStoreSeparation(ctx, core, telemetry)
}

func validateGatewayStoreSeparation(ctx context.Context, core, telemetry *gorm.DB) error {
	if core == nil || telemetry == nil || telemetry == core {
		return errors.New("Postgres gateway requires a separate telemetry store; configure TELEMETRY_DATABASE_URL or LOG_SQL_DSN before startup")
	}
	if core.Dialector.Name() != "postgres" || telemetry.Dialector.Name() != "postgres" {
		return errors.New("Postgres gateway requires two PostgreSQL stores")
	}
	left, err := readGatewayDatabaseIdentity(ctx, core)
	if err != nil {
		return fmt.Errorf("identify Core database: %w", err)
	}
	right, err := readGatewayDatabaseIdentity(ctx, telemetry)
	if err != nil {
		return fmt.Errorf("identify Telemetry database: %w", err)
	}
	return compareGatewayDatabaseIdentities(left, right)
}
func ledgerOperationID(id, kind string) string { return kind + ":" + id }
func LedgerBudgets(definitions []SpendBudget, at time.Time) []ledger.Budget {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	result := make([]ledger.Budget, 0, len(definitions))
	for _, b := range definitions {
		start, end := spendBudgetPeriodWindow(b.Period, at)
		result = append(result, ledger.Budget{ID: b.ID, Start: start, End: end, Limit: int64(b.LimitMicrousd), Block: b.Enforcement == SpendBudgetEnforcementBlock, ProjectID: b.ProjectID, Scope: b.Scope, UserID: b.UserID, TokenID: b.TokenID, Period: b.Period})
	}
	return result
}

type redisLedgerStorage struct{}

func (redisLedgerStorage) Settled(ctx context.Context, id, kind string) (bool, error) {
	var n int64
	err := CoreDB().WithContext(ctx).Model(&RedisLedgerOperation{}).Where("id = ?", ledgerOperationID(id, kind)).Count(&n).Error
	return n > 0, err
}
func (redisLedgerStorage) Snapshot(ctx context.Context, input ledger.Input) (ledger.Snapshot, error) {
	result := ledger.Snapshot{TokenRemaining: map[int]int64{}, Budgets: map[string]int64{}, BudgetReserved: map[string]int64{}, ReservationBudgets: map[string][]string{}}
	// Capture pending identities BEFORE opening the consistent SQL snapshot.
	// ACK removes identities only after commit; retained journal entries remain
	// for ten minutes, so a concurrent ACK between these reads is harmless.
	// Already acknowledged history is dedupe data, not a cold-balance input.
	pendingIDs, err := common.RDB.ZRange(ctx, ledger.Prefix+"uncommitted", 0, -1).Result()
	if err != nil {
		return result, err
	}
	events := make([]ledger.Event, 0, len(pendingIDs))
	relevant := func(event ledger.Input) bool {
		if input.PayerID > 0 && event.PayerID == input.PayerID || input.TokenID > 0 && event.TokenID == input.TokenID {
			return true
		}
		for _, budget := range input.Budgets {
			if budget.Matches(event) {
				return true
			}
		}
		return false
	}
	const snapshotChunk = 5000
	for start := 0; start < len(pendingIDs); start += snapshotChunk {
		end := min(start+snapshotChunk, len(pendingIDs))
		rows, err := common.RDB.HMGet(ctx, ledger.Prefix+"journal", pendingIDs[start:end]...).Result()
		if err != nil {
			return result, err
		}
		for _, row := range rows {
			raw, ok := row.(string)
			if !ok {
				return result, errors.New("pending financial journal entry is missing")
			}
			var event ledger.Event
			if err := json.Unmarshal([]byte(raw), &event); err != nil {
				return result, err
			}
			if err := validateRecoveryEvent(event); err != nil {
				return result, err
			}
			if relevant(event.Input) {
				events = append(events, event)
			}
		}
	}
	reservations, err := common.RDB.HGetAll(ctx, ledger.Prefix+"reservations").Result()
	if err != nil {
		return result, err
	}
	err = CoreDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if input.PayerID > 0 {
			var user User
			if err := tx.Unscoped().Select("id", "quota", "credit_seq").First(&user, input.PayerID).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return fmt.Errorf("%w: payer %d was deleted", ledger.ErrPermanentOperation, input.PayerID)
				}
				return err
			}
			result.Balance = int64(user.Quota)
			result.CreditSeq = user.CreditSeq
		}
		if input.TokenID > 0 && !input.Unlimited {
			var key Token
			if err := tx.Unscoped().Select("id", "remain_quota").First(&key, input.TokenID).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return fmt.Errorf("%w: key %d was deleted", ledger.ErrPermanentOperation, input.TokenID)
				}
				return err
			}
			result.TokenRemaining[input.TokenID] = int64(key.RemainQuota)
		}
		for _, b := range input.Budgets {
			definition := SpendBudget{ID: b.ID, ProjectID: b.ProjectID, Scope: b.Scope, UserID: b.UserID, TokenID: b.TokenID, Period: b.Period}
			amount, err := sumSpendMicrousdForBudgetWithDB(tx, definition, b.Start, b.End)
			if err != nil {
				return err
			}
			result.Budgets[b.Key()] = int64(amount)
		}
		ids := make([]string, 0, len(events))
		for _, e := range events {
			ids = append(ids, ledgerOperationID(e.OperationID, e.Kind))
		}
		done := make(map[string]bool, len(ids))
		for start := 0; start < len(ids); start += snapshotChunk {
			var committed []string
			if err := tx.Model(&RedisLedgerOperation{}).Where("id IN ?", ids[start:min(start+snapshotChunk, len(ids))]).Pluck("id", &committed).Error; err != nil {
				return err
			}
			for _, id := range committed {
				done[id] = true
			}
		}
		for _, e := range events {
			if done[ledgerOperationID(e.OperationID, e.Kind)] {
				continue
			}
			if e.PayerID == input.PayerID {
				result.Balance, err = checkedLedgerSubtract(result.Balance, e.Amount)
				if err != nil {
					return err
				}
			}
			if e.TokenID == input.TokenID && !e.Unlimited {
				result.TokenRemaining[input.TokenID], err = checkedLedgerSubtract(result.TokenRemaining[input.TokenID], e.Amount)
				if err != nil {
					return err
				}
			}
			for _, b := range input.Budgets {
				if b.Matches(e.Input) {
					result.Budgets[b.Key()], err = checkedLedgerAdd(result.Budgets[b.Key()], e.Amount)
					if err != nil {
						return err
					}
				}
			}
		}
		for _, raw := range reservations {
			var r ledger.Input
			if err := json.Unmarshal([]byte(raw), &r); err != nil {
				return err
			}
			if r.PayerID <= 0 || r.RequestID == "" || r.Amount < 0 || r.Amount > 9007199254740991 {
				return errors.New("invalid financial reservation in cold snapshot")
			}
			if r.PayerID == input.PayerID {
				result.Balance, err = checkedLedgerSubtract(result.Balance, r.Amount)
				if err != nil {
					return err
				}
			}
			if r.TokenID == input.TokenID && !r.Unlimited {
				result.TokenRemaining[input.TokenID], err = checkedLedgerSubtract(result.TokenRemaining[input.TokenID], r.Amount)
				if err != nil {
					return err
				}
			}
			for _, b := range input.Budgets {
				if b.Matches(r) {
					result.BudgetReserved[b.Key()], err = checkedLedgerAdd(result.BudgetReserved[b.Key()], r.Amount)
					if err != nil {
						return err
					}
					result.ReservationBudgets[b.Key()] = append(result.ReservationBudgets[b.Key()], r.RequestID)
				}
			}
		}
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	return result, err
}
func (redisLedgerStorage) Apply(ctx context.Context, events []ledger.Event) error {
	if len(events) == 0 {
		return nil
	}
	batch := common.GenerateRequestID()
	ops := make([]RedisLedgerOperation, 0, len(events))
	byID := make(map[string]ledger.Event, len(events))
	for _, e := range events {
		if err := validateRecoveryEvent(e); err != nil {
			return fmt.Errorf("%w: %v", ledger.ErrPermanentOperation, err)
		}
		if e.Kind != "settle" && e.Kind != "refund" {
			return fmt.Errorf("%w: invalid financial event", ledger.ErrPermanentOperation)
		}
		e, raw, err := canonicalLedgerFinancialEvent(e)
		if err != nil {
			return err
		}
		id := ledgerOperationID(e.OperationID, e.Kind)
		if prior, exists := byID[id]; exists {
			_, priorRaw, err := canonicalLedgerFinancialEvent(prior)
			if err != nil {
				return err
			}
			if priorRaw != raw {
				return fmt.Errorf("%w: financial operation %s conflicts within its delivery batch", ledger.ErrPermanentOperation, id)
			}
			continue
		}
		byID[id] = e
		ops = append(ops, RedisLedgerOperation{ID: id, RequestID: e.RequestID, Kind: e.Kind, PayerID: e.PayerID, ProjectID: e.ProjectID, ActorID: e.ActorID, TokenID: e.TokenID, Amount: e.Amount, BatchID: batch, Input: raw, CreatedAt: e.OccurredAt})
	}
	var notifications []ledger.Event
	err := CoreDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		tx = SkipIdentityCounterRefresh(tx)
		inserted, err := insertVerifiedLedgerBatch(tx, ops)
		if err != nil {
			return err
		}
		if len(inserted) == 0 {
			return nil
		}
		users := map[int]int64{}
		keys := map[int]int64{}
		keyRemaining := map[int]int64{}
		keyLastUsed := map[int]int64{}
		rows := make([]WalletLedger, 0, len(inserted))
		spends := map[string]ProjectSpend{}
		insertedIDs := make(map[string]bool, len(inserted))
		for _, op := range inserted {
			insertedIDs[op.ID] = true
			e := byID[op.ID]
			var err error
			users[e.PayerID], err = checkedLedgerAdd(users[e.PayerID], e.Amount)
			if err != nil {
				return err
			}
			if e.TokenID > 0 {
				keys[e.TokenID], err = checkedLedgerAdd(keys[e.TokenID], e.Amount)
				if err != nil {
					return err
				}
				if !e.Unlimited {
					keyRemaining[e.TokenID], err = checkedLedgerAdd(keyRemaining[e.TokenID], e.Amount)
					if err != nil {
						return err
					}
				}
				if at := e.OccurredAt.Unix(); at > keyLastUsed[e.TokenID] {
					keyLastUsed[e.TokenID] = at
				}
			}
			kind := WalletLedgerKindSpend
			if e.Kind == "refund" {
				kind = WalletLedgerKindRefund
			}
			rows = append(rows, WalletLedger{UserID: e.PayerID, DeltaMicrousd: billing.MicroUSD(-e.Amount), Kind: kind, RefType: "relay_redis_" + e.Kind, RefID: e.OperationID, EventID: op.ID, PricingVersion: e.PricingVersion, Metadata: common.MapToJsonStr(map[string]interface{}{"actor_user_id": e.ActorID, "project_id": e.ProjectID, "token_id": e.TokenID, "model": e.Model}), CreatedAt: e.OccurredAt})
			if e.ProjectID > 0 {
				day := time.Date(e.OccurredAt.UTC().Year(), e.OccurredAt.UTC().Month(), e.OccurredAt.UTC().Day(), 0, 0, 0, 0, time.UTC)
				key := projectSpendDailyKey(e.ProjectID, e.ActorID, e.TokenID, day)
				row := spends[key]
				row.EventKey = key
				row.ProjectID = e.ProjectID
				row.ActorUserID = e.ActorID
				row.PayerUserID = e.PayerID
				row.TokenID = e.TokenID
				row.OccurredAt = day
				row.Aggregated = true
				amount, err := checkedLedgerAdd(int64(row.AmountMicrousd), e.Amount)
				if err != nil {
					return err
				}
				row.AmountMicrousd = billing.MicroUSD(amount)
				spends[key] = row
			}
		}
		if tx.Dialector.Name() == "postgres" {
			if err := applyPostgresLedgerUsers(tx, users); err != nil {
				return err
			}
			if err := applyPostgresLedgerTokens(tx, keys, keyRemaining, keyLastUsed); err != nil {
				return err
			}
		} else {
			for _, id := range sortedLedgerIDs(users) {
				amount := users[id]
				r := tx.Unscoped().Model(&User{}).Where("id = ?", id).Updates(map[string]interface{}{"quota": gorm.Expr("quota - ?", amount), "used_quota": gorm.Expr("used_quota + ?", amount)})
				if r.Error != nil {
					return r.Error
				}
				if r.RowsAffected != 1 {
					return fmt.Errorf("%w: payer %d missing", ledger.ErrPermanentOperation, id)
				}
			}
			for _, id := range sortedLedgerIDs(keys) {
				amount := keys[id]
				r := tx.Unscoped().Model(&Token{}).Where("id = ?", id).Updates(map[string]interface{}{"remain_quota": gorm.Expr("remain_quota - ?", keyRemaining[id]), "used_quota": gorm.Expr("used_quota + ?", amount), "last_used_at": gorm.Expr("CASE WHEN last_used_at > ? THEN last_used_at ELSE ? END", keyLastUsed[id], keyLastUsed[id])})
				if r.Error != nil {
					return r.Error
				}
				if r.RowsAffected != 1 {
					return fmt.Errorf("%w: key %d missing", ledger.ErrPermanentOperation, id)
				}
			}
		}
		if len(rows) > 0 {
			if err := tx.CreateInBatches(&rows, 500).Error; err != nil {
				return err
			}
		}
		if len(spends) > 0 {
			daily := make([]ProjectSpend, 0, len(spends))
			for _, row := range spends {
				daily = append(daily, row)
			}
			sort.Slice(daily, func(i, j int) bool { return daily[i].EventKey < daily[j].EventKey })
			if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "event_key"}}, DoUpdates: clause.Assignments(map[string]interface{}{"amount_microusd": gorm.Expr("project_spends.amount_microusd + excluded.amount_microusd")})}).CreateInBatches(&daily, 500).Error; err != nil {
				return err
			}
		}
		ordered := make([]ledger.Event, 0, len(inserted))
		for _, op := range ops {
			if insertedIDs[op.ID] {
				event := byID[op.ID]
				ordered = append(ordered, event)
				if event.Kind == "settle" && event.Amount > 0 {
					notifications = append(notifications, event)
				}
			}
		}
		return recordLedgerBatchBudgetAlerts(tx, ordered)
	})
	if err != nil {
		return err
	}
	// Only the background financial writer dispatches these checks, after the
	// entire batch commits. Replayed operations and rolled-back charges never
	// reach this callback. Delivery retains the previous best-effort behavior.
	notifyCommittedLedgerSettlements(notifications)
	return nil
}

// Alerts are calculated once per affected current budget/period after the
// financial batch commits its facts. Permanent operation inputs retain the
// original budget proof; modern writes do not duplicate it in charge rows.
func recordLedgerBatchBudgetAlerts(tx *gorm.DB, events []ledger.Event) error {
	projects := map[int]bool{0: true}
	for _, event := range events {
		projects[event.ProjectID] = true
	}
	ids := make([]int, 0, len(projects))
	for id := range projects {
		ids = append(ids, id)
	}
	var budgets []SpendBudget
	if err := tx.Where("enabled = ? AND project_id IN ?", true, ids).Order("id").Find(&budgets).Error; err != nil {
		return err
	}
	type periodDelta struct {
		start, end int64
		net, peak  billing.MicroUSD
		input      SpendBudgetChargeInput
	}
	for _, budget := range budgets {
		periods := map[int64]*periodDelta{}
		for _, event := range events {
			start, end := spendBudgetPeriodWindow(budget.Period, event.OccurredAt)
			definition := ledger.Budget{ProjectID: budget.ProjectID, UserID: budget.UserID, TokenID: budget.TokenID, Start: start, End: end}
			if !definition.Matches(event.Input) {
				continue
			}
			delta := periods[start]
			if delta == nil {
				delta = &periodDelta{start: start, end: end}
				periods[start] = delta
			}
			net, err := checkedLedgerAdd(int64(delta.net), event.Amount)
			if err != nil {
				return err
			}
			delta.net = billing.MicroUSD(net)
			if delta.net > delta.peak {
				delta.peak = delta.net
				delta.input = SpendBudgetChargeInput{ProjectID: event.ProjectID, PayerUserID: event.PayerID, ActorUserID: event.ActorID, UserID: event.ActorID, TokenID: event.TokenID, SessionID: event.SessionID, AgentID: event.AgentID, RequestID: event.RequestID, DeltaMicrousd: billing.MicroUSD(event.Amount), OccurredAt: event.OccurredAt}
			}
		}
		starts := make([]int64, 0, len(periods))
		for start := range periods {
			starts = append(starts, start)
		}
		sort.Slice(starts, func(i, j int) bool { return starts[i] < starts[j] })
		for _, start := range starts {
			delta := periods[start]
			if delta.peak <= 0 {
				continue // refunds alone cannot create threshold or burst alerts
			}
			after, previous, err := sumSpendBudgetAlertPeriodsWithDB(tx, budget, delta.start, delta.end)
			if err != nil {
				return err
			}
			beforeValue, err := checkedLedgerSubtract(int64(after), int64(delta.net))
			if err != nil {
				return err
			}
			peakValue, err := checkedLedgerAdd(beforeValue, int64(delta.peak))
			if err != nil {
				return err
			}
			before, peak := billing.MicroUSD(beforeValue), billing.MicroUSD(peakValue)
			if err := recordSpendBudgetThresholdAlerts(tx, budget, delta.input, delta.start, delta.end, before, peak); err != nil {
				return err
			}
			if err := recordSpendBudgetBurstAlert(tx, budget, delta.input, delta.start, delta.end, peak, previous); err != nil {
				return err
			}
		}
	}
	return nil
}
func sortedLedgerIDs(values map[int]int64) []int {
	ids := make([]int, 0, len(values))
	for id := range values {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return ids
}
func (redisLedgerStorage) PendingCredits(ctx context.Context, limit int) ([]ledger.Credit, error) {
	var rows []LedgerCreditSync
	if err := CoreDB().WithContext(ctx).Where("applied_at IS NULL").Order("user_id, sequence").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]ledger.Credit, len(rows))
	for i, r := range rows {
		result[i] = ledger.Credit{ID: r.ID, PayerID: r.UserID, Sequence: r.Sequence, Amount: r.Amount}
	}
	return result, nil
}
func (redisLedgerStorage) CreditApplied(ctx context.Context, id uint) error {
	return CoreDB().WithContext(ctx).Model(&LedgerCreditSync{}).Where("id = ? AND applied_at IS NULL", id).Update("applied_at", time.Now().UTC()).Error
}

func recordLedgerCreditSyncTx(tx *gorm.DB, userID int, amount int64) error {
	var sequence int64
	if err := tx.Model(&User{}).Where("id = ?", userID).Pluck("credit_seq", &sequence).Error; err != nil {
		return err
	}
	return tx.Create(&LedgerCreditSync{UserID: userID, Sequence: sequence, Amount: amount}).Error
}

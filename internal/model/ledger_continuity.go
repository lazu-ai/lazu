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
	"strings"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/ledger"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Epoch is a permanent financial identity. Recovery changes only the admission
// generation, fencing old processes without changing operation deduplication.
type LedgerContinuityState struct {
	ID                  uint      `gorm:"primaryKey" json:"id"`
	Epoch               string    `gorm:"size:64;not null" json:"epoch"`
	AdmissionGeneration int64     `gorm:"not null" json:"admission_generation"`
	Status              string    `gorm:"size:24;not null" json:"status"`
	RecoveryProof       string    `gorm:"type:text;not null;default:''" json:"recovery_proof,omitempty"`
	OpeningID           uint      `gorm:"not null;default:0" json:"opening_id,omitempty"`
	OpeningSHA256       string    `gorm:"size:64;not null;default:''" json:"opening_sha256,omitempty"`
	ActivatedAt         time.Time `json:"activated_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

func (s LedgerContinuityState) HotValue() string {
	return fmt.Sprintf("%s:%d", s.Epoch, s.AdmissionGeneration)
}

type LedgerRecoveryWriter struct {
	ID             string    `json:"id"`
	Role           string    `json:"role"`
	SpoolDirectory string    `json:"spool_directory"`
	StoppedAt      time.Time `json:"stopped_at"`
}

// Accepted requests whose results are absent from Redis and spools must be
// classified from retained operator evidence. Intents retain the original IDs;
// abandoned requests are explicit zero-amount releases, never guessed charges.
type LedgerRecoveryProof struct {
	Evidence                   string                                `json:"evidence"`
	Writers                    []LedgerRecoveryWriter                `json:"writers"`
	AcceptedRequestsClassified bool                                  `json:"accepted_requests_classified"`
	UnclassifiedRequests       int                                   `json:"unclassified_requests"`
	Intents                    []ledger.Event                        `json:"intents,omitempty"`
	AbandonedRequestIDs        []string                              `json:"abandoned_request_ids,omitempty"`
	LegacyPrecharges           []LedgerLegacyPrechargeClassification `json:"legacy_precharges,omitempty"`
	Opening                    *LedgerOpeningRequest                 `json:"opening,omitempty"`
}

type LedgerLegacyPrechargeClassification struct {
	LedgerID       uint   `json:"ledger_id"`
	AmountMicrousd int64  `json:"amount_microusd"`
	Status         string `json:"status"`
	Evidence       string `json:"evidence"`
}
type LedgerContinuityReport struct {
	State                          *LedgerContinuityState       `json:"core_state,omitempty"`
	HotValue                       string                       `json:"hot_value"`
	Maintenance                    bool                         `json:"maintenance"`
	LiveWriters                    []string                     `json:"live_writers"`
	Reservations                   []string                     `json:"reservations"`
	Uncommitted                    int64                        `json:"uncommitted"`
	ActiveMediaJobIDs              []uint                       `json:"active_media_job_ids"`
	SpoolFiles                     map[string]int               `json:"spool_files"`
	SpoolInventory                 map[string]ledger.SpoolUsage `json:"spool_inventory"`
	LegacyPrecharges               int64                        `json:"legacy_precharges"`
	UnresolvedSettlementFailureIDs []uint                       `json:"unresolved_settlement_failure_ids"`
	RetainedSettlementFailureIDs   []uint                       `json:"retained_settlement_failure_ids,omitempty"`
	WaivedSettlementFailureIDs     []uint                       `json:"waived_settlement_failure_ids,omitempty"`
	UnresolvedSettlementOutboxIDs  []uint                       `json:"unresolved_settlement_outbox_ids"`
}

func loadLedgerContinuity(ctx context.Context, db *gorm.DB) (LedgerContinuityState, error) {
	var state LedgerContinuityState
	if db == nil {
		return state, errors.New("core database is unavailable")
	}
	err := db.WithContext(ctx).First(&state, 1).Error
	if err == nil && (state.Epoch == "" || state.AdmissionGeneration <= 0) {
		return state, errors.New("permanent financial continuity record is invalid")
	}
	return state, err
}

// Auto-migration supports a genuinely empty self-hosted installation. It must
// never infer recovery from missing Redis state or bootstrap an existing account.
func bootstrapEmptyGatewayLedger(ctx context.Context, db *gorm.DB, engine *ledger.Engine) error {
	if migrationMode() != "auto" {
		return nil
	}
	_, err := loadLedgerContinuity(ctx, db)
	if err == nil {
		return nil
	} // Bind below enforces status and the exact Redis epoch.
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	for _, row := range []interface{}{&User{}, &Token{}, &WalletLedger{}, &TopUp{}, &MediaJob{}} {
		var count int64
		if err := db.WithContext(ctx).Unscoped().Model(row).Limit(1).Count(&count).Error; err != nil {
			return err
		}
		if count != 0 {
			return errors.New("existing installation requires explicit ledger-bootstrap before startup")
		}
	}
	usage, err := engine.SpoolUsage()
	if err != nil {
		return err
	}
	if usage.Files != 0 {
		return errors.New("existing settlement spool requires explicit ledger recovery")
	}
	_, err = BootstrapLedgerContinuity(ctx, db)
	return err
}

func BindLedgerContinuity(ctx context.Context, db *gorm.DB, engine *ledger.Engine) error {
	state, err := loadLedgerContinuity(ctx, db)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return errors.New("financial continuity is not initialized; run migrate -command ledger-bootstrap before starting writers")
	}
	if err != nil {
		return err
	}
	if state.Status != "active" {
		return fmt.Errorf("financial continuity status %q requires explicit recovery", state.Status)
	}
	if err := verifyContinuityOpening(ctx, db, state, false); err != nil {
		return err
	}
	engine.Continuity = state.HotValue()
	if err := engine.CheckContinuity(ctx); err != nil {
		return fmt.Errorf("%w; stop writers and use migrate -command ledger-report / ledger-recover", err)
	}
	return nil
}

// Opening is an immutable historical boundary, independent of the replaceable
// recovery proof. Runtime binding checks its small commitment only; explicit
// stopped-writer commands verify the complete historical financial prefix.
func verifyContinuityOpening(ctx context.Context, db *gorm.DB, state LedgerContinuityState, history bool) error {
	if state.OpeningID == 0 && state.OpeningSHA256 == "" {
		return nil
	}
	if state.OpeningID == 0 || state.OpeningSHA256 == "" {
		return errors.New("financial opening binding is incomplete")
	}
	binding := LedgerOpeningBinding{OpeningID: state.OpeningID, OpeningSHA256: state.OpeningSHA256}
	var opening LedgerOpeningManifest
	var err error
	if history {
		opening, err = VerifyLedgerOpeningHistory(ctx, db, binding)
	} else {
		opening, err = LoadLedgerOpening(ctx, db, binding)
	}
	if err != nil {
		return fmt.Errorf("financial opening: %w", err)
	}
	if opening.Epoch != state.Epoch {
		return errors.New("financial opening epoch differs from permanent continuity")
	}
	return nil
}

// MarkLedgerDurabilityFailure is called off the request path after both Redis
// settlement and persistent fsync fail. A successful marker prevents a restarted
// process from admitting requests until the operator classifies accepted work.
// Existing process settlements retain the same epoch and may still finish.
func MarkLedgerDurabilityFailure(ctx context.Context, expectedContinuity string) error {
	if expectedContinuity == "" {
		return errors.New("financial engine has no continuity identity")
	}
	state, err := loadLedgerContinuity(ctx, CoreDB())
	if err != nil {
		return err
	}
	if state.HotValue() != expectedContinuity {
		return ledger.ErrContinuity
	}
	if state.Status == "recovery_required" || state.Status == "recovering" {
		return nil
	}
	if state.Status != "active" {
		return fmt.Errorf("cannot mark financial continuity status %q", state.Status)
	}
	result := CoreDB().WithContext(ctx).Model(&LedgerContinuityState{}).Where("id = ? AND epoch = ? AND admission_generation = ? AND status = ?", 1, state.Epoch, state.AdmissionGeneration, "active").Updates(map[string]interface{}{"status": "recovery_required", "updated_at": time.Now().UTC()})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errors.New("financial continuity changed while recording durability failure")
	}
	return nil
}

// A lease is observation, not authorization. Recovery also requires the writer
// inventory and explicit stopped-writers proof because Redis loss loses leases.
func StartLedgerNodeLease(engine *ledger.Engine) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := engine.CheckContinuity(ctx); err != nil {
		return err
	}
	host, _ := os.Hostname()
	node := fmt.Sprintf("%s:%d:%s", host, os.Getpid(), common.GenerateRequestID())
	key := ledger.Prefix + "nodes:" + node
	value := common.ResolveProcessRole(os.Getenv("PROCESS_ROLE")) + ":" + engine.Continuity
	if err := engine.Redis.Set(ctx, key, value, 30*time.Second).Err(); err != nil {
		return err
	}
	common.SafeCtxGo(context.Background(), "ledger-node-lease", func() {
		timer := time.NewTicker(5 * time.Second)
		defer timer.Stop()
		for range timer.C {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_ = engine.Redis.Set(ctx, key, value, 30*time.Second).Err()
			cancel()
		}
	})
	return nil
}
func ledgerScan(ctx context.Context, pattern string) ([]string, error) {
	var cursor uint64
	keys := []string{}
	seen := map[string]bool{}
	for {
		batch, next, err := common.RDB.Scan(ctx, cursor, pattern, 500).Result()
		if err != nil {
			return nil, err
		}
		for _, key := range batch {
			if !seen[key] {
				keys = append(keys, key)
				seen[key] = true
			}
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
	sort.Strings(keys)
	return keys, nil
}
func ReportLedgerContinuity(ctx context.Context, db *gorm.DB, dirs []string) (LedgerContinuityReport, error) {
	report := LedgerContinuityReport{SpoolFiles: map[string]int{}, SpoolInventory: map[string]ledger.SpoolUsage{}}
	state, err := loadLedgerContinuity(ctx, db)
	if err == nil {
		report.State = &state
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return report, err
	}
	if common.RDB == nil {
		return report, errors.New("Redis is required")
	}
	report.HotValue, err = common.RDB.Get(ctx, ledger.ContinuityKey).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return report, err
	}
	maintenance, err := common.RDB.Exists(ctx, ledger.Prefix+"maintenance").Result()
	if err != nil {
		return report, err
	}
	report.Maintenance = maintenance > 0
	report.LiveWriters, err = ledgerScan(ctx, ledger.Prefix+"nodes:*")
	if err != nil {
		return report, err
	}
	report.Reservations, err = common.RDB.HKeys(ctx, ledger.Prefix+"reservations").Result()
	if err != nil {
		return report, err
	}
	sort.Strings(report.Reservations)
	report.Uncommitted, err = common.RDB.ZCard(ctx, ledger.Prefix+"uncommitted").Result()
	if err != nil {
		return report, err
	}
	if db.Migrator().HasTable(&MediaJob{}) {
		if err := db.WithContext(ctx).Model(&MediaJob{}).Where("status IN ?", []string{MediaJobStatusPending, MediaJobStatusRunning}).Order("id").Pluck("id", &report.ActiveMediaJobIDs).Error; err != nil {
			return report, err
		}
	}
	if db.Migrator().HasTable(&WalletLedger{}) {
		if err := db.WithContext(ctx).Model(&WalletLedger{}).Where("ref_type = ? AND delta_microusd < 0", "relay_preconsume").Count(&report.LegacyPrecharges).Error; err != nil {
			return report, err
		}
	}
	if db.Migrator().HasTable(&SettlementFailure{}) {
		if err := db.WithContext(ctx).Model(&SettlementFailure{}).Where("status <> ?", SettlementFailureStatusResolved).Order("id").Pluck("id", &report.RetainedSettlementFailureIDs).Error; err != nil {
			return report, err
		}
		report.UnresolvedSettlementFailureIDs = append([]uint(nil), report.RetainedSettlementFailureIDs...)
		if db.Migrator().HasTable(&LedgerLegacySettlementWaiver{}) {
			ids, err := VerifiedUnresolvedSettlementFailureIDs(ctx, db)
			if err != nil {
				return report, err
			}
			report.UnresolvedSettlementFailureIDs = ids
			blocking := map[uint]bool{}
			for _, id := range ids {
				blocking[id] = true
			}
			for _, id := range report.RetainedSettlementFailureIDs {
				if !blocking[id] {
					report.WaivedSettlementFailureIDs = append(report.WaivedSettlementFailureIDs, id)
				}
			}
		}
	}
	if db.Migrator().HasTable(&SettlementOutbox{}) {
		if err := db.WithContext(ctx).Model(&SettlementOutbox{}).Where("status <> ?", SettlementFailureStatusResolved).Order("id").Pluck("id", &report.UnresolvedSettlementOutboxIDs).Error; err != nil {
			return report, err
		}
	}
	var spoolErrors []error
	for _, dir := range dirs {
		usage, err := inspectContinuitySpool(dir)
		report.SpoolInventory[dir] = usage
		report.SpoolFiles[dir] = int(usage.Files)
		if err != nil {
			spoolErrors = append(spoolErrors, err)
		}
	}
	return report, errors.Join(spoolErrors...)
}

func inspectContinuitySpool(directory string) (ledger.SpoolUsage, error) {
	if _, err := os.ReadDir(directory); err != nil {
		return ledger.SpoolUsage{}, fmt.Errorf("spool %s must exist and be readable: %w", directory, err)
	}
	engine := ledger.Engine{SpoolDir: directory}
	usage, err := engine.SpoolUsage()
	if err != nil {
		return usage, fmt.Errorf("spool %s: %w", directory, err)
	}
	// A manifest without its payload is still unresolved financial evidence.
	// Never treat moving a permanent error out of the root as a clean drain.
	quarantine, err := os.ReadDir(filepath.Join(directory, "quarantine"))
	if err != nil && !os.IsNotExist(err) {
		return usage, err
	}
	if len(quarantine) > 0 {
		return usage, fmt.Errorf("spool %s contains unresolved quarantine evidence: %d entries, exposure %d microusd, %d unknown exposure files; retain and review it before recovery or resume", directory, len(quarantine), usage.UnresolvedExposure, usage.UnknownExposureFiles)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return usage, err
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".pending-") {
			return usage, fmt.Errorf("spool %s contains unfinished fsync intent %s; classify it before recovery", directory, entry.Name())
		}
	}
	return usage, nil
}
func continuityCommandLock(ctx context.Context, db *gorm.DB) (func(), error) {
	if db != nil && db.Dialector.Name() == "postgres" {
		// A session advisory lock survives Redis loss and is released by the
		// server when the command exits or crashes, allowing immediate recovery.
		raw, err := db.DB()
		if err != nil {
			return nil, err
		}
		connection, err := raw.Conn(ctx)
		if err != nil {
			return nil, err
		}
		var acquired bool
		if err := connection.QueryRowContext(ctx, "SELECT pg_try_advisory_lock(1279343189)").Scan(&acquired); err != nil {
			connection.Close()
			return nil, err
		}
		if !acquired {
			connection.Close()
			return nil, errors.New("another continuity command is running")
		}
		return func() {
			unlockCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_, _ = connection.ExecContext(unlockCtx, "SELECT pg_advisory_unlock(1279343189)")
			_ = connection.Close()
		}, nil
	}
	if common.RDB == nil {
		return nil, errors.New("Redis is required")
	}
	owner := common.GenerateRequestID()
	ok, err := common.RDB.SetNX(ctx, ledger.Prefix+"continuity_command", owner, 30*time.Second).Result()
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errors.New("another continuity command is running")
	}
	return func() {
		_ = common.RDB.Eval(context.Background(), `if redis.call('GET',KEYS[1])==ARGV[1] then return redis.call('DEL',KEYS[1]) end return 0`, []string{ledger.Prefix + "continuity_command"}, owner).Err()
	}, nil
}
func BootstrapLedgerContinuity(ctx context.Context, db *gorm.DB) (LedgerContinuityState, error) {
	return BootstrapLedgerContinuityWithProof(ctx, db, nil, false, nil)
}

func BootstrapLedgerContinuityWithProof(ctx context.Context, db *gorm.DB, dirs []string, stopped bool, proof *LedgerRecoveryProof) (LedgerContinuityState, error) {
	var empty LedgerContinuityState
	unlock, err := continuityCommandLock(ctx, db)
	if err != nil {
		return empty, err
	}
	defer unlock()
	report, err := ReportLedgerContinuity(ctx, db, dirs)
	if err != nil {
		return empty, err
	}
	if report.State != nil && report.State.Status == "active" {
		if report.HotValue == report.State.HotValue() {
			if err := verifyContinuityOpening(ctx, db, *report.State, true); err != nil {
				return empty, err
			}
			return *report.State, nil
		}
		return empty, errors.New("existing continuity lost its Redis state; bootstrap cannot recover history, use ledger-recover")
	}
	if len(report.LiveWriters) > 0 || len(report.ActiveMediaJobIDs) > 0 {
		return empty, errors.New("bootstrap requires no writers or active media; classify existing activity before cutover")
	}
	if len(report.UnresolvedSettlementFailureIDs) > 0 || len(report.UnresolvedSettlementOutboxIDs) > 0 {
		return empty, errors.New("resolve every previous-release settlement failure and outbox before bootstrap")
	}
	if report.LegacyPrecharges > 0 || proof != nil {
		if proof == nil {
			return empty, errors.New("historic legacy precharges require -writers-stopped, every -spool-dirs, and -ledger-proof with each settled/refunded ledger ID classified")
		}
		if err := verifyRecoveryProof(*proof, dirs, stopped); err != nil {
			return empty, err
		}
		if len(proof.Intents) > 0 || len(proof.AbandonedRequestIDs) > 0 {
			return empty, errors.New("legacy bootstrap requires all old requests settled or refunded by the previous release; it cannot replay unknown precharges")
		}
		if proof.Opening != nil {
			if len(proof.LegacyPrecharges) != 0 || !report.Maintenance || len(report.Reservations) != 0 || report.Uncommitted != 0 {
				return empty, errors.New("initial opening requires maintenance, drained financial state, and no per-request legacy classifications")
			}
			if err := VerifyRelayEventsDrained(ctx); err != nil {
				return empty, err
			}
		} else if err := verifyLegacyPrechargeClassifications(ctx, db, proof.LegacyPrecharges); err != nil {
			return empty, err
		}
		for _, files := range report.SpoolFiles {
			if files > 0 {
				return empty, errors.New("legacy bootstrap requires every old spool to be drained before cutover")
			}
		}
	}
	for _, row := range []interface{}{&RedisLedgerOperation{}, &LedgerCreditSync{}, &LedgerCounterRefresh{}} {
		var n int64
		if err := db.WithContext(ctx).Model(row).Count(&n).Error; err != nil {
			return empty, err
		}
		if n > 0 {
			return empty, errors.New("bootstrap requires empty permanent financial operation and outbox tables")
		}
	}
	keys, err := ledgerScan(ctx, ledger.Prefix+"*")
	if err != nil {
		return empty, err
	}
	for _, key := range keys {
		if key != ledger.Prefix+"continuity_command" && key != ledger.Prefix+"maintenance" && key != ledger.ContinuityKey {
			return empty, fmt.Errorf("bootstrap refuses existing financial state %s", key)
		}
	}
	state := LedgerContinuityState{ID: 1, Epoch: common.GenerateRequestID(), AdmissionGeneration: 1, Status: "bootstrapping"}
	if proof != nil {
		raw, err := json.Marshal(proof)
		if err != nil {
			return empty, err
		}
		state.RecoveryProof = string(raw)
	}
	if report.State != nil {
		if report.State.Status != "bootstrapping" {
			return empty, errors.New("unfinished recovery cannot be bootstrapped")
		}
		state = *report.State
		if state.RecoveryProof != "" && proof == nil {
			return empty, errors.New("unfinished bootstrap requires its original proof")
		}
		if proof != nil {
			raw, _ := json.Marshal(proof)
			if state.RecoveryProof != string(raw) {
				return empty, errors.New("unfinished bootstrap must use the same immutable legacy proof")
			}
		}
	}
	if report.HotValue != "" && report.HotValue != state.HotValue() {
		return empty, errors.New("bootstrap sentinel conflicts with permanent state")
	}
	// The SQL opening and permanent binding commit together. Invalid evidence
	// or changed baselines must not leave an unfixable bootstrapping record.
	if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if proof != nil && proof.Opening != nil {
			request := *proof.Opening
			if !request.WritersStopped {
				return errors.New("initial opening must attest stopped writers")
			}
			if report.State == nil && request.Epoch != "" {
				state.Epoch = request.Epoch
			}
			if request.Epoch != "" && request.Epoch != state.Epoch {
				return errors.New("initial opening must match the permanent epoch")
			}
			request.Epoch = state.Epoch
			opening, err := BuildLedgerOpening(ctx, tx, request)
			if err != nil {
				return err
			}
			binding := LedgerOpeningBinding{OpeningID: opening.ID, OpeningSHA256: opening.ManifestSHA256}
			if err := VerifyLedgerOpeningBaselines(ctx, tx, binding); err != nil {
				return err
			}
			state.OpeningID, state.OpeningSHA256 = opening.ID, opening.ManifestSHA256
		}
		if report.State == nil {
			return tx.Create(&state).Error
		}
		return tx.Save(&state).Error
	}, &sql.TxOptions{Isolation: sql.LevelSerializable}); err != nil {
		return state, err
	}
	if err := common.RDB.Set(ctx, ledger.ContinuityKey, state.HotValue(), 0).Err(); err != nil {
		return state, err
	}
	state.Status = "active"
	state.ActivatedAt = time.Now().UTC()
	err = db.WithContext(ctx).Save(&state).Error
	return state, err
}

func verifyLegacyPrechargeClassifications(ctx context.Context, db *gorm.DB, proof []LedgerLegacyPrechargeClassification) error {
	if len(proof) > 0 {
		if err := ImportLegacyPrechargeClassifications(ctx, db, proof); err != nil {
			return err
		}
	}
	_, err := LoadLegacyPrechargeClassifications(ctx, db)
	return err
}

func verifyRecoveryProof(proof LedgerRecoveryProof, dirs []string, stopped bool) error {
	if !stopped {
		return errors.New("recovery requires -writers-stopped after every API, callback and jobs writer is stopped")
	}
	if !proof.AcceptedRequestsClassified || proof.UnclassifiedRequests != 0 || strings.TrimSpace(proof.Evidence) == "" || len(proof.Writers) == 0 {
		return errors.New("recovery proof must identify stopped writers and evidence classifying every accepted request")
	}
	expected := map[string]bool{}
	for _, dir := range dirs {
		absolute, err := filepath.Abs(dir)
		if err != nil {
			return err
		}
		expected[absolute] = true
	}
	if len(expected) == 0 {
		return errors.New("provide every writer spool directory with -spool-dirs")
	}
	seen := map[string]bool{}
	ids := map[string]bool{}
	for _, writer := range proof.Writers {
		if writer.ID == "" || writer.Role == "" || ids[writer.ID] || writer.StoppedAt.IsZero() || writer.StoppedAt.After(time.Now()) {
			return errors.New("invalid stopped writer inventory")
		}
		ids[writer.ID] = true
		absolute, err := filepath.Abs(writer.SpoolDirectory)
		if err != nil {
			return err
		}
		if !expected[absolute] {
			return fmt.Errorf("writer %s spool directory is absent from -spool-dirs", writer.ID)
		}
		seen[absolute] = true
	}
	if len(seen) != len(expected) {
		return errors.New("spool directories and stopped writer inventory differ")
	}
	classified := map[string]bool{}
	for _, event := range proof.Intents {
		if err := validateRecoveryEvent(event); err != nil {
			return err
		}
		if event.RequestID == "" || event.PayerID <= 0 || event.OperationID != event.RequestID || classified[event.RequestID] {
			return errors.New("invalid or duplicate recovery intent")
		}
		if event.Kind != "settle" && event.Kind != "release" && event.Kind != "refund" {
			return errors.New("invalid recovery intent kind")
		}
		if event.Kind == "settle" && event.Amount < 0 || event.Kind == "refund" && event.Amount >= 0 || event.Kind == "release" && event.Amount != 0 {
			return errors.New("invalid recovery intent amount")
		}
		classified[event.RequestID] = true
	}
	for _, id := range proof.AbandonedRequestIDs {
		if id == "" || classified[id] {
			return errors.New("invalid duplicate abandoned request identity")
		}
		classified[id] = true
	}
	return nil
}
func clearDerivedLedgerCounters(ctx context.Context) error {
	for _, pattern := range []string{"wallet:*", "token:*", "token_gen:*", "budget:*", "budget_index:*", "reservation_budgets:*", "fence:*", "budget_fence"} {
		keys, err := ledgerScan(ctx, ledger.Prefix+pattern)
		if err != nil {
			return err
		}
		for start := 0; start < len(keys); start += 500 {
			end := start + 500
			if end > len(keys) {
				end = len(keys)
			}
			if err := common.RDB.Del(ctx, keys[start:end]...).Err(); err != nil {
				return err
			}
		}
	}
	return nil
}

// Recovery always leaves maintenance enabled. A separate resume command proves
// the drain and matching epoch before it permits admission again.
func RecoverLedgerContinuity(ctx context.Context, db *gorm.DB, dirs []string, stopped bool, proof LedgerRecoveryProof) (LedgerContinuityState, error) {
	state, err := loadLedgerContinuity(ctx, db)
	if err != nil {
		return state, err
	}
	if proof.Opening != nil {
		return state, errors.New("initial opening evidence cannot classify requests accepted after cutover")
	}
	if state.Status != "active" && state.Status != "recovering" && state.Status != "recovery_required" {
		return state, errors.New("continuity is not recoverable; a legacy rollback invalidates the initial opening")
	}
	if err := verifyRecoveryProof(proof, dirs, stopped); err != nil {
		return state, err
	}
	if err := verifyContinuityOpening(ctx, db, state, true); err != nil {
		return state, err
	}
	unlock, err := continuityCommandLock(ctx, db)
	if err != nil {
		return state, err
	}
	defer unlock()
	report, err := ReportLedgerContinuity(ctx, db, dirs)
	if err != nil {
		return state, err
	}
	if !report.Maintenance || len(report.LiveWriters) > 0 {
		return state, errors.New("recovery requires maintenance and no live financial writer leases")
	}
	if len(report.ActiveMediaJobIDs) > 0 {
		return state, fmt.Errorf("classify and finish/cancel %d active media jobs before recovery", len(report.ActiveMediaJobIDs))
	}
	if len(report.UnresolvedSettlementFailureIDs) > 0 || len(report.UnresolvedSettlementOutboxIDs) > 0 {
		return state, errors.New("resolve/classify legacy settlement failures and outboxes before financial recovery")
	}
	events, paths, err := collectLedgerRecoveryEvents(ctx, dirs, proof.Intents)
	if err != nil {
		return state, err
	}
	classifications := map[string]bool{}
	financialIdentities := map[string]bool{}
	for _, event := range events {
		if db.Migrator().HasTable(&LedgerLegacySettlementWaiver{}) {
			blocked, err := LegacySettlementWaiverBlocksOperation(ctx, db, event.PayerID, "relay_settlement", event.RequestID, event.RequestID)
			if err != nil {
				return state, err
			}
			if blocked {
				return state, ErrLegacySettlementWaived
			}
		}
		classifications[event.RequestID] = true
		if event.Kind != "release" {
			financialIdentities[event.RequestID] = true
		}
	}
	for _, id := range proof.AbandonedRequestIDs {
		if db.Migrator().HasTable(&LedgerLegacySettlementWaiver{}) {
			blocked, err := LegacySettlementWaiverBlocksOperation(ctx, db, 0, "relay_settlement", id, id)
			if err != nil {
				return state, err
			}
			if blocked {
				return state, ErrLegacySettlementWaived
			}
		}
		if financialIdentities[id] {
			return state, fmt.Errorf("request %s has a financial result and cannot be classified abandoned", id)
		}
		var committed int64
		if err := db.WithContext(ctx).Model(&RedisLedgerOperation{}).Where("request_id = ?", id).Count(&committed).Error; err != nil {
			return state, err
		}
		if committed > 0 {
			return state, fmt.Errorf("request %s has a permanent financial result and cannot be abandoned", id)
		}
		classifications[id] = true
	}
	for _, id := range report.Reservations {
		if !classifications[id] {
			return state, fmt.Errorf("surviving reservation %s lacks an explicit result or abandoned classification", id)
		}
	}
	// Missing journal payloads cannot be discarded merely because the operator
	// declared a quiet window. Require recovered evidence or a permanent op.
	ids, err := common.RDB.ZRange(ctx, ledger.Prefix+"uncommitted", 0, -1).Result()
	if err != nil {
		return state, err
	}
	for _, id := range ids {
		if financialIdentities[id] {
			continue
		}
		var n int64
		if err := db.WithContext(ctx).Model(&RedisLedgerOperation{}).Where("request_id = ?", id).Count(&n).Error; err != nil {
			return state, err
		}
		if n == 0 {
			return state, fmt.Errorf("pending financial operation %s has no recoverable payload or permanent identity", id)
		}
	}
	financial := []ledger.Event{}
	for _, event := range events {
		if event.Kind != "release" {
			financial = append(financial, event)
		}
	}
	if err := verifyRecoveredPermanentIdentities(ctx, db, financial); err != nil {
		return state, err
	}

	raw, err := json.Marshal(proof)
	if err != nil {
		return state, err
	}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&state, 1).Error; err != nil {
			return err
		}
		if state.Status == "recovering" {
			if state.RecoveryProof != string(raw) {
				return errors.New("unfinished recovery must use the same immutable proof")
			}
			return nil
		}
		if state.Status != "active" && state.Status != "recovery_required" {
			return errors.New("only active, recovery_required or recovering continuity can enter recovery")
		}
		state.AdmissionGeneration++
		state.Status = "recovering"
		state.RecoveryProof = string(raw)
		return tx.Save(&state).Error
	})
	if err != nil {
		return state, err
	}
	if err := common.RDB.Set(ctx, ledger.ContinuityKey, state.HotValue(), 0).Err(); err != nil {
		return state, err
	}
	engine := ledger.New(common.RDB, redisLedgerStorage{})
	engine.Continuity = state.HotValue()
	engine.TelemetryStream = common.RelayEventStream
	engine.TelemetryEncode = EncodeFinancialTelemetry

	// SQL operation identities are permanent and independent of the epoch. A
	// crash here is resumed with exactly the same proof and deduplicated rows.
	for start := 0; start < len(financial); start += 500 {
		end := min(start+500, len(financial))
		if err := engine.Storage.Apply(ctx, financial[start:end]); err != nil {
			return state, err
		}
	}
	for _, event := range events {
		if err := engine.PublishRecoveryTelemetry(ctx, event); err != nil {
			return state, err
		}
	}
	// Retain terminal identities for abandoned/released requests as well. A
	// repeated request ID may not be re-admitted immediately after recovery.
	for _, event := range events {
		status := "settled"
		if event.Kind == "release" {
			status = "released"
		}
		encoded, err := json.Marshal(event.Input)
		if err != nil {
			return state, err
		}
		if err := common.RDB.HSet(ctx, ledger.Prefix+"request:"+event.RequestID, "state", status, "input", string(encoded)).Err(); err != nil {
			return state, err
		}
	}
	for _, id := range proof.AbandonedRequestIDs {
		if err := common.RDB.HSet(ctx, ledger.Prefix+"request:"+id, "state", "released").Err(); err != nil {
			return state, err
		}
	}

	// Every known hold has an explicit result or abandonment, and lost holds are
	// covered by the retained evidence attestation. They may now be retired.
	if err := common.RDB.Del(ctx, ledger.Prefix+"reservations", ledger.Prefix+"expiry", ledger.Prefix+"exposure", ledger.Prefix+"uncommitted", ledger.Stream, ledger.DeadStream, ledger.Prefix+"dead_ids", ledger.Prefix+"stream_gc").Err(); err != nil {
		return state, err
	}
	if err := clearDerivedLedgerCounters(ctx); err != nil {
		return state, err
	}
	// Core credit sequences and administrative values are already authoritative;
	// the next cold snapshot includes them. Retiring their delivery tasks is safe
	// only after all hot financial counters have been cleared under maintenance.
	now := time.Now().UTC()
	if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, row := range []interface{}{&LedgerCreditSync{}, &LedgerCounterRefresh{}} {
			if err := tx.Model(row).Where("applied_at IS NULL").Update("applied_at", now).Error; err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return state, err
	}
	for _, path := range paths {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return state, err
		}
		directory, err := os.Open(filepath.Dir(path))
		if err != nil {
			return state, err
		}
		err = directory.Sync()
		directory.Close()
		if err != nil {
			return state, err
		}
	}
	state.Status = "active"
	state.ActivatedAt = now
	if err := db.WithContext(ctx).Save(&state).Error; err != nil {
		return state, err
	}
	return state, nil
}
func validateRecoveryEvent(event ledger.Event) error {
	if event.RequestID == "" || event.OperationID != event.RequestID || event.PayerID <= 0 || event.ActorID < 0 || event.ProjectID < 0 || event.TokenID < 0 {
		return errors.New("invalid recovered financial identity")
	}
	if event.Kind != "settle" && event.Kind != "release" && event.Kind != "refund" {
		return errors.New("invalid recovered financial kind")
	}
	const maxMoney int64 = 9007199254740991
	if event.Amount > maxMoney || event.Amount < -maxMoney || event.Kind == "settle" && event.Amount < 0 || event.Kind == "release" && event.Amount != 0 || event.Kind == "refund" && event.Amount >= 0 {
		return errors.New("invalid recovered financial amount")
	}
	return nil
}
func verifyRecoveredPermanentIdentities(ctx context.Context, db *gorm.DB, events []ledger.Event) error {
	for start := 0; start < len(events); start += 500 {
		end := min(start+500, len(events))
		ids := []string{}
		byID := map[string]ledger.Event{}
		for _, event := range events[start:end] {
			id := ledgerOperationID(event.OperationID, event.Kind)
			ids = append(ids, id)
			byID[id] = event
		}
		var committed []RedisLedgerOperation
		if err := db.WithContext(ctx).Where("id IN ?", ids).Find(&committed).Error; err != nil {
			return err
		}
		for _, row := range committed {
			event := byID[row.ID]
			if row.Amount != event.Amount || row.PayerID != event.PayerID || row.TokenID != event.TokenID || row.ProjectID != event.ProjectID || row.ActorID != event.ActorID {
				return fmt.Errorf("recovery conflicts with permanent financial identity %s", row.ID)
			}
		}
	}
	return nil
}
func collectLedgerRecoveryEvents(ctx context.Context, dirs []string, proofEvents []ledger.Event) ([]ledger.Event, []string, error) {
	events := map[string]ledger.Event{}
	paths := []string{}
	add := func(event ledger.Event) error {
		if err := validateRecoveryEvent(event); err != nil {
			return err
		}
		key := event.Kind + ":" + event.OperationID
		if prior, ok := events[key]; ok {
			if prior.Amount != event.Amount || prior.PayerID != event.PayerID || prior.TokenID != event.TokenID || prior.ProjectID != event.ProjectID || prior.ActorID != event.ActorID || prior.PricingVersion != event.PricingVersion {
				return fmt.Errorf("conflicting recovered financial identity %s", key)
			}
			if event.TelemetryEnvelope == "" {
				event.TelemetryEnvelope = prior.TelemetryEnvelope
			}
		}
		events[key] = event
		return nil
	}
	decode := func(raw interface{}) error {
		payload, ok := raw.(string)
		if !ok {
			return errors.New("financial recovery source has no payload")
		}
		var event ledger.Event
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			return err
		}
		return add(event)
	}
	var cursor uint64
	for {
		values, next, err := common.RDB.HScan(ctx, ledger.Prefix+"journal", cursor, "*", 500).Result()
		if err != nil {
			return nil, nil, err
		}
		for i := 1; i < len(values); i += 2 {
			if err := decode(values[i]); err != nil {
				return nil, nil, err
			}
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
	for _, stream := range []string{ledger.Stream, ledger.DeadStream} {
		start := "-"
		for {
			rows, err := common.RDB.XRangeN(ctx, stream, start, "+", 500).Result()
			if err != nil {
				return nil, nil, err
			}
			for _, row := range rows {
				if err := decode(row.Values["event"]); err != nil {
					return nil, nil, err
				}
			}
			if len(rows) < 500 {
				break
			}
			start = "(" + rows[len(rows)-1].ID
		}
	}
	for _, event := range proofEvents {
		if err := add(event); err != nil {
			return nil, nil, err
		}
	}
	for _, dir := range dirs {
		if _, err := inspectContinuitySpool(dir); err != nil {
			return nil, nil, err
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil, nil, err
		}
		for _, entry := range entries {
			if !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			payload, err := os.ReadFile(path)
			if err != nil {
				return nil, nil, err
			}
			if err := decode(string(payload)); err != nil {
				return nil, nil, err
			}
			paths = append(paths, path)
		}
	}
	keys := make([]string, 0, len(events))
	for key := range events {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]ledger.Event, 0, len(keys))
	for _, key := range keys {
		result = append(result, events[key])
	}
	return result, paths, nil
}
func ResumeLedgerContinuity(ctx context.Context, db *gorm.DB, dirs []string, stopped bool) error {
	if !stopped {
		return errors.New("resume requires -writers-stopped until the recovery proof and final drain have been checked")
	}
	unlock, err := continuityCommandLock(ctx, db)
	if err != nil {
		return err
	}
	defer unlock()
	report, err := ReportLedgerContinuity(ctx, db, dirs)
	if err != nil {
		return err
	}
	if report.State == nil || report.State.Status != "active" || report.State.HotValue() != report.HotValue {
		return ledger.ErrContinuity
	}
	if err := verifyContinuityOpening(ctx, db, *report.State, true); err != nil {
		return err
	}
	if !report.Maintenance || len(report.LiveWriters) > 0 || len(report.ActiveMediaJobIDs) > 0 || len(dirs) == 0 {
		return errors.New("resume requires maintenance, no live writers/media jobs, and every spool directory")
	}
	if len(report.UnresolvedSettlementFailureIDs) > 0 || len(report.UnresolvedSettlementOutboxIDs) > 0 {
		return errors.New("legacy settlement failures or outboxes remain unresolved")
	}
	if report.State.RecoveryProof != "" {
		var proof LedgerRecoveryProof
		if err := json.Unmarshal([]byte(report.State.RecoveryProof), &proof); err != nil {
			return err
		}
		if err := verifyRecoveryProof(proof, dirs, stopped); err != nil {
			return err
		}
	}
	for _, files := range report.SpoolFiles {
		if files != 0 {
			return errors.New("resume requires empty spools")
		}
	}
	health, err := ReadLedgerHealth(ctx)
	if err != nil {
		return err
	}
	if health.Uncommitted != 0 || health.Reservations != 0 || health.Exposure != 0 || health.Dead != 0 {
		return errors.New("financial drain is incomplete")
	}
	for _, row := range []interface{}{&LedgerCreditSync{}, &LedgerCounterRefresh{}} {
		var n int64
		if err := db.WithContext(ctx).Model(row).Where("applied_at IS NULL").Count(&n).Error; err != nil {
			return err
		}
		if n > 0 {
			return errors.New("credit or administrative refresh outbox is not drained")
		}
	}
	if err := verifyFinancialDeadStreamEmpty(ctx); err != nil {
		return err
	}
	if err := VerifyRelayEventsDrained(ctx); err != nil {
		return err
	}
	return common.RDB.Del(ctx, ledger.Prefix+"maintenance").Err()
}

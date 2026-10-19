package model

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lazu-ai/lazu/pkg/billing"
	"gorm.io/gorm"
)

const ledgerOpeningVersion = 1
const ledgerOpeningPageSize = 500

// LedgerOpeningManifest commits to the SQL authority at the first, stopped-
// writer cutover. It does not classify, settle, refund, or replay old precharges.
// The continuity record must permanently retain both its ID and ManifestSHA256.
type LedgerOpeningManifest struct {
	ID                    uint      `gorm:"primaryKey;autoIncrement:false" json:"id"`
	Epoch                 string    `gorm:"size:64;not null" json:"epoch"`
	FormatVersion         int       `gorm:"not null" json:"format_version"`
	CapturedAt            time.Time `gorm:"not null" json:"captured_at"`
	OperatorEvidence      string    `gorm:"type:text;not null" json:"operator_evidence"`
	WalletLedgerThroughID uint      `gorm:"not null" json:"wallet_ledger_through_id"`
	WalletLedgerCount     int64     `gorm:"not null" json:"wallet_ledger_count"`
	WalletLedgerSHA256    string    `gorm:"size:64;not null" json:"wallet_ledger_sha256"`
	LegacyPrechargeCount  int64     `gorm:"not null" json:"legacy_precharge_count"`
	LegacyPrechargeSHA256 string    `gorm:"size:64;not null" json:"legacy_precharge_sha256"`
	SnapshotSHA256        string    `gorm:"size:64;not null" json:"snapshot_sha256"`
	SectionsJSON          string    `gorm:"type:text;not null" json:"sections"`
	SourceProofsJSON      string    `gorm:"type:text;not null" json:"source_proofs"`
	ManifestSHA256        string    `gorm:"size:64;not null" json:"manifest_sha256"`
}

// Chunks contain only allowed financial baseline fields. Historical wallet
// metadata and credentials are hashed in place, never copied to these chunks.
type LedgerOpeningChunk struct {
	OpeningID uint   `gorm:"primaryKey;autoIncrement:false" json:"opening_id"`
	Section   string `gorm:"primaryKey;size:32" json:"section"`
	Sequence  int    `gorm:"primaryKey;autoIncrement:false" json:"sequence"`
	RowCount  int64  `gorm:"not null" json:"row_count"`
	Payload   string `gorm:"type:text;not null" json:"-"`
	SHA256    string `gorm:"size:64;not null" json:"sha256"`
}

func (*LedgerOpeningManifest) BeforeUpdate(*gorm.DB) error {
	return errors.New("ledger opening is immutable")
}
func (*LedgerOpeningManifest) BeforeDelete(*gorm.DB) error {
	return errors.New("ledger opening is permanent")
}
func (*LedgerOpeningChunk) BeforeUpdate(*gorm.DB) error {
	return errors.New("ledger opening snapshot is immutable")
}
func (*LedgerOpeningChunk) BeforeDelete(*gorm.DB) error {
	return errors.New("ledger opening snapshot is permanent")
}

type LedgerOpeningRequest struct {
	Epoch            string    `json:"epoch"`
	OperatorEvidence string    `json:"operator_evidence"`
	WritersStopped   bool      `json:"writers_stopped"`
	CapturedAt       time.Time `json:"captured_at"`
}

type LedgerOpeningBinding struct {
	OpeningID     uint   `json:"opening_id"`
	OpeningSHA256 string `json:"opening_sha256"`
}

type ledgerOpeningSection struct {
	Name   string `json:"name"`
	Rows   int64  `json:"rows"`
	Chunks int    `json:"chunks"`
	SHA256 string `json:"sha256"`
}

type ledgerOpeningSourceProof struct {
	Table     string   `json:"table"`
	Primary   string   `json:"primary"`
	Columns   []string `json:"columns"`
	ThroughID uint     `json:"through_id"`
	Rows      int64    `json:"rows"`
	SHA256    string   `json:"sha256"`
}

type ledgerOpeningWallet struct {
	ID        int              `gorm:"column:id" json:"id"`
	Quota     billing.MicroUSD `json:"quota_microusd"`
	CreditSeq int64            `json:"credit_seq"`
}

type ledgerOpeningKey struct {
	ID             int              `gorm:"column:id" json:"id"`
	UserID         int              `gorm:"column:user_id" json:"user_id"`
	ProjectID      int              `json:"project_id"`
	RemainQuota    billing.MicroUSD `json:"remaining_microusd"`
	UsedQuota      billing.MicroUSD `json:"used_microusd"`
	UnlimitedQuota bool             `json:"unlimited_quota"`
	DeletedAt      gorm.DeletedAt   `json:"deleted_at"`
}

type ledgerOpeningBudget struct {
	Definition  SpendBudget      `json:"definition"`
	ProjectID   int              `json:"project_id"`
	PeriodStart int64            `json:"period_start"`
	PeriodEnd   int64            `json:"period_end"`
	Used        billing.MicroUSD `json:"used_microusd"`
}

func ledgerOpeningDigest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func ledgerOpeningManifestHash(manifest LedgerOpeningManifest) (string, error) {
	manifest.ManifestSHA256 = ""
	manifest.CapturedAt = manifest.CapturedAt.UTC()
	raw, err := json.Marshal(manifest)
	if err != nil {
		return "", err
	}
	return ledgerOpeningDigest(raw), nil
}

// BuildLedgerOpening is an explicit offline operation. The caller must stop all
// writers and perform the financial drain/recovery proof before calling it.
// Its serializable transaction captures the baselines and history together.
// A retry retains the original baseline; replacing an opening is not supported.
func BuildLedgerOpening(ctx context.Context, db *gorm.DB, request LedgerOpeningRequest) (LedgerOpeningManifest, error) {
	var manifest LedgerOpeningManifest
	if db == nil || !request.WritersStopped || strings.TrimSpace(request.Epoch) == "" || len(request.Epoch) > 64 || strings.TrimSpace(request.OperatorEvidence) == "" {
		return manifest, errors.New("ledger opening requires Core SQL, stopped writers, an epoch, and operator evidence")
	}
	if request.CapturedAt.IsZero() {
		request.CapturedAt = time.Now()
	}
	if request.CapturedAt.After(time.Now()) {
		return manifest, errors.New("ledger opening capture time cannot be in the future")
	}
	// PostgreSQL persists microseconds. Hash exactly the timestamp it retains.
	request.CapturedAt = request.CapturedAt.UTC().Truncate(time.Microsecond)
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing LedgerOpeningManifest
		err := tx.Take(&existing, 1).Error
		if err == nil {
			if existing.Epoch != request.Epoch {
				return errors.New("a different ledger opening already exists; an explicit new cutover protocol is required")
			}
			verified, err := verifyLedgerOpeningHistory(tx, LedgerOpeningBinding{existing.ID, existing.ManifestSHA256})
			manifest = verified
			return err
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		manifest = LedgerOpeningManifest{ID: 1, Epoch: request.Epoch, FormatVersion: ledgerOpeningVersion, CapturedAt: request.CapturedAt, OperatorEvidence: request.OperatorEvidence}
		if err := ledgerOpeningVerifyWaivers(tx); err != nil {
			return err
		}
		wallet, err := ledgerOpeningSource(tx, "wallet_ledgers", "id", nil, nil)
		if err != nil {
			return err
		}
		precharges, err := ledgerOpeningSource(tx, "wallet_ledgers", "id", nil, func(q *gorm.DB) *gorm.DB { return q.Where("ref_type = ? AND delta_microusd < 0", "relay_preconsume") })
		if err != nil {
			return err
		}
		manifest.WalletLedgerThroughID, manifest.WalletLedgerCount, manifest.WalletLedgerSHA256 = wallet.ThroughID, wallet.Rows, wallet.SHA256
		manifest.LegacyPrechargeCount, manifest.LegacyPrechargeSHA256 = precharges.Rows, precharges.SHA256
		sources := []ledgerOpeningSourceProof{wallet}
		// These are cutover source watermarks, not assertions that mutable SQL
		// reservations, failures, or budget projections never change later.
		for _, table := range []string{"spend_budget_charges", "project_spends", "spend_budget_reservations", "settlement_failures", "ledger_legacy_settlement_waivers"} {
			if !tx.Migrator().HasTable(table) {
				continue
			}
			primary := "id"
			if table == "ledger_legacy_settlement_waivers" {
				primary = "failure_id"
			}
			proof, err := ledgerOpeningSource(tx, table, primary, nil, nil)
			if err != nil {
				return err
			}
			sources = append(sources, proof)
		}
		sections, err := ledgerOpeningBaselines(tx, manifest.CapturedAt, func(chunk LedgerOpeningChunk) error { return tx.Create(&chunk).Error })
		if err != nil {
			return err
		}
		raw, err := json.Marshal(sections)
		if err != nil {
			return err
		}
		manifest.SectionsJSON, manifest.SnapshotSHA256 = string(raw), ledgerOpeningDigest(raw)
		raw, err = json.Marshal(sources)
		if err != nil {
			return err
		}
		manifest.SourceProofsJSON = string(raw)
		manifest.ManifestSHA256, err = ledgerOpeningManifestHash(manifest)
		if err != nil {
			return err
		}
		return tx.Create(&manifest).Error
	}, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return LedgerOpeningManifest{}, err
	}
	return manifest, nil
}

// LoadLedgerOpening verifies bounded manifest metadata and its permanent Core
// binding. Startup uses this; it deliberately does not scan historical SQL or
// baseline chunks. Explicit cutover/recovery/backup audits must use the full API.
func LoadLedgerOpening(ctx context.Context, db *gorm.DB, binding LedgerOpeningBinding) (LedgerOpeningManifest, error) {
	if db == nil {
		return LedgerOpeningManifest{}, errors.New("core database is unavailable")
	}
	return loadLedgerOpening(db.WithContext(ctx), binding)
}

func loadLedgerOpening(db *gorm.DB, binding LedgerOpeningBinding) (LedgerOpeningManifest, error) {
	var manifest LedgerOpeningManifest
	if binding.OpeningID != 1 || !legacyWaiverValidHash(binding.OpeningSHA256) {
		return manifest, errors.New("invalid permanent ledger opening binding")
	}
	if err := db.Take(&manifest, binding.OpeningID).Error; err != nil {
		return manifest, err
	}
	manifest.CapturedAt = manifest.CapturedAt.UTC()
	computed, err := ledgerOpeningManifestHash(manifest)
	if err != nil {
		return manifest, err
	}
	if manifest.FormatVersion != ledgerOpeningVersion || strings.TrimSpace(manifest.Epoch) == "" || manifest.CapturedAt.IsZero() || computed != binding.OpeningSHA256 || computed != manifest.ManifestSHA256 || ledgerOpeningDigest([]byte(manifest.SectionsJSON)) != manifest.SnapshotSHA256 {
		return manifest, errors.New("ledger opening manifest or its permanent binding changed")
	}
	var sections []ledgerOpeningSection
	if err := json.Unmarshal([]byte(manifest.SectionsJSON), &sections); err != nil {
		return manifest, fmt.Errorf("invalid opening sections: %w", err)
	}
	expected := []string{"wallets", "keys", "budgets"}
	if len(sections) != len(expected) {
		return manifest, errors.New("incomplete opening baseline sections")
	}
	for i, section := range sections {
		if section.Name != expected[i] || section.Rows < 0 || section.Chunks < 0 || !legacyWaiverValidHash(section.SHA256) || (section.Rows == 0) != (section.Chunks == 0) {
			return manifest, errors.New("invalid opening baseline section")
		}
	}
	var sources []ledgerOpeningSourceProof
	if err := json.Unmarshal([]byte(manifest.SourceProofsJSON), &sources); err != nil {
		return manifest, fmt.Errorf("invalid opening source proofs: %w", err)
	}
	if len(sources) == 0 || sources[0].Table != "wallet_ledgers" || sources[0].ThroughID != manifest.WalletLedgerThroughID || sources[0].Rows != manifest.WalletLedgerCount || sources[0].SHA256 != manifest.WalletLedgerSHA256 || !legacyWaiverValidHash(manifest.LegacyPrechargeSHA256) || manifest.LegacyPrechargeCount < 0 {
		return manifest, errors.New("invalid opening historical source proof")
	}
	return manifest, nil
}

// VerifyLedgerOpeningHistory performs bounded-memory, ordered scans. Current
// wallet balances, Key quotas and budgets may legitimately differ from opening.
// Every retained ledger row through the watermark must be unchanged, and ALL
// current legacy precharges must still be precisely the original set. New old-
// writer activity therefore cannot be silently accepted after schema rollback.
func VerifyLedgerOpeningHistory(ctx context.Context, db *gorm.DB, binding LedgerOpeningBinding) (LedgerOpeningManifest, error) {
	var manifest LedgerOpeningManifest
	if db == nil {
		return manifest, errors.New("core database is unavailable")
	}
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		manifest, err = verifyLedgerOpeningHistory(tx, binding)
		return err
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	return manifest, err
}

func verifyLedgerOpeningHistory(tx *gorm.DB, binding LedgerOpeningBinding) (LedgerOpeningManifest, error) {
	manifest, err := loadLedgerOpening(tx, binding)
	if err != nil {
		return manifest, err
	}
	if err := ledgerOpeningVerifyChunks(tx, manifest); err != nil {
		return manifest, err
	}
	wallet, err := ledgerOpeningSource(tx, "wallet_ledgers", "id", &manifest.WalletLedgerThroughID, nil)
	if err != nil {
		return manifest, err
	}
	if wallet.Rows != manifest.WalletLedgerCount || wallet.SHA256 != manifest.WalletLedgerSHA256 {
		return manifest, errors.New("ledger opening historical wallet rows changed or disappeared")
	}
	precharges, err := ledgerOpeningSource(tx, "wallet_ledgers", "id", nil, func(q *gorm.DB) *gorm.DB { return q.Where("ref_type = ? AND delta_microusd < 0", "relay_preconsume") })
	if err != nil {
		return manifest, err
	}
	if precharges.Rows != manifest.LegacyPrechargeCount || precharges.SHA256 != manifest.LegacyPrechargeSHA256 {
		return manifest, errors.New("legacy precharges changed or appeared after ledger opening; an explicit new cutover protocol is required")
	}
	var sources []ledgerOpeningSourceProof
	_ = json.Unmarshal([]byte(manifest.SourceProofsJSON), &sources)
	for _, source := range sources {
		if source.Table != "ledger_legacy_settlement_waivers" {
			continue
		}
		current, err := ledgerOpeningSource(tx, source.Table, source.Primary, nil, nil)
		if err != nil {
			return manifest, err
		}
		if current.Rows != source.Rows || current.SHA256 != source.SHA256 {
			return manifest, errors.New("ledger opening waiver set changed")
		}
		if err := ledgerOpeningVerifyWaivers(tx); err != nil {
			return manifest, err
		}
	}
	return manifest, nil
}

func ledgerOpeningVerifyWaivers(tx *gorm.DB) error {
	var cursor uint
	for {
		var rows []LedgerLegacySettlementWaiver
		if err := tx.Where("failure_id > ?", cursor).Order("failure_id").Limit(ledgerOpeningPageSize).Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		for _, row := range rows {
			if err := verifyLegacySettlementWaiver(tx, row); err != nil {
				return err
			}
			cursor = row.FailureID
		}
	}
}

// VerifyLedgerOpeningBaselines is only for the initial stopped-writer
// activation. Never invoke it during ordinary startup, recovery, or backup:
// those paths must allow subsequently committed financial activity.
func VerifyLedgerOpeningBaselines(ctx context.Context, db *gorm.DB, binding LedgerOpeningBinding) error {
	if db == nil {
		return errors.New("core database is unavailable")
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		manifest, err := verifyLedgerOpeningHistory(tx, binding)
		if err != nil {
			return err
		}
		sections, err := ledgerOpeningBaselines(tx, manifest.CapturedAt, nil)
		if err != nil {
			return err
		}
		raw, err := json.Marshal(sections)
		if err != nil {
			return err
		}
		if ledgerOpeningDigest(raw) != manifest.SnapshotSHA256 {
			return errors.New("SQL financial baselines changed before initial ledger opening activation")
		}
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
}

func ledgerOpeningBaselines(tx *gorm.DB, at time.Time, save func(LedgerOpeningChunk) error) ([]ledgerOpeningSection, error) {
	var active int64
	if err := tx.Model(&SpendBudgetReservation{}).Where("(expires_at <= 0 OR expires_at > ?) AND reserved_microusd > 0", at.Unix()).Count(&active).Error; err != nil {
		return nil, err
	}
	if active != 0 {
		return nil, fmt.Errorf("%d effective legacy SQL budget reservations remain; drain them before ledger opening", active)
	}
	sections := []ledgerOpeningSection{}
	for _, name := range []string{"wallets", "keys", "budgets"} {
		section := ledgerOpeningSection{Name: name}
		digest := sha256.New()
		cursor := uint(0)
		for {
			var raw []byte
			var count int
			var encodeErr error
			switch name {
			case "wallets":
				var rows []ledgerOpeningWallet
				if err := tx.Table("users").Select("id,quota,credit_seq").Where("id > ?", cursor).Order("id").Limit(ledgerOpeningPageSize).Scan(&rows).Error; err != nil {
					return nil, err
				}
				count = len(rows)
				if count > 0 {
					cursor = uint(rows[count-1].ID)
				}
				raw, encodeErr = ledgerOpeningRowsJSON(rows)
			case "keys":
				var rows []ledgerOpeningKey
				if err := tx.Table("tokens").Select("id,user_id,project_id,remain_quota,used_quota,unlimited_quota,deleted_at").Where("id > ?", cursor).Order("id").Limit(ledgerOpeningPageSize).Scan(&rows).Error; err != nil {
					return nil, err
				}
				count = len(rows)
				if count > 0 {
					cursor = uint(rows[count-1].ID)
				}
				for i := range rows {
					if rows[i].DeletedAt.Valid {
						rows[i].DeletedAt.Time = rows[i].DeletedAt.Time.UTC()
					}
				}
				raw, encodeErr = ledgerOpeningRowsJSON(rows)
			case "budgets":
				var definitions []SpendBudget
				if err := tx.Where("id > ?", cursor).Order("id").Limit(ledgerOpeningPageSize).Find(&definitions).Error; err != nil {
					return nil, err
				}
				used, err := readSpendBudgetUsedBatchWithDB(tx, definitions, at)
				if err != nil {
					return nil, err
				}
				rows := make([]ledgerOpeningBudget, 0, len(definitions))
				for _, definition := range definitions {
					start, end := spendBudgetPeriodWindow(definition.Period, at)
					definition.CreatedAt, definition.UpdatedAt = definition.CreatedAt.UTC(), definition.UpdatedAt.UTC()
					rows = append(rows, ledgerOpeningBudget{definition, definition.ProjectID, start, end, used[definition.ID]})
				}
				count = len(rows)
				if count > 0 {
					cursor = definitions[count-1].ID
				}
				raw, encodeErr = ledgerOpeningRowsJSON(rows)
			}
			if encodeErr != nil {
				return nil, encodeErr
			}
			if count == 0 {
				break
			}
			section.Chunks++
			section.Rows += int64(count)
			digest.Write(raw)
			if save != nil {
				if err := save(LedgerOpeningChunk{OpeningID: 1, Section: name, Sequence: section.Chunks, RowCount: int64(count), Payload: string(raw), SHA256: ledgerOpeningDigest(raw)}); err != nil {
					return nil, err
				}
			}
		}
		section.SHA256 = hex.EncodeToString(digest.Sum(nil))
		sections = append(sections, section)
	}
	return sections, nil
}

func ledgerOpeningRowsJSON[T any](rows []T) ([]byte, error) {
	var result []byte
	for _, row := range rows {
		raw, err := json.Marshal(row)
		if err != nil {
			return nil, err
		}
		result = append(result, raw...)
		result = append(result, '\n')
	}
	return result, nil
}

func ledgerOpeningVerifyChunks(tx *gorm.DB, manifest LedgerOpeningManifest) error {
	var sections []ledgerOpeningSection
	_ = json.Unmarshal([]byte(manifest.SectionsJSON), &sections)
	var total int64
	for _, section := range sections {
		digest := sha256.New()
		var count int64
		sequence := 0
		for {
			var chunks []LedgerOpeningChunk
			if err := tx.Where("opening_id = ? AND section = ? AND sequence > ?", manifest.ID, section.Name, sequence).Order("sequence").Limit(ledgerOpeningPageSize).Find(&chunks).Error; err != nil {
				return err
			}
			if len(chunks) == 0 {
				break
			}
			for _, chunk := range chunks {
				if chunk.Sequence != sequence+1 || chunk.RowCount <= 0 || chunk.RowCount > ledgerOpeningPageSize || int64(strings.Count(chunk.Payload, "\n")) != chunk.RowCount || !strings.HasSuffix(chunk.Payload, "\n") || ledgerOpeningDigest([]byte(chunk.Payload)) != chunk.SHA256 {
					return errors.New("ledger opening snapshot chunk changed")
				}
				digest.Write([]byte(chunk.Payload))
				count += chunk.RowCount
				sequence = chunk.Sequence
			}
		}
		if sequence != section.Chunks || count != section.Rows || hex.EncodeToString(digest.Sum(nil)) != section.SHA256 {
			return errors.New("ledger opening snapshot is incomplete or changed")
		}
		total += int64(sequence)
	}
	var actual int64
	if err := tx.Model(&LedgerOpeningChunk{}).Where("opening_id = ?", manifest.ID).Count(&actual).Error; err != nil {
		return err
	}
	if actual != total {
		return errors.New("unexpected ledger opening snapshot chunks")
	}
	return nil
}

// Hash every persisted source column, including NULL, event IDs and metadata.
// Numeric primary-key pagination is O(n), bounded in memory, and never uses
// OFFSET or a per-precharge lookup. Only the resulting commitment is retained.
func ledgerOpeningSource(tx *gorm.DB, table, primary string, through *uint, filter func(*gorm.DB) *gorm.DB) (ledgerOpeningSourceProof, error) {
	proof := ledgerOpeningSourceProof{Table: table, Primary: primary}
	columns, err := tx.Migrator().ColumnTypes(table)
	if err != nil {
		return proof, err
	}
	for _, column := range columns {
		proof.Columns = append(proof.Columns, column.Name())
	}
	sort.Strings(proof.Columns)
	index := -1
	quoted := make([]string, len(proof.Columns))
	for i, name := range proof.Columns {
		quoted[i] = `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
		if name == primary {
			index = i
		}
	}
	if index < 0 {
		return proof, fmt.Errorf("opening source %s has no %s primary key", table, primary)
	}
	base := tx.Table(table)
	if filter != nil {
		base = filter(base)
	}
	if through != nil {
		proof.ThroughID = *through
	} else if err := base.Select("COALESCE(MAX(" + primary + "),0)").Scan(&proof.ThroughID).Error; err != nil {
		return proof, err
	}
	digest := sha256.New()
	header, _ := json.Marshal(proof.Columns)
	digest.Write(header)
	digest.Write([]byte{'\n'})
	var cursor uint
	first := true
	for {
		query := tx.Table(table).Where(primary+" <= ?", proof.ThroughID)
		if filter != nil {
			query = filter(query)
		}
		if !first {
			query = query.Where(primary+" > ?", cursor)
		}
		rows, err := query.Select(strings.Join(quoted, ",")).Order(primary).Limit(ledgerOpeningPageSize).Rows()
		if err != nil {
			return proof, err
		}
		count := 0
		for rows.Next() {
			values := make([]any, len(proof.Columns))
			dest := make([]any, len(values))
			for i := range values {
				dest[i] = &values[i]
			}
			if err := rows.Scan(dest...); err != nil {
				rows.Close()
				return proof, err
			}
			next, err := ledgerOpeningPrimary(values[index])
			if err != nil || next == 0 || (!first && next <= cursor) {
				rows.Close()
				return proof, fmt.Errorf("invalid opening source %s primary key", table)
			}
			cursor, first = next, false
			if err := ledgerOpeningHashValues(digest, values); err != nil {
				rows.Close()
				return proof, err
			}
			count++
			proof.Rows++
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return proof, err
		}
		if count < ledgerOpeningPageSize {
			break
		}
	}
	proof.SHA256 = hex.EncodeToString(digest.Sum(nil))
	return proof, nil
}

func ledgerOpeningPrimary(value any) (uint, error) {
	var raw string
	switch v := value.(type) {
	case int64:
		raw = strconv.FormatInt(v, 10)
	case int:
		raw = strconv.Itoa(v)
	case uint64:
		raw = strconv.FormatUint(v, 10)
	case []byte:
		raw = string(v)
	case string:
		raw = v
	default:
		return 0, errors.New("invalid primary-key type")
	}
	number, err := strconv.ParseUint(raw, 10, strconv.IntSize)
	return uint(number), err
}

func ledgerOpeningHashValues(digest hash.Hash, values []any) error {
	for i, value := range values {
		switch v := value.(type) {
		case []byte:
			values[i] = string(v)
		case time.Time:
			values[i] = v.UTC().Format(time.RFC3339Nano)
		}
	}
	raw, err := json.Marshal(values)
	if err != nil {
		return err
	}
	digest.Write(raw)
	digest.Write([]byte{'\n'})
	return nil
}

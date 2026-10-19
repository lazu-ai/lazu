package model

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// This is an audit record, never an instruction to debit or refund a wallet.
// Automatic evidence contains only selected financial facts and stable links;
// retained request metadata (including token_key) is never copied into it.
type LedgerLegacyPrechargeRecord struct {
	LedgerID        uint      `gorm:"primaryKey;autoIncrement:false" json:"ledger_id"`
	UserID          int       `gorm:"not null" json:"user_id"`
	AmountMicrousd  int64     `gorm:"not null" json:"amount_microusd"`
	Status          string    `gorm:"size:32;not null" json:"status"`
	Source          string    `gorm:"size:16;not null" json:"source"`
	PrechargeSHA256 string    `gorm:"size:64;not null" json:"precharge_sha256"`
	Evidence        string    `gorm:"type:text;not null" json:"evidence"`
	Classifier      string    `gorm:"size:32;not null" json:"classifier"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type LegacyPrechargeClassificationReport struct {
	Processed         int64  `json:"processed"`
	Settled           int64  `json:"settled"`
	Refunded          int64  `json:"refunded"`
	ManualReview      int64  `json:"manual_review"`
	OperatorPreserved int64  `json:"operator_preserved"`
	ManualLedgerIDs   []uint `json:"manual_ledger_ids,omitempty"`
}

type legacyPrechargeMetadata struct {
	RequestID        string      `json:"request_id"`
	PrechargeLedger  json.Number `json:"precharge_ledger_id"`
	PrechargeRef     string      `json:"precharge_ref_id"`
	PrechargeEvent   string      `json:"precharge_event_id"`
	PreConsumedQuota json.Number `json:"pre_consumed_quota"`
}
type legacyStableLink struct{ Kind, Value string }
type legacyPrechargeLink struct {
	LedgerID            uint
	UserID              int
	LinkKind, LinkValue string
}
type legacyResolutionFact struct {
	SourceID    uint
	SourceTable string
	UserID      int
	RefType     string
	RefID       string
	Status      string
	QuotaDelta  int64
	PreConsumed int64
	LinksJSON   string
}
type legacyTerminalFact struct {
	WalletLedgerID uint
	UserID         int
	RefType        string
	RefID          string
	DeltaMicrousd  int64
	PreConsumed    int64
	LinkKind       string
	LinkValue      string
	BlockingReason string
	Evidence       string
}
type legacyWalletEvidence struct {
	WalletLedgerID uint                   `json:"wallet_ledger_id"`
	UserID         int                    `json:"user_id"`
	RefType        string                 `json:"ref_type"`
	RefID          string                 `json:"ref_id"`
	DeltaMicrousd  int64                  `json:"delta_microusd"`
	PreConsumed    int64                  `json:"pre_consumed_microusd"`
	Links          []legacyStableLink     `json:"stable_links"`
	Resolutions    []legacyResolutionFact `json:"resolution_facts,omitempty"`
}
type legacyClassificationEvidence struct {
	Reason           string   `json:"reason"`
	PrechargeSHA256  string   `json:"precharge_sha256"`
	WalletFacts      []string `json:"wallet_facts,omitempty"`
	OperatorEvidence string   `json:"operator_evidence,omitempty"`
}

func legacyMetadata(value string) legacyPrechargeMetadata {
	var metadata legacyPrechargeMetadata
	if json.Unmarshal([]byte(value), &metadata) != nil {
		return legacyPrechargeMetadata{}
	}
	return metadata
}
func legacyLinks(metadata legacyPrechargeMetadata, requestID string) []legacyStableLink {
	links := []legacyStableLink{}
	if id, err := metadata.PrechargeLedger.Int64(); err == nil && id > 0 {
		links = append(links, legacyStableLink{"ledger_id", strconv.FormatInt(id, 10)})
	}
	for _, link := range []legacyStableLink{{"ref_id", metadata.PrechargeRef}, {"event_id", metadata.PrechargeEvent}, {"request_id", metadata.RequestID}, {"request_id", requestID}} {
		if link.Value != "" {
			duplicate := false
			for _, prior := range links {
				duplicate = duplicate || prior == link
			}
			if !duplicate {
				links = append(links, link)
			}
		}
	}
	return links
}
func legacyPrechargeIdentity(row WalletLedger) (string, int64) {
	metadata := legacyMetadata(row.Metadata)
	raw, _ := json.Marshal(struct {
		LedgerID                uint
		UserID                  int
		Delta                   int64
		RefType, RefID, EventID string
		CreatedAt               time.Time
		RequestID               string
	}{row.ID, row.UserID, row.DeltaMicrousd.Int64(), row.RefType, row.RefID, row.EventID, row.CreatedAt.UTC(), metadata.RequestID})
	digest := sha256.Sum256(raw)
	amount := int64(0)
	if row.DeltaMicrousd.Int64() != math.MinInt64 {
		amount = -row.DeltaMicrousd.Int64()
	}
	return hex.EncodeToString(digest[:]), amount
}

// The CLI runs this with writers stopped. Fact inventories use primary-key
// cursors. Temporary stable-link indexes keep each
// precharge lookup bounded; unrelated same-user historical rows are never
// rescanned per precharge. Only the resulting classification is durable.
func ClassifyLegacyPrecharges(ctx context.Context, db *gorm.DB, batchSize int) (LegacyPrechargeClassificationReport, error) {
	report := LegacyPrechargeClassificationReport{}
	if db == nil {
		return report, errors.New("core database is unavailable")
	}
	if batchSize <= 0 || batchSize > 1000 {
		batchSize = 500
	}
	if err := db.WithContext(ctx).AutoMigrate(&LedgerLegacyPrechargeRecord{}); err != nil {
		return report, err
	}
	err := db.WithContext(ctx).Connection(func(connection *gorm.DB) error {
		// Connection returns an initialized session. Use fresh statements for
		// each source/index model while retaining its pinned SQL connection.
		connection = connection.Session(&gorm.Session{NewDB: true})
		suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
		resolutions, terminals, precharges := "legacy_resolutions_"+suffix, "legacy_terminals_"+suffix, "legacy_precharges_"+suffix
		if err := connection.Exec("CREATE TEMP TABLE " + resolutions + " (source_id bigint,source_table text,user_id bigint,ref_type text,ref_id text,status text,quota_delta bigint,pre_consumed bigint,links_json text)").Error; err != nil {
			return err
		}
		defer connection.WithContext(context.Background()).Exec("DROP TABLE " + resolutions)
		if err := connection.Exec("CREATE INDEX " + resolutions + "_ref ON " + resolutions + " (user_id,ref_type,ref_id)").Error; err != nil {
			return err
		}
		if err := connection.Exec("CREATE TEMP TABLE " + terminals + " (wallet_ledger_id bigint,user_id bigint,ref_type text,ref_id text,delta_microusd bigint,pre_consumed bigint,link_kind text,link_value text,blocking_reason text,evidence text)").Error; err != nil {
			return err
		}
		defer connection.WithContext(context.Background()).Exec("DROP TABLE " + terminals)
		if err := connection.Exec("CREATE INDEX " + terminals + "_link ON " + terminals + " (user_id,link_kind,link_value)").Error; err != nil {
			return err
		}
		if err := connection.Exec("CREATE TEMP TABLE " + precharges + " (ledger_id bigint,user_id bigint,link_kind text,link_value text)").Error; err != nil {
			return err
		}
		defer connection.WithContext(context.Background()).Exec("DROP TABLE " + precharges)
		if err := connection.Exec("CREATE INDEX " + precharges + "_link ON " + precharges + " (user_id,link_kind,link_value)").Error; err != nil {
			return err
		}
		if err := indexLegacyPrechargeLinks(connection, precharges, batchSize); err != nil {
			return err
		}
		if err := indexLegacyResolutionFacts(connection, resolutions, batchSize); err != nil {
			return err
		}
		if err := indexLegacyTerminalFacts(connection, resolutions, terminals, batchSize); err != nil {
			return err
		}
		cursor := uint(0)
		for {
			var rows []WalletLedger
			if err := connection.Where("id > ? AND ref_type = ? AND delta_microusd < 0", cursor, "relay_preconsume").Order("id").Limit(batchSize).Find(&rows).Error; err != nil {
				return err
			}
			if len(rows) == 0 {
				break
			}
			if err := connection.Transaction(func(tx *gorm.DB) error {
				ids := make([]uint, 0, len(rows))
				for _, row := range rows {
					ids = append(ids, row.ID)
				}
				var retained []LedgerLegacyPrechargeRecord
				if err := tx.Where("ledger_id IN ?", ids).Find(&retained).Error; err != nil {
					return err
				}
				existingByID := map[uint]LedgerLegacyPrechargeRecord{}
				for _, record := range retained {
					existingByID[record.LedgerID] = record
				}
				updates := make([]LedgerLegacyPrechargeRecord, 0, len(rows))
				for _, row := range rows {
					fingerprint, amount := legacyPrechargeIdentity(row)
					existing := existingByID[row.ID]
					record := existing
					if existing.Source == "operator" {
						if existing.PrechargeSHA256 != fingerprint || existing.AmountMicrousd != amount {
							return fmt.Errorf("operator classification source changed for precharge %d", row.ID)
						}
						report.OperatorPreserved++
					} else {
						var err error
						record, err = classifyLegacyPrecharge(tx, terminals, precharges, row, fingerprint, amount)
						if err != nil {
							return err
						}
						updates = append(updates, record)
					}
					report.Processed++
					switch record.Status {
					case "settled":
						report.Settled++
					case "refunded":
						report.Refunded++
					default:
						report.ManualReview++
						if len(report.ManualLedgerIDs) < 100 {
							report.ManualLedgerIDs = append(report.ManualLedgerIDs, row.ID)
						}
					}
				}
				if len(updates) > 0 {
					return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "ledger_id"}}, DoUpdates: clause.AssignmentColumns([]string{"user_id", "amount_microusd", "status", "source", "precharge_sha256", "evidence", "classifier", "updated_at"}), Where: clause.Where{Exprs: []clause.Expression{gorm.Expr("ledger_legacy_precharge_records.source <> 'operator'")}}}).Create(&updates).Error
				}
				return nil
			}); err != nil {
				return err
			}
			cursor = rows[len(rows)-1].ID
		}
		return nil
	})
	return report, err
}

func indexLegacyPrechargeLinks(db *gorm.DB, table string, batchSize int) error {
	cursor := uint(0)
	for {
		var rows []WalletLedger
		if err := db.Where("id > ? AND ref_type = ? AND delta_microusd < 0", cursor, "relay_preconsume").Order("id").Limit(batchSize).Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		links := []legacyPrechargeLink{}
		for _, row := range rows {
			for _, link := range []legacyStableLink{{"request_id", legacyMetadata(row.Metadata).RequestID}, {"event_id", row.EventID}} {
				if link.Value != "" {
					links = append(links, legacyPrechargeLink{row.ID, row.UserID, link.Kind, link.Value})
				}
			}
		}
		if len(links) > 0 {
			if err := db.Table(table).CreateInBatches(&links, batchSize).Error; err != nil {
				return err
			}
		}
		cursor = rows[len(rows)-1].ID
	}
}

func indexLegacyResolutionFacts(db *gorm.DB, table string, batchSize int) error {
	for _, source := range []string{"settlement_failures", "settlement_outboxes"} {
		if !db.Migrator().HasTable(source) {
			continue
		}
		cursor := uint(0)
		for {
			var rows []SettlementFailure // the selected fields are shared with outbox
			if err := db.Table(source).Select("id,user_id,request_id,ref_type,ref_id,status,quota_delta,pre_consumed,metadata").Where("id > ?", cursor).Order("id").Limit(batchSize).Find(&rows).Error; err != nil {
				return err
			}
			if len(rows) == 0 {
				break
			}
			facts := make([]legacyResolutionFact, 0, len(rows))
			for _, row := range rows {
				links, _ := json.Marshal(legacyLinks(legacyMetadata(row.Metadata), row.RequestID))
				facts = append(facts, legacyResolutionFact{row.ID, source, row.UserID, row.RefType, row.RefID, row.Status, row.QuotaDelta.Int64(), row.PreConsumed.Int64(), string(links)})
			}
			if err := db.Table(table).Create(&facts).Error; err != nil {
				return err
			}
			cursor = rows[len(rows)-1].ID
		}
	}
	return nil
}

func indexLegacyTerminalFacts(db *gorm.DB, resolutions, table string, batchSize int) error {
	cursor := uint(0)
	for {
		var rows []WalletLedger
		if err := db.Where("id > ? AND ref_type IN ?", cursor, []string{"relay_settlement", "relay_refund"}).Order("id").Limit(batchSize).Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		facts := []legacyTerminalFact{}
		for _, row := range rows {
			metadata := legacyMetadata(row.Metadata)
			links := legacyLinks(metadata, "")
			preConsumed, _ := metadata.PreConsumedQuota.Int64()
			var controls []legacyResolutionFact
			if err := db.Table(resolutions).Where("user_id = ? AND ref_type = ? AND ref_id = ?", row.UserID, row.RefType, row.RefID).Order("source_table,source_id").Find(&controls).Error; err != nil {
				return err
			}
			blocking := ""
			for _, control := range controls {
				var additional []legacyStableLink
				_ = json.Unmarshal([]byte(control.LinksJSON), &additional)
				links = append(links, additional...)
				if control.Status != SettlementFailureStatusResolved {
					blocking = "unresolved_matching_settlement"
				}
				if control.QuotaDelta == math.MinInt64 || row.DeltaMicrousd.Int64() != -control.QuotaDelta {
					blocking = "settlement_wallet_delta_mismatch"
				}
				if preConsumed == 0 {
					preConsumed = control.PreConsumed
				} else if control.PreConsumed != preConsumed {
					blocking = "conflicting_precharge_amount"
				}
			}
			evidence, _ := json.Marshal(legacyWalletEvidence{row.ID, row.UserID, row.RefType, row.RefID, row.DeltaMicrousd.Int64(), preConsumed, links, controls})
			seen := map[legacyStableLink]bool{}
			for _, link := range links {
				if seen[link] {
					continue
				}
				seen[link] = true
				facts = append(facts, legacyTerminalFact{row.ID, row.UserID, row.RefType, row.RefID, row.DeltaMicrousd.Int64(), preConsumed, link.Kind, link.Value, blocking, string(evidence)})
			}
		}
		if len(facts) > 0 {
			if err := db.Table(table).CreateInBatches(&facts, batchSize).Error; err != nil {
				return err
			}
		}
		cursor = rows[len(rows)-1].ID
	}
}

func classifyLegacyPrecharge(db *gorm.DB, table, precharges string, row WalletLedger, fingerprint string, amount int64) (LedgerLegacyPrechargeRecord, error) {
	links := []legacyStableLink{{"ledger_id", strconv.FormatUint(uint64(row.ID), 10)}, {"ref_id", row.RefID}}
	if requestID := legacyMetadata(row.Metadata).RequestID; requestID != "" {
		links = append(links, legacyStableLink{"request_id", requestID})
	}
	if row.EventID != "" {
		links = append(links, legacyStableLink{"event_id", row.EventID})
	}
	conditions, parameters := []string{}, []interface{}{row.UserID}
	for _, link := range links {
		if link.Value != "" {
			conditions = append(conditions, "(link_kind = ? AND link_value = ?)")
			parameters = append(parameters, link.Kind, link.Value)
		}
	}
	var matches []legacyTerminalFact
	if err := db.Table(table).Where("user_id = ? AND ("+strings.Join(conditions, " OR ")+")", parameters...).Order("wallet_ledger_id,link_kind,link_value").Find(&matches).Error; err != nil {
		return LedgerLegacyPrechargeRecord{}, err
	}
	unique := map[uint]legacyTerminalFact{}
	evidence := legacyClassificationEvidence{Reason: "ambiguous_no_stable_terminal_fact", PrechargeSHA256: fingerprint}
	for _, match := range matches {
		if _, exists := unique[match.WalletLedgerID]; !exists {
			unique[match.WalletLedgerID] = match
			evidence.WalletFacts = append(evidence.WalletFacts, match.Evidence)
		}
	}
	status := "manual_review"
	if len(unique) > 1 {
		evidence.Reason = "multiple_matching_terminal_facts"
	} else if len(unique) == 1 && amount > 0 {
		for _, match := range unique {
			var walletEvidence legacyWalletEvidence
			if json.Unmarshal([]byte(match.Evidence), &walletEvidence) != nil {
				return LedgerLegacyPrechargeRecord{}, errors.New("invalid indexed legacy evidence")
			}
			strongLink := false
			for _, link := range walletEvidence.Links {
				for _, expected := range links {
					if expected.Kind == link.Kind && expected.Value != link.Value {
						match.BlockingReason = "conflicting_stable_link"
					}
					if expected == link && (link.Kind == "ledger_id" || link.Kind == "ref_id") {
						strongLink = true
					}
				}
			}
			if !strongLink {
				for _, link := range walletEvidence.Links {
					if link.Kind != "request_id" && link.Kind != "event_id" {
						continue
					}
					var associated int64
					if err := db.Table(precharges).Where("user_id = ? AND link_kind = ? AND link_value = ?", row.UserID, link.Kind, link.Value).Distinct("ledger_id").Count(&associated).Error; err != nil {
						return LedgerLegacyPrechargeRecord{}, err
					}
					if associated > 1 {
						match.BlockingReason = "ambiguous_duplicate_precharge_identity"
					}
				}
			}
			switch {
			case match.BlockingReason != "":
				evidence.Reason = match.BlockingReason
			case match.PreConsumed != amount:
				evidence.Reason = "precharge_amount_mismatch"
			case match.DeltaMicrousd > amount:
				evidence.Reason = "refund_exceeds_precharge"
			case match.DeltaMicrousd == amount:
				status, evidence.Reason = "refunded", "stable_link_completed_refund"
			case match.RefType == "relay_settlement":
				status, evidence.Reason = "settled", "stable_link_completed_settlement"
			default:
				evidence.Reason = "incomplete_refund"
			}
		}
	}
	raw, _ := json.Marshal(evidence)
	return LedgerLegacyPrechargeRecord{row.ID, row.UserID, amount, status, "automatic", fingerprint, string(raw), "stable-link-v1", time.Now().UTC()}, nil
}

// Explicit operator imports fill ambiguity the original schema cannot resolve.
// Correspondence is checked against the exact immutable debit and duplicate
// input IDs are rejected. Automatic runs never replace operator evidence.
func ImportLegacyPrechargeClassifications(ctx context.Context, db *gorm.DB, proof []LedgerLegacyPrechargeClassification) error {
	if db == nil {
		return errors.New("core database is unavailable")
	}
	if err := db.WithContext(ctx).AutoMigrate(&LedgerLegacyPrechargeRecord{}); err != nil {
		return err
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		seen := map[uint]bool{}
		for _, item := range proof {
			if seen[item.LedgerID] {
				return errors.New("duplicate legacy precharge classification")
			}
			seen[item.LedgerID] = true
			if item.LedgerID == 0 || item.AmountMicrousd <= 0 || strings.TrimSpace(item.Evidence) == "" || (item.Status != "settled" && item.Status != "refunded") {
				return errors.New("operator evidence requires an exact debit amount and settled/refunded status")
			}
			var row WalletLedger
			if err := tx.Where("id = ? AND ref_type = ? AND delta_microusd < 0", item.LedgerID, "relay_preconsume").First(&row).Error; err != nil {
				return fmt.Errorf("operator classification precharge %d does not exist", item.LedgerID)
			}
			fingerprint, amount := legacyPrechargeIdentity(row)
			if amount != item.AmountMicrousd {
				return fmt.Errorf("operator classification amount differs for precharge %d", item.LedgerID)
			}
			evidence, _ := json.Marshal(legacyClassificationEvidence{Reason: "explicit_operator_classification", PrechargeSHA256: fingerprint, OperatorEvidence: item.Evidence})
			var existing LedgerLegacyPrechargeRecord
			err := tx.Where("ledger_id = ?", item.LedgerID).Limit(1).Find(&existing).Error
			if err != nil {
				return err
			}
			if existing.Source == "operator" && (existing.PrechargeSHA256 != fingerprint || existing.Status != item.Status || existing.Evidence != string(evidence)) {
				return fmt.Errorf("conflicting retained operator classification for precharge %d", item.LedgerID)
			}
			record := LedgerLegacyPrechargeRecord{item.LedgerID, row.UserID, amount, item.Status, "operator", fingerprint, string(evidence), "operator-v1", time.Now().UTC()}
			if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "ledger_id"}}, UpdateAll: true}).Create(&record).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func LoadLegacyPrechargeClassifications(ctx context.Context, db *gorm.DB) ([]LedgerLegacyPrechargeClassification, error) {
	proof := []LedgerLegacyPrechargeClassification{}
	cursor := uint(0)
	for {
		var rows []WalletLedger
		if err := db.WithContext(ctx).Where("id > ? AND ref_type = ? AND delta_microusd < 0", cursor, "relay_preconsume").Order("id").Limit(500).Find(&rows).Error; err != nil {
			return nil, err
		}
		if len(rows) == 0 {
			return proof, nil
		}
		if !db.Migrator().HasTable(&LedgerLegacyPrechargeRecord{}) {
			return nil, errors.New("legacy precharges have no durable classification; run precharge-classify and import operator evidence for ambiguous rows")
		}
		for _, row := range rows {
			var record LedgerLegacyPrechargeRecord
			if err := db.WithContext(ctx).First(&record, "ledger_id = ?", row.ID).Error; err != nil {
				return nil, fmt.Errorf("precharge %d has no durable classification", row.ID)
			}
			fingerprint, amount := legacyPrechargeIdentity(row)
			if amount <= 0 || record.PrechargeSHA256 != fingerprint || record.AmountMicrousd != amount || record.UserID != row.UserID || (record.Status != "settled" && record.Status != "refunded") || record.Evidence == "" || (record.Source != "automatic" && record.Source != "operator") {
				return nil, fmt.Errorf("precharge %d is ambiguous or its retained classification differs from the source debit", row.ID)
			}
			if record.Source == "automatic" {
				if err := verifyRetainedLegacyTerminalFacts(ctx, db, record); err != nil {
					return nil, err
				}
			}
			proof = append(proof, LedgerLegacyPrechargeClassification{row.ID, amount, record.Status, record.Evidence})
		}
		cursor = rows[len(rows)-1].ID
	}
}

func verifyRetainedLegacyTerminalFacts(ctx context.Context, db *gorm.DB, record LedgerLegacyPrechargeRecord) error {
	var evidence legacyClassificationEvidence
	if err := json.Unmarshal([]byte(record.Evidence), &evidence); err != nil || len(evidence.WalletFacts) != 1 {
		return fmt.Errorf("precharge %d has invalid automatic terminal evidence", record.LedgerID)
	}
	var expected legacyWalletEvidence
	if err := json.Unmarshal([]byte(evidence.WalletFacts[0]), &expected); err != nil {
		return err
	}
	var wallet WalletLedger
	if err := db.WithContext(ctx).First(&wallet, expected.WalletLedgerID).Error; err != nil {
		return fmt.Errorf("precharge %d terminal wallet fact is missing", record.LedgerID)
	}
	metadata := legacyMetadata(wallet.Metadata)
	preConsumed, _ := metadata.PreConsumedQuota.Int64()
	links := legacyLinks(metadata, "")
	controls := []legacyResolutionFact{}
	for _, source := range []string{"settlement_failures", "settlement_outboxes"} {
		if !db.Migrator().HasTable(source) {
			continue
		}
		var rows []SettlementFailure
		if err := db.WithContext(ctx).Table(source).Select("id,user_id,request_id,ref_type,ref_id,status,quota_delta,pre_consumed,metadata").Where("user_id = ? AND ref_type = ? AND ref_id = ?", wallet.UserID, wallet.RefType, wallet.RefID).Order("id").Find(&rows).Error; err != nil {
			return err
		}
		for _, row := range rows {
			additional := legacyLinks(legacyMetadata(row.Metadata), row.RequestID)
			links = append(links, additional...)
			linkJSON, _ := json.Marshal(additional)
			controls = append(controls, legacyResolutionFact{row.ID, source, row.UserID, row.RefType, row.RefID, row.Status, row.QuotaDelta.Int64(), row.PreConsumed.Int64(), string(linkJSON)})
			if preConsumed == 0 {
				preConsumed = row.PreConsumed.Int64()
			}
		}
	}
	// Compare selected current financial facts with the durable audit snapshot.
	// No full request metadata or credentials enter either representation.
	current, _ := json.Marshal(legacyWalletEvidence{wallet.ID, wallet.UserID, wallet.RefType, wallet.RefID, wallet.DeltaMicrousd.Int64(), preConsumed, links, controls})
	if string(current) != evidence.WalletFacts[0] {
		return fmt.Errorf("precharge %d terminal facts changed since classification; rerun precharge-classify", record.LedgerID)
	}
	return nil
}

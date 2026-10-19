package model

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func OpenMigrationDatabase(dsn string) (*gorm.DB, error) {
	dsn = strings.TrimSpace(dsn)
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		return gorm.Open(safePostgresDialector{postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true}).(*postgres.Dialector)}, gormOpenConfig())
	}
	if strings.HasPrefix(dsn, "sqlite://") {
		return gorm.Open(sqlite.Open(strings.TrimPrefix(dsn, "sqlite://")), gormOpenConfig())
	}
	return nil, errors.New("migration database URL must be a PostgreSQL URL or an explicit sqlite:// path")
}

type MigrationTableProof struct {
	Table  string `json:"table"`
	Rows   int64  `json:"rows"`
	SHA256 string `json:"sha256"`
}
type MigrationMoneyProof struct {
	WalletTotal        int64  `json:"wallet_total_microusd"`
	KeyRemainingTotal  int64  `json:"key_remaining_total_microusd"`
	LedgerByUserSHA256 string `json:"ledger_by_user_sha256"`
	ProjectMonthSHA256 string `json:"project_month_sha256"`
}
type MigrationProof struct {
	Tables []MigrationTableProof `json:"tables"`
	Money  *MigrationMoneyProof  `json:"money,omitempty"`
}

func MigrationTableInventory(db *gorm.DB, scope string) ([]string, error) {
	if scope != "core" && scope != "telemetry" {
		return nil, errors.New("scope must be core or telemetry")
	}
	tables, err := db.Migrator().GetTables()
	if err != nil {
		return nil, err
	}
	if db.Dialector.Name() == "postgres" {
		// GetTables only returns physical tables. Reader-visible routing facts
		// also need proof after their names become compatibility views.
		var views []string
		if err := db.Raw("SELECT relname FROM pg_class WHERE relnamespace = current_schema()::regnamespace AND relkind = 'v' AND relname IN ('routing_decision','routing_outcome')").Scan(&views).Error; err != nil {
			return nil, err
		}
		tables = append(tables, views...)
	}
	telemetry := make(map[string]bool, len(TelemetryTables))
	for _, table := range TelemetryTables {
		telemetry[table] = true
	}
	selected := []string{}
	for _, table := range tables {
		if strings.HasPrefix(table, "sqlite_") || strings.HasPrefix(table, "pg_") {
			continue
		}
		isRoutingHistory := table == "routing_decision_legacy" || table == "routing_outcome_legacy"
		isTelemetry := telemetry[table] || strings.HasPrefix(table, "routing_decision_") || strings.HasPrefix(table, "routing_outcome_") || strings.HasPrefix(table, "logs_") || strings.HasPrefix(table, "routing_attempt_outcome_") || strings.HasPrefix(table, "channel_probe_result_")
		// Parent partition counts include their children. Scratch originals are
		// intentionally checked separately by the partition-audit command.
		// Raw legacy facts remain independently verifiable even when a newer
		// log projection hides them from the reader-visible routing view.
		if isTelemetry && !telemetry[table] && !isRoutingHistory {
			continue
		}
		if (scope == "telemetry" && isTelemetry) || (scope == "core" && !isTelemetry) {
			selected = append(selected, table)
		}
	}
	sort.Strings(selected)
	return selected, nil
}
func BuildMigrationProof(ctx context.Context, db *gorm.DB, scope string) (MigrationProof, error) {
	var proof MigrationProof
	tables, err := MigrationTableInventory(db, scope)
	if err != nil {
		return proof, err
	}
	for _, table := range tables {
		columns, err := db.Migrator().ColumnTypes(table)
		if err != nil {
			return proof, err
		}
		names := []string{}
		order := []string{}
		for _, column := range columns {
			names = append(names, column.Name())
			if pk, ok := column.PrimaryKey(); ok && pk {
				order = append(order, column.Name())
			}
		}
		if db.Dialector.Name() == "postgres" {
			order = nil
			if err := db.Raw("SELECT a.attname FROM pg_index i JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = ANY(i.indkey) WHERE i.indrelid = to_regclass(?) AND i.indisprimary ORDER BY a.attnum", table).Scan(&order).Error; err != nil {
				return proof, err
			}
		}
		sort.Strings(names)
		if len(order) == 0 {
			order = append(order, names...)
		}
		sort.Strings(order)
		quoted := []string{}
		for _, name := range names {
			quoted = append(quoted, `"`+name+`"`)
		}
		orderQuoted := []string{}
		for _, name := range order {
			quoted := `"` + name + `"`
			if db.Dialector.Name() == "postgres" {
				quoted += "::text"
			}
			orderQuoted = append(orderQuoted, quoted)
		}
		rows, err := db.WithContext(ctx).Table(table).Select(strings.Join(quoted, ",")).Order(strings.Join(orderQuoted, ",")).Rows()
		if err != nil {
			return proof, err
		}
		hash := sha256.New()
		count := int64(0)
		for rows.Next() {
			values := make([]interface{}, len(names))
			dest := make([]interface{}, len(names))
			for i := range values {
				dest[i] = &values[i]
			}
			if err := rows.Scan(dest...); err != nil {
				rows.Close()
				return proof, err
			}
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
				rows.Close()
				return proof, err
			}
			hash.Write(raw)
			hash.Write([]byte{'\n'})
			count++
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return proof, err
		}
		rows.Close()
		proof.Tables = append(proof.Tables, MigrationTableProof{Table: table, Rows: count, SHA256: hex.EncodeToString(hash.Sum(nil))})
	}
	if scope == "core" {
		money, err := migrationMoneyProof(ctx, db)
		if err != nil {
			return proof, err
		}
		proof.Money = &money
	}
	return proof, nil
}
func migrationMoneyProof(ctx context.Context, db *gorm.DB) (MigrationMoneyProof, error) {
	var proof MigrationMoneyProof
	if err := db.WithContext(ctx).Table("users").Select("COALESCE(SUM(quota),0)").Scan(&proof.WalletTotal).Error; err != nil {
		return proof, err
	}
	if err := db.WithContext(ctx).Table("tokens").Select("COALESCE(SUM(remain_quota),0)").Scan(&proof.KeyRemainingTotal).Error; err != nil {
		return proof, err
	}
	var ledgers []struct {
		UserID int
		Amount int64
	}
	if err := db.WithContext(ctx).Table("wallet_ledgers").Select("user_id,SUM(delta_microusd) AS amount").Group("user_id").Order("user_id").Scan(&ledgers).Error; err != nil {
		return proof, err
	}
	raw, _ := json.Marshal(ledgers)
	sum := sha256.Sum256(raw)
	proof.LedgerByUserSHA256 = hex.EncodeToString(sum[:])
	monthExpr := "strftime('%Y-%m',occurred_at)"
	if db.Dialector.Name() == "postgres" {
		monthExpr = "to_char(occurred_at AT TIME ZONE 'UTC','YYYY-MM')"
	}
	var spends []struct {
		ProjectID int
		Month     string
		Amount    int64
	}
	if err := db.WithContext(ctx).Table("project_spends").Select("project_id," + monthExpr + " AS month,SUM(amount_microusd) AS amount").Group("project_id," + monthExpr).Order("project_id,month").Scan(&spends).Error; err != nil {
		return proof, err
	}
	raw, _ = json.Marshal(spends)
	sum = sha256.Sum256(raw)
	proof.ProjectMonthSHA256 = hex.EncodeToString(sum[:])
	return proof, nil
}
func CompareMigrationProofs(source, target MigrationProof) error {
	left, _ := json.Marshal(source)
	right, _ := json.Marshal(target)
	if string(left) != string(right) {
		return errors.New("migration verification differs: table rows/checksums or financial aggregates do not match")
	}
	return nil
}

// AuditTelemetryPartitions verifies the retained originals against the new
// parents before accepting the cutover. It never drops or rewrites originals.
func AuditTelemetryPartitions(ctx context.Context, db *gorm.DB) error {
	if db.Dialector.Name() != "postgres" {
		return errors.New("partition audit requires PostgreSQL")
	}
	for _, spec := range telemetryPartitionSpecs {
		old := spec.table + "_unpartitioned_v24"
		if !db.Migrator().HasTable(old) {
			return fmt.Errorf("missing retained original %s", old)
		}
		var missing int64
		columns := strings.Split(spec.uniqueColumns, ", ")
		joins := []string{}
		for _, column := range columns {
			joins = append(joins, "parent."+column+" = old."+column)
		}
		query := fmt.Sprintf("SELECT COUNT(*) FROM %s old WHERE NOT EXISTS (SELECT 1 FROM %s parent WHERE %s AND to_jsonb(parent) = to_jsonb(old))", old, spec.table, strings.Join(joins, " AND "))
		if err := db.WithContext(ctx).Raw(query).Scan(&missing).Error; err != nil {
			return err
		}
		if missing != 0 {
			return fmt.Errorf("%s has %d missing or changed historical rows", spec.table, missing)
		}
	}
	return nil
}

// A report is evidence for manual review, never an instruction to refund.
// Legacy precharge refs do not carry a reliable request association.
type LegacyPrechargeReport struct {
	LedgerID            uint      `json:"ledger_id"`
	UserID              int       `json:"user_id"`
	AmountMicrousd      int64     `json:"amount_microusd"`
	CreatedAt           time.Time `json:"created_at"`
	Evidence            string    `json:"evidence"`
	CandidateFailureIDs []uint    `json:"candidate_failure_ids,omitempty"`
}

func ReportLegacyPrecharges(ctx context.Context, db *gorm.DB) ([]LegacyPrechargeReport, error) {
	var rows []WalletLedger
	if err := db.WithContext(ctx).Where("ref_type = ? AND delta_microusd < 0", "relay_preconsume").Order("id").Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]LegacyPrechargeReport, 0, len(rows))
	cutoff := time.Now().UTC().AddDate(0, 0, -90)
	for _, row := range rows {
		report := LegacyPrechargeReport{LedgerID: row.ID, UserID: row.UserID, AmountMicrousd: -row.DeltaMicrousd.Int64(), CreatedAt: row.CreatedAt, Evidence: "ambiguous_no_request_association"}
		if row.CreatedAt.Before(cutoff) {
			report.Evidence = "outside_log_retention"
		} else {
			var matches []SettlementFailure
			if err := db.WithContext(ctx).Where("user_id = ? AND pre_consumed = ? AND created_at >= ? AND created_at <= ?", row.UserID, -row.DeltaMicrousd.Int64(), row.CreatedAt.Add(-time.Minute), row.CreatedAt.Add(time.Minute)).Find(&matches).Error; err != nil {
				return nil, err
			}
			for _, match := range matches {
				report.CandidateFailureIDs = append(report.CandidateFailureIDs, match.ID)
			}
			if len(matches) > 0 {
				report.Evidence = "candidate_settlement_failure_requires_manual_identity_check"
			}
		}
		result = append(result, report)
	}
	return result, nil
}

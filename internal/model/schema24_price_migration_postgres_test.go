package model

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"gorm.io/gorm"
)

// This is the populated price-table shape shipped by main at schema 24, not
// a fresh schema-25 AutoMigrate fixture. In particular its following-price
// metadata and materialized billing numbers must survive the gateway upgrade.
const schema24SellPriceDDL = `CREATE TABLE model_sell_prices (
	id bigserial PRIMARY KEY,
	model_name varchar(191) NOT NULL,
	channel_group varchar(32) NOT NULL DEFAULT 'direct',
	input_per_mtok numeric(20,10), output_per_mtok numeric(20,10),
	cache_read_per_mtok numeric(20,10), cache_write5m_per_mtok numeric(20,10),
	cache_write1h_per_mtok numeric(20,10), audio_input_per_mtok numeric(20,10),
	audio_output_per_mtok numeric(20,10), image_input_per_mtok numeric(20,10),
	image_output_per_mtok numeric(20,10), request_usd numeric(20,10),
	second_usd numeric(20,10), peak_hours_utc varchar(64), peak_multiplier numeric(10,4),
	context_tier_threshold bigint DEFAULT 0,
	context_tier_input_per_mtok numeric(20,10), context_tier_output_per_mtok numeric(20,10),
	context_tier_cache_read_per_mtok numeric(20,10),
	context_tier_cache_write5m_per_mtok numeric(20,10),
	context_tier_cache_write1h_per_mtok numeric(20,10),
	follow_official boolean DEFAULT false, price_ratio numeric(10,4),
	pinned_fields varchar(128) DEFAULT '', source varchar(64) DEFAULT 'manual',
	active boolean DEFAULT true, pricing_version bigint NOT NULL,
	created_at timestamptz, updated_at timestamptz
)`

func schema24PriceMigrationFixture(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("TEST_GATEWAY_CORE_DSN")
	if dsn == "" {
		t.Skip("isolated PostgreSQL test DSN is not configured")
	}
	admin, err := OpenMigrationDatabase(dsn)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("schema24_price_%d", time.Now().UnixNano())
	if err := admin.Exec("CREATE SCHEMA " + name).Error; err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal("parse fixture PostgreSQL URL")
	}
	q := u.Query()
	q.Set("search_path", name+",public") // pg_trgm's existing operator class is in public.
	u.RawQuery = q.Encode()
	db, err := OpenMigrationDatabase(u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		invalidateSchemaCatalogue(db)
		if err := admin.Exec("DROP SCHEMA " + name + " CASCADE").Error; err != nil {
			t.Errorf("remove schema-24 fixture: %v", err)
		}
		for _, connection := range []*gorm.DB{db, admin} {
			if raw, err := connection.DB(); err == nil {
				_ = raw.Close()
			}
		}
	})
	return db
}

func TestPostgresSchema24PriceUpgradePreservesFollowingFieldsAndMoney(t *testing.T) {
	db := schema24PriceMigrationFixture(t)
	previousCore, previousPostgres, previousSQLite := coreDB, common.UsingPostgreSQL, common.UsingSQLite
	previousGroup, previousKey, previousTrue, previousFalse := commonGroupCol, commonKeyCol, commonTrueVal, commonFalseVal
	previousLogGroup, previousLogKey := logGroupCol, logKeyCol
	coreDB, common.UsingPostgreSQL, common.UsingSQLite = db, true, false
	initCol() // InitDB prepares dialect-specific column names before migration.
	t.Cleanup(func() {
		coreDB, common.UsingPostgreSQL, common.UsingSQLite = previousCore, previousPostgres, previousSQLite
		commonGroupCol, commonKeyCol, commonTrueVal, commonFalseVal = previousGroup, previousKey, previousTrue, previousFalse
		logGroupCol, logKeyCol = previousLogGroup, previousLogKey
	})
	if err := db.Exec(schema24SellPriceDDL).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE UNIQUE INDEX idx_model_sell_prices_model_lane_version
		ON model_sell_prices(model_name,channel_group,pricing_version)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO model_sell_prices
		(model_name,channel_group,input_per_mtok,output_per_mtok,cache_read_per_mtok,
		 cache_write5m_per_mtok,cache_write1h_per_mtok,image_output_per_mtok,
		 context_tier_threshold,context_tier_input_per_mtok,follow_official,price_ratio,
		 pinned_fields,source,active,pricing_version,created_at,updated_at) VALUES
		('schema24-following','direct',1.2345678901,9.8765432101,0.1234567891,
		 1.1111111111,2.2222222222,30.3333333333,200000,2.3456789012,true,37.1250,
		 'output,context_tier','official_follow',true,2401,'2026-09-01T00:00:00Z','2026-09-02T00:00:00Z'),
		('schema24-fixed','direct',2.3456789012,8.7654321098,NULL,NULL,NULL,NULL,
		 0,NULL,false,NULL,'','manual',true,2402,'2026-09-01T00:00:00Z','2026-09-02T00:00:00Z'),
		('schema24-following','direct',0.7654321098,7.6543210987,NULL,NULL,NULL,NULL,
		 0,NULL,true,25.0000,'output','official_follow',false,2399,'2026-08-01T00:00:00Z','2026-08-02T00:00:00Z')`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&SchemaVersion{}, &User{}, &WalletLedger{}); err != nil {
		t.Fatal(err)
	}
	for _, scope := range []string{schemaVersionPrimary, schemaVersionLog} {
		if err := db.Create(&SchemaVersion{Scope: scope, Version: 24}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Create(&User{Id: 101, Username: "schema24-price-owner", Quota: 1234567, Status: common.UserStatusEnabled}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&WalletLedger{UserID: 101, DeltaMicrousd: 1234567, Kind: WalletLedgerKindTopup, RefType: "schema24_fixture", RefID: "retained-credit", PricingVersion: 2401, Metadata: `{"source":"historical fixture"}`, CreatedAt: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)}).Error; err != nil {
		t.Fatal(err)
	}
	readRows := func(table string) []string {
		t.Helper()
		var rows []string
		projection := "to_jsonb(row)"
		if table == "model_sell_prices" {
			// Schemas 28 and 35 add nullable image and video price sheets.
			// Compare all historical fields exactly, and assert the new fields
			// stay NULL separately.
			projection = "(to_jsonb(row) - 'image_pricing' - 'video_pricing')"
		}
		if err := db.Raw("SELECT " + projection + "::text FROM " + table + " row ORDER BY id").Scan(&rows).Error; err != nil {
			t.Fatal(err)
		}
		return rows
	}
	pricesBefore, ledgerBefore := readRows("model_sell_prices"), readRows("wallet_ledgers")
	for attempt := 0; attempt < 2; attempt++ {
		if err := RunPrimaryMigrations(); err != nil {
			t.Fatal(err)
		}
		var version SchemaVersion
		if err := db.Where("scope = ?", schemaVersionPrimary).First(&version).Error; err != nil || version.Version != SchemaVersionCurrent {
			t.Fatalf("schema24 upgrade did not reach current schema: version=%d err=%v", version.Version, err)
		}
		if after := readRows("model_sell_prices"); !reflect.DeepEqual(after, pricesBefore) {
			t.Fatalf("upgrade attempt %d changed historical price rows\nbefore=%v\nafter=%v", attempt, pricesBefore, after)
		}
		var imagePriceCount int64
		if err := db.Table("model_sell_prices").Where("image_pricing IS NOT NULL").Count(&imagePriceCount).Error; err != nil || imagePriceCount != 0 {
			t.Fatalf("upgrade attempt %d added image prices to historical rows: count=%d err=%v", attempt, imagePriceCount, err)
		}
		var videoPriceCount int64
		if err := db.Table("model_sell_prices").Where("video_pricing IS NOT NULL").Count(&videoPriceCount).Error; err != nil || videoPriceCount != 0 {
			t.Fatalf("upgrade attempt %d added video prices to historical rows: count=%d err=%v", attempt, videoPriceCount, err)
		}
		if after := readRows("wallet_ledgers"); !reflect.DeepEqual(after, ledgerBefore) {
			t.Fatalf("upgrade attempt %d changed historical wallet facts", attempt)
		}
		var owner User
		if err := db.First(&owner, 101).Error; err != nil || owner.Quota != 1234567 {
			t.Fatalf("upgrade changed wallet balance: quota=%d err=%v", owner.Quota, err)
		}
	}
	// Marking the explicitly supported old-image target does not remove the
	// newer additive columns or alter materialized prices. It is only the final
	// marker step; financial/Telemetry rollback is covered by its own fixtures.
	// The full schema25 image requires both markers to match 25. Its price
	// following columns are retained, as are schema24 materialized wallet facts.
	if err := db.Model(&SchemaVersion{}).Where("scope = ?", schemaVersionLog).Update("version", SchemaVersionCurrent).Error; err != nil {
		t.Fatal(err)
	}
	if err := MarkLegacySchemaRollbackToVersion(context.Background(), db, 25); err != nil {
		t.Fatal(err)
	}
	var prepared []SchemaVersion
	if err := db.Where("scope IN ?", []string{schemaVersionPrimary, schemaVersionLog}).Find(&prepared).Error; err != nil || len(prepared) != 2 || prepared[0].Version != 25 || prepared[1].Version != 25 {
		t.Fatalf("schema25 target does not match the previous image: %+v err=%v", prepared, err)
	}
	if !reflect.DeepEqual(readRows("model_sell_prices"), pricesBefore) || !reflect.DeepEqual(readRows("wallet_ledgers"), ledgerBefore) {
		t.Fatal("schema25 rollback marker changed retained schema24 price or wallet facts")
	}
	if err := MarkLegacySchemaRollback(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	var versions []SchemaVersion
	if err := db.Where("scope IN ?", []string{schemaVersionPrimary, schemaVersionLog}).Find(&versions).Error; err != nil || len(versions) != 2 || versions[0].Version != 23 || versions[1].Version != 23 {
		t.Fatalf("legacy target no longer marks both old scopes as23: versions=%+v err=%v", versions, err)
	}
	if !reflect.DeepEqual(readRows("model_sell_prices"), pricesBefore) || !reflect.DeepEqual(readRows("wallet_ledgers"), ledgerBefore) {
		t.Fatal("schema23 rollback marker changed retained schema24 price or wallet facts")
	}
}

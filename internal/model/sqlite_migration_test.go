package model

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// Repeat the startup migration against a persistent database. In particular,
// decimal precision commas must survive the driver's table reconstruction.
func TestSQLiteRepeatedTopUpMigrationPreservesDataAndConstraints(t *testing.T) {
	for _, schema := range []string{"fresh", "v1.9.0"} {
		t.Run(schema, func(t *testing.T) {
			testSQLiteRepeatedTopUpMigration(t, schema == "v1.9.0")
		})
	}
}

func testSQLiteRepeatedTopUpMigration(t *testing.T, legacy bool) {
	path := filepath.Join(t.TempDir(), "migration.sqlite")
	want := TopUp{
		InvoiceStatus: "legacy",
		UserId:        42, TradeNo: "migration-fixture", PaymentMethod: "fixture",
		PaidAmount: 12.3456789012, CreditedMicrousd: 13580246, BonusMicrousd: 1234567,
	}
	for pass := 0; pass < 3; pass++ {
		func() {
			db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
			if err != nil {
				t.Fatal(err)
			}
			pool, err := db.DB()
			if err != nil {
				t.Fatal(err)
			}
			defer pool.Close()
			if pass == 0 && legacy {
				// Schema emitted by sqlite v1.9.0 / GORM v1.25.2, including
				// its inline UNIQUE constraint and decimal default syntax.
				const ddl = `CREATE TABLE "top_ups" (
                    id integer, user_id integer, amount integer, money real,
                    trade_no varchar(255) UNIQUE, payment_method varchar(50),
                    paid_amount decimal(20,10) DEFAULT 0.000000,
                    paid_currency varchar(8) DEFAULT "USD",
                    credited_microusd bigint DEFAULT 0, bonus_microusd bigint DEFAULT 0,
                    presentment_amount decimal(20,10) DEFAULT 0.000000,
                    bonus_credit_rate decimal(10,6) DEFAULT 0.000000,
                    create_time integer, complete_time integer, status text,
                    pending_notify_sent_at integer, PRIMARY KEY (id))`
				for _, sql := range []string{ddl,
					"CREATE INDEX idx_top_ups_user_id ON top_ups(user_id)",
					"CREATE INDEX idx_top_ups_trade_no ON top_ups(trade_no)",
				} {
					if err := db.Exec(sql).Error; err != nil {
						t.Fatal(err)
					}
				}
				// Insert only columns that existed in the frozen legacy schema;
				// new project/invoice columns are added by AutoMigrate below.
				if err := db.Session(&gorm.Session{SkipHooks: true}).Select("user_id", "amount", "trade_no", "payment_method", "paid_amount", "credited_microusd", "bonus_microusd", "create_time", "complete_time", "status", "pending_notify_sent_at").Create(&want).Error; err != nil {
					t.Fatal(err)
				}
			}
			if err := db.AutoMigrate(&TopUp{}); err != nil {
				t.Fatalf("migration pass %d: %v", pass+1, err)
			}
			if pass == 0 && !legacy {
				if err := db.Create(&want).Error; err != nil {
					t.Fatal(err)
				}
			}
			var got TopUp
			if err := db.First(&got, want.Id).Error; err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("migration changed payment data: got %+v, want %+v", got, want)
			}
			// trade_no keeps its UNIQUE constraint; the second, plain index on it
			// was retired in schema version 15.
			for _, index := range []string{"idx_top_ups_user_id", "idx_top_ups_user_status_complete"} {
				if !db.Migrator().HasIndex(&TopUp{}, index) {
					t.Fatalf("migration lost index %s", index)
				}
			}
			duplicate := want
			duplicate.Id = 0
			if err := db.Create(&duplicate).Error; err == nil {
				t.Fatal("migration lost trade number uniqueness")
			}
		}()
	}
}

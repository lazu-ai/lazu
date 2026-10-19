package metrics

import (
	"context"
	"strings"
	"testing"

	gormsqlite "github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestComputeZeroTokenQuotaZeroRate(t *testing.T) {
	tests := []struct {
		name  string
		total int64
		zero  int64
		want  float64
	}{
		{name: "empty window", total: 0, zero: 10, want: 0},
		{name: "no zero rows", total: 100, zero: 0, want: 0},
		{name: "one percent", total: 100, zero: 1, want: 0.01},
		{name: "two percent", total: 500, zero: 10, want: 0.02},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ComputeZeroTokenQuotaZeroRate(tt.total, tt.zero)
			if got != tt.want {
				t.Fatalf("expected %v, got %v", tt.want, got)
			}
		})
	}
}

func TestQueryZeroTokenQuotaZeroRateIgnoresInputOnlyRows(t *testing.T) {
	dbName := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	db, err := gorm.Open(gormsqlite.Open("file:"+dbName+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	if err := db.Exec(`
		CREATE TABLE logs (
			type integer,
			created_at integer,
			amount_microusd integer,
			prompt_tokens integer,
			completion_tokens integer
		)
	`).Error; err != nil {
		t.Fatalf("failed to create logs table: %v", err)
	}
	rows := []struct {
		logType          int
		createdAt        int64
		amountMicrousd   int64
		promptTokens     int
		completionTokens int
	}{
		{logType: logTypeConsume, createdAt: 150, amountMicrousd: 0, promptTokens: 0, completionTokens: 0},
		{logType: logTypeConsume, createdAt: 151, amountMicrousd: 0, promptTokens: 12, completionTokens: 0},
		{logType: logTypeConsume, createdAt: 152, amountMicrousd: 1, promptTokens: 42, completionTokens: 0},
		{logType: logTypeConsume, createdAt: 99, amountMicrousd: 0, promptTokens: 0, completionTokens: 0},
		{logType: 1, createdAt: 153, amountMicrousd: 0, promptTokens: 0, completionTokens: 0},
	}
	for _, row := range rows {
		if err := db.Exec(
			`INSERT INTO logs (type, created_at, amount_microusd, prompt_tokens, completion_tokens) VALUES (?, ?, ?, ?, ?)`,
			row.logType,
			row.createdAt,
			row.amountMicrousd,
			row.promptTokens,
			row.completionTokens,
		).Error; err != nil {
			t.Fatalf("failed to insert log row: %v", err)
		}
	}

	got, err := QueryZeroTokenQuotaZeroRate(context.Background(), db, 100, 200)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if got.Total != 3 {
		t.Fatalf("expected total 3, got %d", got.Total)
	}
	if got.Zero != 1 {
		t.Fatalf("expected one full zero-token row, got %d", got.Zero)
	}
	if got.Rate != 1.0/3.0 {
		t.Fatalf("expected rate 1/3, got %v", got.Rate)
	}
}

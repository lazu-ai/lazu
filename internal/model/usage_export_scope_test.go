package model

import (
	"bytes"
	"strings"
	"testing"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/xuri/excelize/v2"
)

func TestUsageExportCannotBroadenMemberScopeOrExposePrivateMetadata(t *testing.T) {
	cleanup := setupDashboardUsageTestDB(t)
	defer cleanup()
	for id := 1; id <= 2; id++ {
		insertDashboardUsageLog(t, Log{UserId: id, PublicID: string(rune('a' + id)), RequestID: string(rune('a' + id)), Type: LogTypeConsume, CreatedAt: 100, AmountMicrousd: 5})
	}
	rows, err := ListDashboardUsageLogs(1, common.RoleCommonUser, DashboardUsageWindow{}, DashboardUsageLogFilters{UserID: 2, ChannelID: 99}, 100)
	if err != nil || len(rows) != 1 || rows[0].UserId != 1 {
		t.Fatalf("member export widened scope: rows=%+v err=%v", rows, err)
	}
	rows[0].Other = `{"admin_info":{"provider_key":"private-fixture-key","upstream_cost":123}}`
	csv, err := BuildUsageLogsCSV(rows)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(csv, []byte("private-fixture")) || bytes.Contains(csv, []byte("upstream_cost")) {
		t.Fatal("private metadata leaked into CSV")
	}
	xlsx, err := BuildUsageLogsXLSX(rows)
	if err != nil {
		t.Fatal(err)
	}
	file, err := excelize.OpenReader(bytes.NewReader(xlsx))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	cells, err := file.GetRows("Logs")
	if err != nil || len(cells) != 2 {
		t.Fatalf("invalid XLSX: %v %v", cells, err)
	}
	for _, row := range cells {
		for _, value := range row {
			if strings.Contains(value, "private-fixture") || strings.Contains(value, "upstream_cost") {
				t.Fatal("private metadata leaked into XLSX")
			}
		}
	}
}

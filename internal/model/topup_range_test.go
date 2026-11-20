package model

import (
	"testing"
	"time"

	gormsqlite "github.com/glebarez/sqlite"
	"github.com/lazu-ai/lazu/internal/common"
	"gorm.io/gorm"
)

// The wallet's bill asks for six months of an account's own top-ups. An
// explicit range must reach past the default 30-day window, stay inside its
// half-open bounds and never return another account's rows.
func TestGetUserTopUpsBetweenReachesPastTheDefaultWindow(t *testing.T) {
	db, err := gorm.Open(gormsqlite.Open("file:topups_between?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	previous := CoreDB()
	SetCoreDB(db)
	t.Cleanup(func() {
		SetCoreDB(previous)
		raw, _ := db.DB()
		_ = raw.Close()
	})
	if err := db.AutoMigrate(&TopUp{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	day := int64(24 * 60 * 60)
	start := now.Unix() - 180*day
	end := now.Unix() + 1
	rows := []TopUp{
		{PublicID: "a", UserId: 7, TradeNo: "t-a", Status: common.TopUpStatusSuccess, CreateTime: now.Unix() - 120*day},
		{PublicID: "b", UserId: 7, TradeNo: "t-b", Status: common.TopUpStatusPending, CreateTime: now.Unix() - 2*day},
		{PublicID: "c", UserId: 7, TradeNo: "t-c", Status: common.TopUpStatusSuccess, CreateTime: start - 1},
		{PublicID: "d", UserId: 8, TradeNo: "t-d", Status: common.TopUpStatusSuccess, CreateTime: now.Unix() - day},
		{PublicID: "e", UserId: 7, TradeNo: "t-e", Status: common.TopUpStatusSuccess, CreateTime: end},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}

	page := &common.PageInfo{Page: 1, PageSize: 10}
	got, total, err := GetUserTopUpsBetween(7, start, end, page)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(got) != 2 || got[0].PublicID != "b" || got[1].PublicID != "a" {
		ids := []string{}
		for _, row := range got {
			ids = append(ids, row.PublicID)
		}
		t.Fatalf("total=%d rows=%v", total, ids)
	}

	// The default listing still stops at 30 days: the 120-day-old row is
	// only reachable through an explicit range.
	recent, _, err := GetUserTopUps(7, page)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range recent {
		if row.PublicID == "a" || row.PublicID == "c" {
			t.Fatalf("default window returned %q", row.PublicID)
		}
	}

	// Paging keeps the count of the whole range.
	got, total, err = GetUserTopUpsBetween(7, start, end, &common.PageInfo{Page: 2, PageSize: 1})
	if err != nil || total != 2 || len(got) != 1 || got[0].PublicID != "a" {
		t.Fatalf("second page total=%d len=%d err=%v", total, len(got), err)
	}
}

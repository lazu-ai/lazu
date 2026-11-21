package model

import (
	"fmt"
	"testing"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
)

func TestGetUserRatePeak(t *testing.T) {
	_, logDB := newLedgerTestDBs(t)
	minute := (time.Now().Unix()/60 - 5) * 60
	logs := []Log{
		// Busiest minute by requests: three calls, 30 tokens.
		{UserId: 7, Type: LogTypeConsume, CreatedAt: minute, PromptTokens: 10},
		{UserId: 7, Type: LogTypeConsume, CreatedAt: minute + 20, PromptTokens: 10},
		{UserId: 7, Type: LogTypeError, CreatedAt: minute + 59, PromptTokens: 10},
		// Busiest minute by tokens: one call, 900 tokens.
		{UserId: 7, Type: LogTypeConsume, CreatedAt: minute + 60, PromptTokens: 400, CompletionTokens: 500},
		// Outside the window, another account, and a non-request row.
		{UserId: 7, Type: LogTypeConsume, CreatedAt: minute - 7200, PromptTokens: 5000},
		{UserId: 8, Type: LogTypeConsume, CreatedAt: minute, PromptTokens: 9000},
		{UserId: 7, Type: LogTypeTopup, CreatedAt: minute},
	}
	for i := range logs {
		logs[i].RequestTS = logs[i].CreatedAt
		logs[i].PublicID = fmt.Sprintf("log-%d", i)
	}
	if err := logDB.Create(&logs).Error; err != nil {
		t.Fatalf("seed logs: %v", err)
	}
	peak, err := GetUserRatePeak(7, minute-60)
	if err != nil {
		t.Fatalf("GetUserRatePeak: %v", err)
	}
	if peak.RPM != 3 || peak.TPM != 900 {
		t.Fatalf("peak = %+v, want rpm 3 tpm 900", peak)
	}
	empty, err := GetUserRatePeak(99, minute-60)
	if err != nil || empty.RPM != 0 || empty.TPM != 0 {
		t.Fatalf("empty peak = %+v, %v", empty, err)
	}
}

func TestLatestBalanceAlertAt(t *testing.T) {
	_, logDB := newLedgerTestDBs(t)
	if err := logDB.AutoMigrate(&MailDelivery{}); err != nil {
		t.Fatalf("migrate mail: %v", err)
	}
	rows := []MailDelivery{
		{Kind: common.MailKindBalance, UserID: 7, Status: MailStatusDelivered, CreatedAt: 100, SentAt: 101},
		{Kind: common.MailKindBalance, UserID: 7, Template: "balance-runway", Status: MailStatusSent, CreatedAt: 200, SentAt: 201},
		// Not a reminder that left: failed, another template, another kind.
		{Kind: common.MailKindBalance, UserID: 7, Status: MailStatusFailed, CreatedAt: 300},
		{Kind: common.MailKindBalance, UserID: 7, Template: "auto-reload-failed", Status: MailStatusSent, CreatedAt: 400},
		{Kind: common.MailKindPayment, UserID: 7, Status: MailStatusSent, CreatedAt: 500},
	}
	if err := logDB.Create(&rows).Error; err != nil {
		t.Fatalf("seed mail: %v", err)
	}
	at, err := LatestBalanceAlertAt(7)
	if err != nil || at != 201 {
		t.Fatalf("LatestBalanceAlertAt = %d, %v; want 201", at, err)
	}
	none, err := LatestBalanceAlertAt(8)
	if err != nil || none != 0 {
		t.Fatalf("no mail = %d, %v", none, err)
	}
}

func TestGetUserPendingTopUps(t *testing.T) {
	db, _ := newLedgerTestDBs(t)
	now := common.GetTimestamp()
	rows := []TopUp{
		{UserId: 7, TradeNo: "old", Status: common.TopUpStatusPending, CreateTime: now - 40*24*3600, Amount: 5},
		{UserId: 7, TradeNo: "paid", Status: common.TopUpStatusSuccess, CreateTime: now, Amount: 5},
		{UserId: 8, TradeNo: "other", Status: common.TopUpStatusPending, CreateTime: now, Amount: 5},
		{UserId: 7, TradeNo: "a", Status: common.TopUpStatusPending, CreateTime: now - 60, Amount: 10},
		{UserId: 7, TradeNo: "b", Status: common.TopUpStatusPending, CreateTime: now, Amount: 20},
	}
	for i := range rows {
		rows[i].PublicID = rows[i].TradeNo
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("seed top-ups: %v", err)
	}
	got, err := GetUserPendingTopUps(7, 10)
	if err != nil {
		t.Fatalf("GetUserPendingTopUps: %v", err)
	}
	if len(got) != 2 || got[0].TradeNo != "b" || got[1].TradeNo != "a" {
		t.Fatalf("pending = %+v, want b then a", got)
	}
}

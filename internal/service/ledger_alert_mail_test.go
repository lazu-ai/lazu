package service

import (
	"strings"
	"testing"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
)

func TestLedgerAlertMailNamesTheFindingAndItsAmounts(t *testing.T) {
	subject, mail := ledgerAlertMail(model.LedgerAlertNotice{
		Key:     "budget:90",
		Message: "budget_id=90 hot_spent=824500 expected_spent=824522 hot_reserved=0 expected_reserved=0",
		Facts: []model.LedgerAlertFact{
			{Label: "Key", Value: "Seedream launch verification (#1000903)"},
			{Label: "差额（数据库 − Redis）", Value: "+$0.000022", Mono: true},
		},
	})
	if subject != "账本：预算 #90 两本账不一致" || mail.Title != "预算 #90 两本账不一致" {
		t.Fatalf("subject=%q title=%q", subject, mail.Title)
	}
	mail.Tier = common.MailOps
	body, err := common.RenderMail(mail)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Seedream launch verification (#1000903)", "$0.000022", "budget:90", "24 小时后", "hot_spent=824500"} {
		if !strings.Contains(body, want) {
			t.Fatalf("rendered mail is missing %q", want)
		}
	}

	subject, mail = ledgerAlertMail(model.LedgerAlertNotice{Key: "redis-aof", Message: "Redis AOF durability needs attention"})
	if subject != "账本：Redis AOF 持久化异常" || mail.Lead != "Redis AOF durability needs attention" {
		t.Fatalf("durability alert subject=%q lead=%q", subject, mail.Lead)
	}
}

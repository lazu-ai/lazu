package common

import (
	"strings"
	"testing"
)

func captureOpsAlerts(t *testing.T) *[]OpsAlert {
	t.Helper()
	var got []OpsAlert
	old := opsAlertSinks
	opsAlertSinks = []func(OpsAlert){func(a OpsAlert) { got = append(got, a) }}
	t.Cleanup(func() { opsAlertSinks = old })
	return &got
}

func TestOpsMailReachesAlertSinksOnce(t *testing.T) {
	got := captureOpsAlerts(t)
	// No transport is configured, so delivery fails; the alert still goes out.
	_ = SendMail(MailMeta{Kind: MailKindOps, AlertText: "disk: 91%"}, "[Lazu ops] disk", "ops@example.com", "<p>disk</p>")
	if len(*got) != 1 || (*got)[0].Subject != "[Lazu ops] disk" || (*got)[0].Text != "disk: 91%" {
		t.Fatalf("alerts = %+v", *got)
	}
}

func TestNonAlertMailSkipsAlertSinks(t *testing.T) {
	got := captureOpsAlerts(t)
	_ = SendMail(MailMeta{Kind: MailKindOps, NotAnAlert: true}, "[Lazu ops] 测试邮件", "ops@example.com", "<p>t</p>")
	_ = SendMail(MailMeta{Kind: MailKindBalance}, "balance", "u@example.com", "<p>b</p>")
	if len(*got) != 0 {
		t.Fatalf("non-alert mail reached sinks: %+v", *got)
	}
}

func TestMailAlertText(t *testing.T) {
	text := MailAlertText(Mail{
		Lead: "Disk is filling up.",
		Blocks: []MailBlock{
			{Facts: []MailFact{{Label: "Used", Value: "91%"}, {Label: "Host", Value: "db"}}},
			{Text: "Free space soon."},
			{Table: &MailTable{Head: []string{"a", "b"}, Rows: [][]string{{"1", "2"}}}},
			{HTML: "<b>ignored</b>"},
		},
	})
	want := "Disk is filling up.\nUsed: 91%\nHost: db\nFree space soon.\na | b\n1 | 2"
	if text != want {
		t.Fatalf("MailAlertText =\n%s\nwant\n%s", text, want)
	}
	if strings.Contains(text, "ignored") {
		t.Fatal("raw HTML block leaked into alert text")
	}
}

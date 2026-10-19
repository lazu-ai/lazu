package common

import (
	"html/template"
	"strings"
	"testing"
)

func everyBlock() []MailBlock {
	return []MailBlock{
		{Facts: []MailFact{{Label: "邮箱", Value: `a<b@example.test`}, {Label: "Key", Value: "sk-…9f2a", Mono: true}}},
		{Code: "482913"},
		{Snippet: `curl -d '{"a":1}'`},
		{Steps: []MailStep{{Text: "Create a Key"}, {Text: "Point at", Mono: "https://api.lazu.ai/v1"}}},
		{Table: &MailTable{Head: []string{"时间", "错误"}, Rows: [][]string{{"14:09:12", "<reset>"}}}},
		{HTML: template.HTML("<p>trusted <b>notice</b></p>")},
		{Text: "Not you?", LinkLabel: "Reset", LinkURL: "https://lazu.ai/reset", TextAfter: " Now."},
		{Primary: &MailAction{Label: "Open", URL: `javascript:alert(1)`}, Secondary: &MailAction{Label: "Docs", URL: "https://lazu.ai/docs"}},
	}
}

func TestRenderMailEscapesTextAndNeutralisesUnsafeLinks(t *testing.T) {
	for _, tier := range []string{MailInk, MailPaper, MailOps} {
		body, err := RenderMail(Mail{Tier: tier, Title: `<script>alert(1)</script>`, Hero: "$3.20", Message: "{{.Title}} & more", Source: "DISK", Severity: "ALERT", Blocks: everyBlock()})
		if err != nil {
			t.Fatalf("%s: %v", tier, err)
		}
		for _, bad := range []string{"<script>", `href="javascript:`, "<reset>"} {
			if strings.Contains(body, bad) {
				t.Fatalf("%s: unsafe or unrendered content %q", tier, bad)
			}
		}
		if tier == MailInk && !strings.Contains(body, "{{.Title}} &amp; more") {
			t.Fatal("ink message must be printed literally, not executed")
		}
		for _, want := range []string{"a&lt;b@example.test", "#ZgotmplZ", "<b>notice</b>", "482913", "#202329"} {
			if !strings.Contains(body, want) {
				t.Fatalf("%s: missing %q", tier, want)
			}
		}
	}
}

func TestRenderMailTierShells(t *testing.T) {
	ink, _ := RenderMail(Mail{Tier: MailInk, Lang: MailEN, Hero: "$3.20", Eyebrow: "LOW BALANCE"})
	if !strings.Contains(ink, `bgcolor="#202329"`) || !strings.Contains(ink, "lazu-email-logo-paper.png") || !strings.Contains(ink, "Contact support") {
		t.Fatal("ink tier lost its masthead, paper logo or English footer")
	}
	paper, _ := RenderMail(Mail{Tier: MailPaper, Title: "验证你的邮箱"})
	if !strings.Contains(paper, "lazu-email-logo.png") || !strings.Contains(paper, "联系支持") || strings.Contains(paper, "请勿回复") {
		t.Fatal("paper tier lost its graphite logo or Chinese footer")
	}
	ops, _ := RenderMail(Mail{Tier: MailOps, Title: "磁盘 86%", Source: "DISK", Severity: "ALERT"})
	if !strings.Contains(ops, "LAZU OPS · DISK") || !strings.Contains(ops, "#b42318") || strings.Contains(ops, "lazu-email-logo") {
		t.Fatal("ops tier should carry source and severity and no logo")
	}
}

func TestMailLang(t *testing.T) {
	for raw, want := range map[string]string{"": MailEN, "zh-CN,zh;q=0.9": MailZH, "zh": MailZH, "zh-TW": MailZH, "en-US,en;q=0.8": MailEN, "ja": MailEN, "*": MailEN} {
		if got := MailLang(raw); got != want {
			t.Fatalf("MailLang(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestMailUSD(t *testing.T) {
	for in, want := range map[float64]string{3.2: "$3.20", 1234.5: "$1,234.50", -5: "-$5.00", 1000000: "$1,000,000.00"} {
		if got := MailUSD(in); got != want {
			t.Fatalf("MailUSD(%v) = %q, want %q", in, got, want)
		}
	}
	if MailSignedUSD(20) != "+$20.00" {
		t.Fatal("credit should carry a plus sign")
	}
}

package common

import (
	"strings"
	"testing"
)

func TestPaymentEmailRendersSafeMailCompatibleMarkup(t *testing.T) {
	body, err := RenderPaymentEmail(PaymentEmailContent{
		LogoURL: "https://lazu.ai/lazu-email-logo-paper.png", Eyebrow: "PAYMENT RECEIVED",
		Hero: "$5.00", Message: "API credit added to your Lazu account.",
		Facts:     []MailFact{{Label: "Paid by", Value: `<script>alert(1)</script>`}, {Label: "Invoice", Value: `INV-<test>`, Mono: true}},
		ActionURL: "https://lazu.ai/console/wallet?top_up_id=top_test", ActionLabel: "Open wallet",
		LinkURL: "https://lazu.ai/payment/document?document=invoice_page", LinkLabel: "View invoice",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"<table", "bgcolor=\"#202329\"", "lazu-email-logo-paper.png", "Open wallet", "View invoice", "document=invoice_page", "&lt;script&gt;", "INV-&lt;test&gt;"} {
		if !strings.Contains(body, expected) {
			t.Fatalf("payment email missing %q", expected)
		}
	}
	withoutLink, err := RenderPaymentEmail(PaymentEmailContent{Eyebrow: "PAYMENT RECEIVED", Hero: "$5.00", ActionURL: "https://lazu.ai/console/wallet", ActionLabel: "Open wallet"})
	if err != nil || strings.Contains(withoutLink, `class="payment-secondary"`) || strings.Contains(withoutLink, "→") {
		t.Fatalf("payment email drew an empty link slot: %v", err)
	}
	for _, unsupported := range []string{"<script", "display:grid", "display:flex", ".svg", "fonts.googleapis.com"} {
		if strings.Contains(body, unsupported) {
			t.Fatalf("payment email contains unsupported %q", unsupported)
		}
	}
}

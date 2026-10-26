package model

import (
	"bytes"
	_ "embed"
	"html/template"

	"github.com/lazu-ai/lazu/internal/common"
)

//go:embed email_templates/payment.html
var paymentEmailHTML string

var paymentEmailTemplate = template.Must(template.New("payment").Parse(paymentEmailHTML))

// PaymentEmailContent is rendered by html/template so project names, invoice
// numbers and URLs from Stripe cannot inject markup into a notification.
type PaymentEmailContent struct {
	LogoURL     string
	Eyebrow     string
	Hero        string
	CompactHero bool
	Message     string
	Preheader   string
	Facts       []common.MailFact
	ActionURL   string
	ActionLabel string
	// One optional text link beside the button (View invoice).
	LinkURL   string
	LinkLabel string
}

func RenderPaymentEmail(content PaymentEmailContent) (string, error) {
	var body bytes.Buffer
	err := paymentEmailTemplate.Execute(&body, content)
	return body.String(), err
}

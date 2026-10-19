package common

import (
	"bytes"
	_ "embed"
	"fmt"
	"html/template"
	"strings"
)

// Lazu mail uses one shell with three tiers (brand/foundations/emails.md):
//
//   - MailInk: the news is an amount or a share (balance adjusted, low
//     balance, Key usage). Follows the locked payment mail.
//   - MailPaper: account and team mail.
//   - MailOps: alerts for ADMIN_EMAIL only.
//
// The payment-received mail keeps its own locked template (payment.html).
const (
	MailInk   = "ink"
	MailPaper = "paper"
	MailOps   = "ops"
)

// Mail languages. Every mail to a person is written in both; ops mail is
// Chinese only.
const (
	MailZH = "zh"
	MailEN = "en"
)

//go:embed email_templates/mail.html
var mailHTML string

var mailTemplate = template.Must(template.New("mail").Funcs(template.FuncMap{
	"inc": func(i int) int { return i + 1 },
}).Parse(mailHTML))

type MailFact struct {
	Label string
	Value string
	Mono  bool
}

type MailAction struct {
	Label string
	URL   string
}

type MailStep struct {
	Text string
	Mono string
}

type MailTable struct {
	Head []string
	Rows [][]string
}

// MailBlock is one stacked element of the body. Set exactly one kind of
// content per block; a block with Primary/Secondary renders the action row.
type MailBlock struct {
	Facts   []MailFact
	Code    string
	Snippet string
	Steps   []MailStep
	Table   *MailTable
	// HTML is trusted markup written by a platform administrator (a broadcast
	// body). Never put user-controlled text here.
	HTML      template.HTML
	Text      string
	Muted     bool
	LinkLabel string
	LinkURL   string
	TextAfter string
	Primary   *MailAction
	Secondary *MailAction
}

// FactWidth spreads ink-tier facts across equal columns.
func (b MailBlock) FactWidth() int {
	if len(b.Facts) == 0 {
		return 100
	}
	return 100 / len(b.Facts)
}

type Mail struct {
	Lang      string
	Tier      string
	Preheader string
	Eyebrow   string
	// Ink tier.
	Hero        string
	CompactHero bool
	Message     string
	// Paper and ops tiers.
	Title string
	Lead  string
	// Ops tier.
	Source   string
	Severity string // ALERT, WARN or NOTICE

	Blocks []MailBlock

	LogoURL     string
	FooterLeft  string
	FooterRight string
	FooterHref  string
}

func (m Mail) SeverityColor() string {
	switch m.Severity {
	case "ALERT":
		return "#b42318"
	case "WARN":
		return "#b54708"
	}
	return "#696d75"
}

// RenderMail fills the defaults every tier shares and renders the shell.
// Text is escaped by html/template; only MailBlock.HTML is trusted.
func RenderMail(m Mail) (string, error) {
	if m.Lang != MailEN {
		m.Lang = MailZH
	}
	if m.Tier == "" {
		m.Tier = MailPaper
	}
	if m.LogoURL == "" {
		if m.Tier == MailInk {
			m.LogoURL = MailSiteURL("/lazu-email-logo-paper.png")
		} else {
			m.LogoURL = MailSiteURL("/lazu-email-logo.png")
		}
	}
	if m.FooterLeft == "" {
		if m.Tier == MailOps {
			node := strings.TrimSpace(NodeName)
			if node == "" {
				node = "lazu"
			}
			m.FooterLeft = "lazu-backend@" + node
		} else {
			m.FooterLeft = "Lazu.ai"
		}
	}
	if m.FooterRight == "" {
		switch {
		case m.Tier == MailOps:
			m.FooterRight, m.FooterHref = "打开控制台", MailSiteURL("/console")
		case m.Lang == MailEN:
			m.FooterRight = "Contact support"
		default:
			m.FooterRight = "联系支持"
		}
	}
	if m.FooterHref == "" {
		if email := SiteSupportEmail(); email != "" {
			m.FooterHref = "mailto:" + email
		} else {
			m.FooterHref = SiteWebOrigin()
		}
	}
	var body bytes.Buffer
	err := mailTemplate.Execute(&body, m)
	return body.String(), err
}

// MailSiteURL builds an absolute link on the public site (console, assets).
// Mail is read outside the product, so every link must be absolute.
func MailSiteURL(path string) string {
	base := SiteWebOrigin()
	if path != "" && !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return base + path
}

// MailDefaultLang is the language for a reader whose console language is
// unknown: an account that has not opened the console since languages were
// recorded, or a request without Accept-Language.
const MailDefaultLang = MailEN

// MailLang maps a locale or an Accept-Language value to a mail language.
// Chinese variants read Chinese mail; everything else reads English. An
// unknown reader gets MailDefaultLang.
func MailLang(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return MailDefaultLang
	}
	first := strings.ToLower(strings.TrimSpace(strings.SplitN(strings.SplitN(raw, ",", 2)[0], ";", 2)[0]))
	if first == "" || first == "*" {
		return MailDefaultLang
	}
	if strings.HasPrefix(first, "zh") {
		return MailZH
	}
	return MailEN
}

// MailText picks the sentence for a mail language.
func MailText(lang, zh, en string) string {
	if lang == MailEN {
		return en
	}
	return zh
}

// MailUSD formats a dollar amount the way every Lazu mail shows money.
func MailUSD(dollars float64) string {
	sign := ""
	if dollars < 0 {
		sign = "-"
		dollars = -dollars
	}
	return fmt.Sprintf("%s$%s", sign, groupThousands(fmt.Sprintf("%.2f", dollars)))
}

// MailSignedUSD is MailUSD with an explicit + for credits.
func MailSignedUSD(dollars float64) string {
	if dollars > 0 {
		return "+" + MailUSD(dollars)
	}
	return MailUSD(dollars)
}

func groupThousands(fixed string) string {
	whole, frac, _ := strings.Cut(fixed, ".")
	var b strings.Builder
	for i, r := range whole {
		if i > 0 && (len(whole)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	if frac != "" {
		b.WriteByte('.')
		b.WriteString(frac)
	}
	return b.String()
}

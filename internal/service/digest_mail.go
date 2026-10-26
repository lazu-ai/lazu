package service

import (
	"bytes"
	_ "embed"
	"html/template"

	"github.com/lazu-ai/lazu/internal/common"
)

// The weekly and monthly usage digests keep their own locked layout
// (brand/foundations/usage-digest-emails.md) on the ink tier of the shell.
//
//go:embed email_templates/digest.html
var digestHTML string

var digestTemplate = template.Must(template.New("digest").Parse(digestHTML))

type DigestCell struct {
	Label      string
	Value      string
	ValueColor string
	Note       string
	NoteColor  string
	LinkLabel  string
	LinkURL    string
}

type DigestAxisLabel struct {
	Text  string
	Align string
}

type DigestMetric struct {
	Label string
	Prev  string
	Cur   string
	Delta string
	Color string
}

type DigestModelRow struct {
	Name    string
	Mono    bool
	Tag     string
	PrevPct int
	CurPct  int
	Cost    string
	Delta   string
}

type DigestSourceRow struct {
	Name  string
	Count string
	Cost  string
	Delta string
}

type DigestLaunchRow struct {
	Name      string
	Text      string
	LinkLabel string
	URL       string
}

type DigestNote struct {
	Title string
	Text  string
}

// DigestView is a fully worded digest; RenderDigestMail only lays it out.
type DigestView struct {
	Lang        string
	Preheader   string
	Eyebrow     string
	LogoURL     string
	HeroLabel   string
	Hero        string
	CompactHero bool
	// LongHero shrinks a long amount on phones so it never widens the mail.
	LongHero  bool
	HeroDelta string
	HeroLine  string

	Wallet  DigestCell
	Runway  []string
	Saving  DigestCell
	Success DigestCell

	ChartTitle    string
	PeakNote      string
	ChartURL      template.URL
	ChartAlt      string
	AxisLabels    []DigestAxisLabel
	LegendSpend   string
	LegendCompare string

	CompareTitle string
	ColPrev      string
	ColCur       string
	ColDelta     string
	Metrics      []DigestMetric

	ModelsTitle  string
	Models       []DigestModelRow
	SourcesTitle string
	Sources      []DigestSourceRow

	LastWeek      *DigestNote
	LaunchesTitle string
	Launches      []DigestLaunchRow

	ActionLabel   string
	ActionURL     string
	FooterNote    string
	OptOutLead    string
	OptOutLabel   string
	OptOutTail    string
	OptOutURL     string
	SettingsLabel string
	SettingsURL   string
	SupportLabel  string
}

func RenderDigestMail(view DigestView) (string, error) {
	if view.Lang != common.MailEN {
		view.Lang = common.MailZH
	}
	if view.LogoURL == "" {
		view.LogoURL = common.MailSiteURL("/lazu-email-logo-paper.png")
	}
	var body bytes.Buffer
	err := digestTemplate.Execute(&body, view)
	return body.String(), err
}

package service

import (
	"testing"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/model"
)

func mustZone(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func TestDigestPeriodsCutOnTheReadersClock(t *testing.T) {
	sh := mustZone(t, "Asia/Shanghai")
	monday := time.Date(2026, 9, 28, 0, 0, 0, 0, sh)
	cur, prev, label := DigestPeriods(DigestWeekly, monday)
	if !cur.Start.Equal(time.Date(2026, 9, 21, 0, 0, 0, 0, sh)) || !cur.End.Equal(monday) {
		t.Fatalf("weekly period = %v – %v", cur.Start, cur.End)
	}
	if !prev.End.Equal(cur.Start) || prev.End.Sub(prev.Start) != 7*24*time.Hour {
		t.Fatalf("previous week = %v – %v", prev.Start, prev.End)
	}
	if label != "2026-W39" {
		t.Fatalf("label = %s", label)
	}

	first := time.Date(2026, 10, 1, 0, 0, 0, 0, sh)
	cur, prev, label = DigestPeriods(DigestMonthly, first)
	if label != "2026-09" || !cur.Start.Equal(time.Date(2026, 9, 1, 0, 0, 0, 0, sh)) || !prev.Start.Equal(time.Date(2026, 8, 1, 0, 0, 0, 0, sh)) {
		t.Fatalf("monthly = %s %v %v", label, cur.Start, prev.Start)
	}
}

func TestDigestPeriodLabelRoundTrips(t *testing.T) {
	la := mustZone(t, "America/Los_Angeles")
	for _, kind := range []string{DigestWeekly, DigestMonthly} {
		sendDay := digestSendDay(kind, time.Date(2026, 1, 5, 12, 0, 0, 0, la))
		_, _, label := DigestPeriods(kind, sendDay)
		back, err := digestSendDayForPeriod(kind, label, la)
		if err != nil || !back.Equal(sendDay) {
			t.Fatalf("%s %s: %v → %v (%v)", kind, label, sendDay, back, err)
		}
	}
}

func TestDueDigestsOpenAtNineThirtyLocalForTwoDays(t *testing.T) {
	sh := mustZone(t, "Asia/Shanghai")
	la := mustZone(t, "America/Los_Angeles")
	// Monday 2026-10-05 09:29 Beijing: nothing yet.
	if due := dueDigests(time.Date(2026, 10, 5, 9, 29, 0, 0, sh), sh); len(due) != 0 {
		t.Fatalf("due before 09:30: %+v", due)
	}
	due := dueDigests(time.Date(2026, 10, 5, 9, 30, 0, 0, sh), sh)
	if len(due) != 1 || due[0].Kind != DigestWeekly {
		t.Fatalf("due at 09:30 = %+v", due)
	}
	// The same instant is Sunday evening in Los Angeles: not due there.
	if due := dueDigests(time.Date(2026, 10, 5, 9, 30, 0, 0, sh), la); len(due) != 0 {
		t.Fatalf("LA due on Sunday: %+v", due)
	}
	// Two days late is past the grace window.
	if due := dueDigests(time.Date(2026, 10, 7, 9, 31, 0, 0, sh), sh); len(due) != 0 {
		t.Fatalf("due after grace: %+v", due)
	}
	// 2026-06-01 is a Monday: both kinds are due.
	if due := dueDigests(time.Date(2026, 6, 1, 10, 0, 0, 0, sh), sh); len(due) != 2 {
		t.Fatalf("monthly Monday = %+v", due)
	}
}

func TestDigestLocationFallsBackByLanguage(t *testing.T) {
	if got := DigestLocation(dto.UserSetting{Language: "zh-CN"}).String(); got != "Asia/Shanghai" {
		t.Fatalf("zh fallback = %s", got)
	}
	if got := DigestLocation(dto.UserSetting{}).String(); got != "America/Los_Angeles" {
		t.Fatalf("default fallback = %s", got)
	}
	if got := DigestLocation(dto.UserSetting{Timezone: "Europe/Berlin", Language: "zh"}).String(); got != "Europe/Berlin" {
		t.Fatalf("stored zone = %s", got)
	}
	if ValidTimezone("Local") || ValidTimezone("Mars/Base") || !ValidTimezone("Asia/Tokyo") {
		t.Fatal("ValidTimezone")
	}
}

func TestSignedMailLinksRejectTampering(t *testing.T) {
	common.CryptoSecret = "digest-test-secret"
	token := MailOptOutToken(42, MailOptOutWeekly)
	if id, kind, ok := ParseMailOptOutToken(token); !ok || id != 42 || kind != MailOptOutWeekly {
		t.Fatalf("parse = %d %s %v", id, kind, ok)
	}
	other := MailOptOutToken(43, MailOptOutWeekly)
	forged := other[:len(other)-len(token[len(token)-32:])] + token[len(token)-32:]
	if _, _, ok := ParseMailOptOutToken(forged); ok {
		t.Fatal("a signature from another user was accepted")
	}
	chart := DigestChartToken(7)
	if id, ok := ParseDigestChartToken(chart + ".png"); !ok || id != 7 {
		t.Fatalf("chart token = %d %v", id, ok)
	}
	if _, ok := ParseDigestChartToken("Nw.deadbeef.png"); ok {
		t.Fatal("unsigned chart token accepted")
	}
}

func TestDigestViewWordsChangesAndSkipsJargon(t *testing.T) {
	common.CryptoSecret = "digest-test-secret"
	sh := mustZone(t, "Asia/Shanghai")
	start := time.Date(2026, 9, 21, 0, 0, 0, 0, sh)
	snap := &DigestSnapshot{Kind: DigestWeekly, Period: "2026-W39", Timezone: "Asia/Shanghai", Lang: common.MailZH,
		Start: start.Unix(), End: start.AddDate(0, 0, 7).Unix(), PrevStart: start.AddDate(0, 0, -7).Unix(), PrevEnd: start.Unix(),
		Usage: model.DigestUsage{
			Current:  model.DigestAmounts{Spend: 44_300_000, Requests: 774, Errors: 2, Official: 63_500_000, Compared: 44_300_000},
			Previous: model.DigestAmounts{Spend: 47_100_000, Requests: 812},
			Days:     make([]model.DigestDay, 7),
			Models:   []model.DigestShare{{Key: "gpt-6.1-sol", Spend: 13_900_000, PrevSpend: 18_400_000}, {Key: "claude-sonnet-5-5", Spend: 21_600_000}},
			Wallet:   46_300_000,
		}}
	view := BuildDigestView(snap, 9, "https://lazu.ai/c.png")
	if view.Hero != "$44.30" || view.HeroDelta != "↓ 6% · 前一周 $47.10" {
		t.Fatalf("hero = %q %q", view.Hero, view.HeroDelta)
	}
	if view.HeroLine != "少花的 $2.80 主要来自 gpt-6.1-sol（−$4.50）。" {
		t.Fatalf("hero line = %q", view.HeroLine)
	}
	if view.Saving.Value != "$19.20" || view.Success.Value != "99.7%" {
		t.Fatalf("cells = %q %q", view.Saving.Value, view.Success.Value)
	}
	if view.Eyebrow != "WEEKLY · SEP 21 – 27" {
		t.Fatalf("eyebrow = %q", view.Eyebrow)
	}
	html, err := RenderDigestMail(view)
	if err != nil {
		t.Fatal(err)
	}
	for _, banned := range []string{"通道", "上游", "渠道", "权重"} {
		if contains(html, banned) {
			t.Fatalf("customer mail mentions %q", banned)
		}
	}
	if !contains(html, "/api/mail/unsubscribe?t=") {
		t.Fatal("weekly mail has no opt-out link")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestUpcomingDigestIsThePeriodRunningNow(t *testing.T) {
	sh := mustZone(t, "Asia/Shanghai")
	for _, at := range []time.Time{
		time.Date(2026, 9, 30, 22, 0, 0, 0, sh),
		time.Date(2026, 10, 1, 8, 0, 0, 0, sh),
	} {
		_, _, period := DigestPeriods(DigestMonthly, upcomingSendDay(DigestMonthly, at))
		if period != "2026-09" {
			t.Fatalf("upcoming monthly at %v = %s", at, period)
		}
	}
	// After 09:30 on the 1st, September has gone out: next up is October.
	_, _, period := DigestPeriods(DigestMonthly, upcomingSendDay(DigestMonthly, time.Date(2026, 10, 1, 10, 0, 0, 0, sh)))
	if period != "2026-10" {
		t.Fatalf("after send = %s", period)
	}
	_, _, week := DigestPeriods(DigestWeekly, upcomingSendDay(DigestWeekly, time.Date(2026, 9, 30, 22, 0, 0, 0, sh)))
	if week != "2026-W40" {
		t.Fatalf("upcoming weekly = %s", week)
	}
}

func TestDigestSavingNamesTheComparedPartAndDropsWhenNone(t *testing.T) {
	common.CryptoSecret = "digest-test-secret"
	sh := mustZone(t, "Asia/Shanghai")
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, sh)
	base := func(u model.DigestUsage) *DigestSnapshot {
		return &DigestSnapshot{Kind: DigestMonthly, Period: "2026-09", Timezone: "Asia/Shanghai", Lang: common.MailZH,
			Start: start.Unix(), End: start.AddDate(0, 1, 0).Unix(), PrevStart: start.AddDate(0, -1, 0).Unix(), PrevEnd: start.Unix(), Usage: u}
	}
	// $78.69 spent, $21.22 of it has a list price of $35.37.
	partial := BuildDigestView(base(model.DigestUsage{Current: model.DigestAmounts{Spend: 78_690_000, Requests: 10, Official: 35_370_000, Compared: 21_220_000},
		Days: make([]model.DigestDay, 30), PreviousMissing: true}), 1, "https://lazu.ai/c.png")
	if partial.Saving.Value != "$14.15" || partial.Saving.Note != "其中 $21.22 按官方价要 $35.37" {
		t.Fatalf("partial saving = %q / %q", partial.Saving.Value, partial.Saving.Note)
	}
	if partial.HeroDelta != "" || partial.Success.Note != "" {
		t.Fatalf("incomplete previous period was mentioned: %q / %q", partial.HeroDelta, partial.Success.Note)
	}
	html, _ := RenderDigestMail(partial)
	if contains(html, "数据不全") {
		t.Fatal("mail mentions incomplete data")
	}

	none := BuildDigestView(base(model.DigestUsage{Current: model.DigestAmounts{Spend: 5_000_000, Requests: 3}, Days: make([]model.DigestDay, 30)}), 1, "https://lazu.ai/c.png")
	html, _ = RenderDigestMail(none)
	if none.Saving.Value != "" || contains(html, "比官方价少付") || !contains(html, `width="50%"`) {
		t.Fatal("saving cell shown without a list price")
	}
}

func TestDigestSavingHidesBelowThreshold(t *testing.T) {
	common.CryptoSecret = "digest-test-secret"
	sh := mustZone(t, "Asia/Shanghai")
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, sh)
	view := func(official, compared int64) DigestView {
		return BuildDigestView(&DigestSnapshot{Kind: DigestMonthly, Period: "2026-09", Timezone: "Asia/Shanghai", Lang: common.MailZH,
			Start: start.Unix(), End: start.AddDate(0, 1, 0).Unix(), PrevStart: start.AddDate(0, -1, 0).Unix(), PrevEnd: start.Unix(),
			Usage: model.DigestUsage{Current: model.DigestAmounts{Spend: compared, Requests: 5, Official: official, Compared: compared}, Days: make([]model.DigestDay, 30)}}, 1, "https://lazu.ai/c.png")
	}
	if v := view(100_000_000, 90_000_000); v.Saving.Value != "" { // 10% < 15%
		t.Fatalf("10%% saving shown: %q", v.Saving.Value)
	}
	if v := view(5_000_000, 4_200_000); v.Saving.Value != "" { // $0.80 < $1
		t.Fatalf("$0.80 saving shown: %q", v.Saving.Value)
	}
	if v := view(100_000_000, 80_000_000); v.Saving.Value != "$20.00" { // 20%, $20
		t.Fatalf("20%% saving hidden: %q", v.Saving.Value)
	}
}

func TestMonthlyDigestCannotBeTurnedOff(t *testing.T) {
	common.CryptoSecret = "digest-test-secret"
	if digestOptedOut(dto.UserSetting{MonthlyDigestOff: true}, DigestMonthly) {
		t.Fatal("monthly digest honoured an opt-out")
	}
	if !digestOptedOut(dto.UserSetting{WeeklyDigestOff: true}, DigestWeekly) {
		t.Fatal("weekly opt-out ignored")
	}
	if digestHeaders(1, DigestMonthly) != nil || digestHeaders(1, DigestWeekly)["List-Unsubscribe-Post"] == "" {
		t.Fatal("unsubscribe headers on the wrong digest")
	}
	sh := mustZone(t, "Asia/Shanghai")
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, sh)
	v := BuildDigestView(&DigestSnapshot{Kind: DigestMonthly, Period: "2026-09", Timezone: "Asia/Shanghai", Lang: common.MailZH,
		Start: start.Unix(), End: start.AddDate(0, 1, 0).Unix(), PrevStart: start.AddDate(0, -1, 0).Unix(), PrevEnd: start.Unix(),
		Usage: model.DigestUsage{Current: model.DigestAmounts{Spend: 5_000_000, Requests: 1}, Days: make([]model.DigestDay, 30)}}, 1, "https://lazu.ai/c.png")
	html, _ := RenderDigestMail(v)
	if v.OptOutURL != "" || contains(html, "/api/mail/unsubscribe") {
		t.Fatal("monthly digest carries an opt-out link")
	}
}

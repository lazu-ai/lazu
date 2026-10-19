package service

import (
	"context"
	"crypto/hmac"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/publicsite"
)

// Usage digests (brand/foundations/usage-digest-emails.md): a weekly and a
// monthly mail to every account that has paid, cut and sent in the reader's
// own time zone.
const (
	DigestWeekly  = "weekly"
	DigestMonthly = "monthly"

	digestTemplateVersion = "digest-v2"
	digestSendAt          = 9*time.Hour + 30*time.Minute
	// A digest that could not go out on time (the worker was down) is still
	// sent within this window, then the period is let go.
	digestGrace = 48 * time.Hour

	digestSkipOptedOut       = "opted_out"
	digestSkipBelowMinimum   = "below_minimum"
	digestSkipMonthlySameDay = "monthly_same_day"
	digestSkipIncomplete     = "incomplete_data"
)

// DigestSnapshot is everything one digest shows, stored with the archived
// mail so the chart and any later look at it read the same numbers.
type DigestSnapshot struct {
	Kind      string               `json:"kind"`
	Period    string               `json:"period"`
	Timezone  string               `json:"timezone"`
	Lang      string               `json:"lang"`
	Start     int64                `json:"start"`
	End       int64                `json:"end"`
	PrevStart int64                `json:"prev_start"`
	PrevEnd   int64                `json:"prev_end"`
	Usage     model.DigestUsage    `json:"usage"`
	Launches  []model.DigestLaunch `json:"launches,omitempty"`
	LastWeek  *DigestWeekLine      `json:"last_week,omitempty"`
	Chart     DigestChart          `json:"chart"`
}

// DigestWeekLine is the short "last week" paragraph a monthly digest carries
// when the 1st is a Monday and the weekly one is folded into it.
type DigestWeekLine struct {
	Start     int64 `json:"start"`
	End       int64 `json:"end"`
	Spend     int64 `json:"spend"`
	PrevSpend int64 `json:"prev_spend"`
}

// DigestLocation is the reader's zone: the stored one, else Beijing for a
// Chinese reader and US Pacific for everyone else.
func DigestLocation(setting dto.UserSetting) *time.Location {
	if name := strings.TrimSpace(setting.Timezone); name != "" {
		if loc, err := time.LoadLocation(name); err == nil {
			return loc
		}
	}
	name := "America/Los_Angeles"
	if common.MailLang(setting.Language) == common.MailZH {
		name = "Asia/Shanghai"
	}
	if loc, err := time.LoadLocation(name); err == nil {
		return loc
	}
	return time.UTC
}

// ValidTimezone reports whether an IANA name is known to this server.
func ValidTimezone(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 64 || strings.EqualFold(name, "local") {
		return false
	}
	_, err := time.LoadLocation(name)
	return err == nil
}

func localMidnight(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

// digestSendDay is the local day a digest of this kind is sent on for the
// period containing now's previous week or month.
func digestSendDay(kind string, now time.Time) time.Time {
	day := localMidnight(now)
	if kind == DigestMonthly {
		return time.Date(day.Year(), day.Month(), 1, 0, 0, 0, 0, day.Location())
	}
	return day.AddDate(0, 0, -((int(day.Weekday()) + 6) % 7))
}

// DigestPeriods returns the period a digest sent on sendDay covers, the one
// before it, and the period's label (2026-W39, 2026-09).
func DigestPeriods(kind string, sendDay time.Time) (model.DigestPeriod, model.DigestPeriod, string) {
	if kind == DigestMonthly {
		end := time.Date(sendDay.Year(), sendDay.Month(), 1, 0, 0, 0, 0, sendDay.Location())
		start := end.AddDate(0, -1, 0)
		prevStart := start.AddDate(0, -1, 0)
		return model.DigestPeriod{Start: start, End: end}, model.DigestPeriod{Start: prevStart, End: start}, start.Format("2006-01")
	}
	end := localMidnight(sendDay)
	start := end.AddDate(0, 0, -7)
	year, week := start.ISOWeek()
	return model.DigestPeriod{Start: start, End: end}, model.DigestPeriod{Start: start.AddDate(0, 0, -7), End: start}, fmt.Sprintf("%d-W%02d", year, week)
}

func digestRefKey(kind string, userID int, period string) string {
	return fmt.Sprintf("digest:%s:%d:%s", kind, userID, period)
}

func digestMailKind(kind string) string {
	if kind == DigestMonthly {
		return common.MailKindMonthlyDigest
	}
	return common.MailKindWeeklyDigest
}

func digestOptedOut(setting dto.UserSetting, kind string) bool {
	if kind == DigestMonthly {
		// The monthly digest is an account statement: it always goes out.
		return false
	}
	return setting.WeeklyDigestOff
}

// BuildDigestSnapshot gathers one digest for a payer. sendDay is the local
// day it is (or would have been) sent on.
func BuildDigestSnapshot(userID int, setting dto.UserSetting, kind string, sendDay time.Time) (*DigestSnapshot, error) {
	loc := sendDay.Location()
	current, previous, label := DigestPeriods(kind, sendDay)
	usage, err := model.CollectDigestUsage(userID, current, previous, kind == DigestWeekly)
	if err != nil {
		return nil, err
	}
	snap := &DigestSnapshot{
		Kind: kind, Period: label, Timezone: loc.String(), Lang: common.MailLang(setting.Language),
		Start: current.Start.Unix(), End: current.End.Unix(), PrevStart: previous.Start.Unix(), PrevEnd: previous.End.Unix(),
		Usage: *usage,
	}
	used := make([]string, 0, len(usage.Models))
	for _, m := range usage.Models {
		used = append(used, m.Key)
	}
	limit := 3
	if kind == DigestMonthly {
		limit = 5
	}
	snap.Launches = model.DigestLaunches(current.Start, current.End, used, limit)

	chart := DigestChart{Peak: -1}
	peak := int64(-1)
	for i, day := range usage.Days {
		chart.Spend = append(chart.Spend, microToDollars(day.Spend))
		chart.Tokens = append(chart.Tokens, float64(day.Tokens))
		if day.Spend > peak {
			peak, chart.Peak = day.Spend, i
		}
	}
	if usage.PreviousMissing {
		// No baseline: the previous period is not fully on record.
	} else if kind == DigestWeekly {
		for _, v := range usage.PrevDays {
			chart.Prev = append(chart.Prev, microToDollars(v))
		}
	} else if days := previous.End.Sub(previous.Start).Hours() / 24; days > 0 && usage.Previous.Spend > 0 {
		chart.RefAvg = microToDollars(usage.Previous.Spend) / math.Round(days)
	}
	snap.Chart = chart
	return snap, nil
}

func microToDollars(v int64) float64 { return float64(v) / 1e6 }

// ----- wording ---------------------------------------------------------------

var zhWeekdays = []string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}
var zhWeekdayShort = []string{"日", "一", "二", "三", "四", "五", "六"}

func digestMonthName(lang string, t time.Time) string {
	if lang == common.MailEN {
		return t.Month().String()
	}
	return fmt.Sprintf("%d 月", int(t.Month()))
}

func digestPct(cur, prev float64) (string, float64) {
	if prev <= 0 {
		return "—", 0
	}
	change := (cur - prev) / prev * 100
	if math.Abs(change) < 0.5 {
		return "±0%", 0
	}
	arrow := "↑"
	if change < 0 {
		arrow = "↓"
	}
	return fmt.Sprintf("%s %d%%", arrow, int(math.Round(math.Abs(change)))), change
}

func digestSignedUSD(delta int64) string {
	if delta == 0 {
		return "$0.00"
	}
	if delta > 0 {
		return "+" + common.MailUSD(microToDollars(delta))
	}
	return "−" + common.MailUSD(microToDollars(-delta))
}

func digestCount(n int64) string {
	switch {
	case n >= 1_000_000_000:
		return trimFloat(float64(n)/1e9) + "B"
	case n >= 1_000_000:
		return trimFloat(float64(n)/1e6) + "M"
	case n >= 10_000:
		return trimFloat(float64(n)/1e3) + "k"
	}
	return groupInt(n)
}

func trimFloat(v float64) string {
	s := strconv.FormatFloat(v, 'f', 1, 64)
	return strings.TrimSuffix(s, ".0")
}

func groupInt(n int64) string {
	s := strconv.FormatInt(n, 10)
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func digestSourceName(key, lang string) string {
	switch key {
	case "claude-code":
		return "Claude Code"
	case "codex":
		return "Codex"
	case "opencode":
		return "OpenCode"
	case "gemini-cli":
		return "Gemini CLI"
	case "qwen-code":
		return "Qwen Code"
	case "kilo-code":
		return "Kilo Code"
	case "roo-code":
		return "Roo Code"
	case "crush":
		return "Crush"
	case "openclaw":
		return "OpenClaw"
	}
	if name, ok := common.ExtensionClientName(key); ok {
		return name
	}
	return common.MailText(lang, "直接调用 API", "Direct API")
}

func digestTZName(lang string, loc string) string {
	if loc == "Asia/Shanghai" && lang == common.MailZH {
		return "北京时间"
	}
	return loc
}

// DigestSubject is the mail's subject line.
func DigestSubject(snap *DigestSnapshot) string {
	lang := snap.Lang
	spend := common.MailUSD(microToDollars(snap.Usage.Current.Spend))
	if snap.Kind == DigestMonthly {
		month := time.Unix(snap.Start, 0).In(digestLoc(snap))
		return common.MailText(lang, fmt.Sprintf("%s你在 Lazu 花了 %s", digestMonthName(lang, month), spend), fmt.Sprintf("You spent %s on Lazu in %s", spend, month.Month()))
	}
	return common.MailText(lang, "上周你在 Lazu 花了 "+spend, "You spent "+spend+" on Lazu last week")
}

func digestLoc(snap *DigestSnapshot) *time.Location {
	if loc, err := time.LoadLocation(snap.Timezone); err == nil {
		return loc
	}
	return time.UTC
}

// BuildDigestView words a snapshot for one reader.
func BuildDigestView(snap *DigestSnapshot, userID int, chartURL template.URL) common.DigestView {
	lang := snap.Lang
	t := func(zh, en string) string { return common.MailText(lang, zh, en) }
	loc := digestLoc(snap)
	start := time.Unix(snap.Start, 0).In(loc)
	lastDay := time.Unix(snap.End, 0).In(loc).AddDate(0, 0, -1)
	prevStart := time.Unix(snap.PrevStart, 0).In(loc)
	cur, prev := snap.Usage.Current, snap.Usage.Previous
	weekly := snap.Kind == DigestWeekly
	v := common.DigestView{Lang: lang, ChartURL: chartURL}
	surface := model.GetUserHomeSurface(userID)
	link := func(to model.SurfaceLink) string { return common.MailSiteURL(model.UserSurfacePath(surface, to)) }

	prevName := t("前一周", "Previous week")
	curName := t("上周", "Last week")
	if !weekly {
		prevName, curName = digestMonthName(lang, prevStart), digestMonthName(lang, start)
		if lang == common.MailEN {
			prevName, curName = prevStart.Format("Jan"), start.Format("Jan")
		}
	}

	if weekly {
		span := strings.ToUpper(start.Format("Jan 2")) + " – " + strings.ToUpper(lastDay.Format("2"))
		if lastDay.Month() != start.Month() {
			span = strings.ToUpper(start.Format("Jan 2")) + " – " + strings.ToUpper(lastDay.Format("Jan 2"))
		}
		v.Eyebrow = "WEEKLY · " + span
		v.HeroLabel = t("上周 API 花费", "Last week's API spend")
	} else {
		v.Eyebrow = "MONTHLY · " + strings.ToUpper(start.Format("Jan 2006"))
		v.HeroLabel = t(digestMonthName(lang, start)+" API 花费", start.Month().String()+" API spend")
	}
	v.Hero = common.MailUSD(microToDollars(cur.Spend))
	v.CompactHero = cur.Spend >= 10_000*1_000_000
	v.LongHero = len(v.Hero) >= 9
	pct, _ := digestPct(float64(cur.Spend), float64(prev.Spend))
	prevMissing := snap.Usage.PreviousMissing
	if prevMissing {
		// The previous period is not fully on record: say nothing about it.
	} else if prev.Spend > 0 {
		prevLabel := t("前一周", "previous week")
		if !weekly {
			prevLabel = digestMonthName(lang, prevStart)
		}
		v.HeroDelta = pct + " · " + prevLabel + " " + common.MailUSD(microToDollars(prev.Spend))
	} else {
		v.HeroDelta = t("上一期没有用量。", "No usage the period before.")
	}
	if !prevMissing {
		v.HeroLine = digestHeroLine(snap, lang)
	}
	v.Preheader = v.HeroDelta
	if v.HeroLine != "" {
		v.Preheader += " " + v.HeroLine
	}

	// Wallet and how long it lasts at this period's pace.
	days := snap.End - snap.Start
	perDay := float64(cur.Spend) / (float64(days) / 86400)
	v.Wallet = common.DigestCell{Label: t("钱包余额", "Wallet balance"), Value: common.MailUSD(microToDollars(snap.Usage.Wallet)), NoteColor: "#5f646d",
		LinkLabel: t("去充值", "Add credit"), LinkURL: link(model.SurfaceLinkWallet)}
	if perDay > 0 {
		runway := float64(snap.Usage.Wallet) / perDay
		whole := int(math.Floor(runway))
		switch {
		case whole > 90:
			v.Wallet.Note = t("按这个速度够用 90 天以上", "Over 90 days at this pace")
		case weekly:
			v.Wallet.Note = t(fmt.Sprintf("约够 %d 天", whole), fmt.Sprintf("About %d days left", whole))
		default:
			v.Wallet.Note = t(fmt.Sprintf("按 %s的速度约够 %d 天", digestMonthName(lang, start), whole), fmt.Sprintf("About %d days at %s's pace", whole, start.Month()))
		}
		filled := min(whole, 14)
		fill := "#23262d"
		if runway < 7 {
			fill = "#b3821b"
			v.Wallet.NoteColor = "#8f6813"
		}
		for i := 0; i < 14; i++ {
			if i < filled {
				v.Runway = append(v.Runway, fill)
			} else {
				v.Runway = append(v.Runway, "#e4e3df")
			}
		}
	}

	// Below list price: only requests whose receipt carries a fair list price
	// can be compared. When some cannot, the note names the part that was
	// compared; with nothing to compare, the cell is left out.
	v.Saving = common.DigestCell{Label: t("比官方价少付", "Below list price"), ValueColor: "#3f7957"}
	settings := DigestSettingsNow()
	if saving := cur.Official - cur.Compared; cur.Official > 0 && saving > 0 &&
		float64(saving) >= settings.MinSavingUSD*1e6 && float64(saving)*100 >= settings.MinSavingPct*float64(cur.Official) {
		v.Saving.Value = common.MailUSD(microToDollars(saving))
		official, compared := common.MailUSD(microToDollars(cur.Official)), common.MailUSD(microToDollars(cur.Compared))
		share := int(math.Round(float64(saving) / float64(cur.Official) * 100))
		if cur.Compared*100 >= cur.Spend*99 {
			v.Saving.Note = t(fmt.Sprintf("官方价 %s · 省\u00a0%d%%", official, share), fmt.Sprintf("List price %s · %d%% less", official, share))
		} else {
			v.Saving.Note = t(fmt.Sprintf("其中 %s 按官方价要 %s", compared, official), fmt.Sprintf("%s of it would cost %s at list price", compared, official))
		}
	}

	v.Success = common.DigestCell{Label: t("成功率", "Success rate"), Value: digestRate(cur)}
	if prevMissing {
		// Nothing to compare with.
	} else if prev.Requests+prev.Errors > 0 {
		v.Success.Note = prevName + " " + digestRate(prev)
		if lang == common.MailEN && weekly {
			v.Success.Note = "Previous week " + digestRate(prev)
		}
	} else {
		v.Success.Note = t("上一期没有请求", "No requests the period before")
	}

	// Chart.
	v.ChartTitle = t("每天", "Daily")
	v.LegendSpend = t("花费", "Spend")
	if prevMissing {
		v.LegendCompare = ""
	} else if weekly {
		v.LegendCompare = t("前一周", "Previous week")
	} else {
		v.LegendCompare = t(digestMonthName(lang, prevStart)+"日均", prevStart.Format("Jan")+" daily avg")
	}
	if p := snap.Chart.Peak; p >= 0 && p < len(snap.Usage.Days) && snap.Usage.Days[p].Spend > 0 {
		day := start.AddDate(0, 0, p)
		amount := common.MailUSD(microToDollars(snap.Usage.Days[p].Spend))
		if weekly {
			v.PeakNote = t("最高 "+zhWeekdays[day.Weekday()]+" "+amount, "Peak "+day.Format("Mon")+" "+amount)
		} else {
			v.PeakNote = t(fmt.Sprintf("最高 %d 月 %d 日 %s", int(day.Month()), day.Day(), amount), "Peak "+day.Format("Jan 2")+" "+amount)
		}
	}
	v.ChartAlt = t("每天的花费与 Tokens", "Daily spend and tokens")
	if weekly {
		for i := range snap.Usage.Days {
			day := start.AddDate(0, 0, i)
			v.AxisLabels = append(v.AxisLabels, common.DigestAxisLabel{Align: "center",
				Text: t(zhWeekdayShort[day.Weekday()]+" "+day.Format("02"), day.Format("Mon 2"))})
		}
	} else {
		n := len(snap.Usage.Days)
		marks := []int{0, 7, 14, 21, n - 1}
		for i, m := range marks {
			align := "center"
			if i == 0 {
				align = "left"
			} else if i == len(marks)-1 {
				align = "right"
			}
			v.AxisLabels = append(v.AxisLabels, common.DigestAxisLabel{Align: align, Text: start.AddDate(0, 0, m).Format("01-02")})
		}
	}

	// Comparison table.
	v.CompareTitle = t("和前一周比", "Against the week before")
	if !weekly {
		v.CompareTitle = t("和 "+digestMonthName(lang, prevStart)+"比", "Against "+prevStart.Month().String())
	}
	v.ColPrev, v.ColCur, v.ColDelta = prevName, curName, t("变化", "Change")
	ink, good := "#22242a", "#3f7957"
	metric := func(label string, a, b float64, format func(float64) string, betterUp bool) common.DigestMetric {
		delta, change := digestPct(b, a)
		color := ink
		if betterUp && change > 0 {
			color = good
		}
		if a <= 0 && b <= 0 {
			delta = "—"
		}
		return common.DigestMetric{Label: label, Prev: format(a), Cur: format(b), Delta: delta, Color: color}
	}
	usd := func(x float64) string { return common.MailUSD(x / 1e6) }
	count := func(x float64) string { return digestCount(int64(x)) }
	avg := func(u model.DigestAmounts) float64 {
		if u.Requests == 0 {
			return 0
		}
		return float64(u.Spend) / float64(u.Requests)
	}
	v.Metrics = []common.DigestMetric{
		metric(t("花费", "Spend"), float64(prev.Spend), float64(cur.Spend), usd, false),
		metric(t("请求", "Requests"), float64(prev.Requests), float64(cur.Requests), count, false),
		metric("Tokens", float64(prev.Tokens), float64(cur.Tokens), count, false),
		metric(t("其中缓存命中", "of which cache hits"), float64(prev.Cache), float64(cur.Cache), count, true),
		metric(t("平均每次请求", "Average per request"), avg(prev), avg(cur), func(x float64) string {
			if x <= 0 {
				return "—"
			}
			return fmt.Sprintf("$%.3f", x/1e6)
		}, false),
	}
	sessions := metric(t("Agent 会话", "Agent sessions"), float64(prev.Sessions), float64(cur.Sessions), count, false)
	if diff := cur.Sessions - prev.Sessions; prev.Sessions > 0 || cur.Sessions > 0 {
		switch {
		case diff > 0:
			sessions.Delta = fmt.Sprintf("↑ %d", diff)
		case diff < 0:
			sessions.Delta = fmt.Sprintf("↓ %d", -diff)
		default:
			sessions.Delta = "±0"
		}
	}
	v.Metrics = append(v.Metrics, sessions)
	if prevMissing {
		for i := range v.Metrics {
			v.Metrics[i].Prev, v.Metrics[i].Delta, v.Metrics[i].Color = "—", "—", ink
		}
	}

	// Models: the five that mattered in either period, the rest folded.
	v.ModelsTitle = t("模型", "Models")
	models := snap.Usage.Models
	ranked := append([]model.DigestShare(nil), models...)
	for i := 1; i < len(ranked); i++ {
		for j := i; j > 0 && max(ranked[j].Spend, ranked[j].PrevSpend) > max(ranked[j-1].Spend, ranked[j-1].PrevSpend); j-- {
			ranked[j], ranked[j-1] = ranked[j-1], ranked[j]
		}
	}
	shown := ranked
	var rest model.DigestShare
	if len(ranked) > 5 {
		shown = ranked[:4]
		for _, m := range ranked[4:] {
			rest.Spend += m.Spend
			rest.PrevSpend += m.PrevSpend
		}
		rest.Key = t(fmt.Sprintf("其他 %d 个模型", len(ranked)-4), fmt.Sprintf("%d other models", len(ranked)-4))
		shown = append(shown, rest)
	}
	scale := int64(1)
	for _, m := range shown {
		scale = max(scale, m.Spend, m.PrevSpend)
	}
	for i, m := range shown {
		row := common.DigestModelRow{Name: m.Key, Mono: true, Cost: common.MailUSD(microToDollars(m.Spend)), Delta: digestSignedUSD(m.Spend - m.PrevSpend),
			PrevPct: barPct(m.PrevSpend, scale), CurPct: barPct(m.Spend, scale)}
		if m.Key == "" {
			row.Name = t("未知模型", "Unknown model")
		}
		if prevMissing {
			row.Delta, row.PrevPct = "—", 0
		}
		isRest := len(ranked) > 5 && i == len(shown)-1
		if isRest || m.Key == "" {
			row.Mono = false
		}
		if prevMissing {
			// Without a full previous period, "new" and "not used" are guesses.
		} else if !isRest && m.PrevSpend == 0 && m.Spend > 0 && prev.Spend > 0 {
			row.Tag = t("新用", "New")
		} else if !isRest && m.Spend == 0 && m.PrevSpend > 0 {
			row.Tag = t("没再用", "Not used")
		}
		v.Models = append(v.Models, row)
	}

	// Sources.
	v.SourcesTitle = t("调用来源", "Where calls came from")
	for i, s := range snap.Usage.Sources {
		if i >= 4 || (s.Spend == 0 && s.Sessions == 0) {
			continue
		}
		row := common.DigestSourceRow{Name: digestSourceName(s.Key, lang), Cost: common.MailUSD(microToDollars(s.Spend)), Delta: digestSignedUSD(s.Spend - s.PrevSpend)}
		if prevMissing {
			row.Delta = "—"
		}
		if s.Sessions > 0 {
			row.Count = t(fmt.Sprintf("%s 次会话", groupInt(s.Sessions)), fmt.Sprintf("%s sessions", groupInt(s.Sessions)))
		} else {
			row.Count = t(fmt.Sprintf("%s 次请求", groupInt(s.Requests)), fmt.Sprintf("%s requests", groupInt(s.Requests)))
		}
		v.Sources = append(v.Sources, row)
	}

	if lw := snap.LastWeek; lw != nil {
		ws, we := time.Unix(lw.Start, 0).In(loc), time.Unix(lw.End, 0).In(loc).AddDate(0, 0, -1)
		change, _ := digestPct(float64(lw.Spend), float64(lw.PrevSpend))
		v.LastWeek = &common.DigestNote{Title: t("上周", "Last week"),
			Text: t(fmt.Sprintf("%d 月 %d 日 – %d 月 %d 日花了 %s，和前一周比 %s。", int(ws.Month()), ws.Day(), int(we.Month()), we.Day(), common.MailUSD(microToDollars(lw.Spend)), change),
				fmt.Sprintf("%s – %s: %s, %s on the week before.", ws.Format("Jan 2"), we.Format("Jan 2"), common.MailUSD(microToDollars(lw.Spend)), change))}
	}

	if len(snap.Launches) > 0 {
		v.LaunchesTitle = t("上周新上线", "New on Lazu last week")
		if !weekly {
			v.LaunchesTitle = t(digestMonthName(lang, start)+"新上线", "New on Lazu in "+start.Month().String())
		}
		locale := "en"
		if lang == common.MailZH {
			locale = "zh"
		}
		for _, l := range snap.Launches {
			text := t("已上线，可以直接调用。", "Available now.")
			if l.Input != nil && l.Output != nil {
				text = t(fmt.Sprintf("输入 %s / 输出 %s，每百万 tokens。", perMtok(*l.Input), perMtok(*l.Output)),
					fmt.Sprintf("%s in / %s out per million tokens.", perMtok(*l.Input), perMtok(*l.Output)))
			}
			v.Launches = append(v.Launches, common.DigestLaunchRow{Name: l.Model, Text: text, LinkLabel: t("看价格", "Pricing"),
				URL: common.MailSiteURL(publicsite.LocalizedPath("/models/"+publicsite.MakerSlug(l.Maker)+"/"+l.Model, locale))})
		}
	}

	if weekly {
		v.ActionLabel = t("查看上周用量", "View last week's usage")
	} else {
		v.ActionLabel = t("查看 "+digestMonthName(lang, start)+"用量", "View "+start.Month().String()+" usage")
	}
	v.ActionURL = link(model.SurfaceLinkUsage)
	tz := digestTZName(lang, snap.Timezone)
	if weekly {
		v.FooterNote = t("周报每周一 09:30（"+tz+"）发送，统计上周一 00:00 到周日 24:00。", "Sent Mondays at 09:30 ("+tz+"), covering Monday 00:00 to Sunday 24:00.")
		v.OptOutLead = t("不想每周收到？", "Don't want this every week? ")
		v.OptOutLabel = t("关闭周报", "Turn off the weekly digest")
		v.OptOutTail = t("，点一下就生效，月报照常发送", "; the monthly one still comes")
		v.OptOutURL = MailUnsubscribeURL(userID, MailOptOutWeekly)
	} else {
		v.FooterNote = t("月报每月 1 日 09:30（"+tz+"）发送，统计上个自然月。", "Sent on the 1st at 09:30 ("+tz+"), covering the previous calendar month.")
	}
	v.SettingsLabel = t("通知设置", "Notification settings")
	v.SettingsURL = link(model.SurfaceLinkSettings)
	v.SupportLabel = t("联系支持", "Contact support")
	return v
}

func perMtok(v float64) string {
	return "$" + strconv.FormatFloat(v, 'f', -1, 64)
}

func barPct(v, scale int64) int {
	if v <= 0 || scale <= 0 {
		return 0
	}
	return max(1, int(math.Round(float64(v)/float64(scale)*100)))
}

func digestRate(u model.DigestAmounts) string {
	total := u.Requests + u.Errors
	if total == 0 {
		return "—"
	}
	rate := float64(u.Requests) / float64(total) * 100
	if u.Errors == 0 {
		return "100%"
	}
	if rate >= 99.95 {
		return "99.9%"
	}
	return strconv.FormatFloat(math.Floor(rate*10)/10, 'f', 1, 64) + "%"
}

// digestHeroLine names the model that moved the total most.
func digestHeroLine(snap *DigestSnapshot, lang string) string {
	cur, prev := snap.Usage.Current.Spend, snap.Usage.Previous.Spend
	diff := cur - prev
	if prev <= 0 || absInt(diff) < 500_000 {
		return ""
	}
	var best model.DigestShare
	for _, m := range snap.Usage.Models {
		d := m.Spend - m.PrevSpend
		if (diff > 0) == (d > 0) && absInt(d) > absInt(best.Spend-best.PrevSpend) {
			best = m
		}
	}
	d := best.Spend - best.PrevSpend
	if best.Key == "" || absInt(d) < 100_000 {
		return ""
	}
	total := common.MailUSD(microToDollars(absInt(diff)))
	if diff > 0 {
		return common.MailText(lang, fmt.Sprintf("多出的 %s 主要在 %s（%s）。", total, best.Key, digestSignedUSD(d)),
			fmt.Sprintf("Most of the extra %s went to %s (%s).", total, best.Key, digestSignedUSD(d)))
	}
	return common.MailText(lang, fmt.Sprintf("少花的 %s 主要来自 %s（%s）。", total, best.Key, digestSignedUSD(d)),
		fmt.Sprintf("Most of the %s drop came from %s (%s).", total, best.Key, digestSignedUSD(d)))
}

func absInt(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

// ----- chart links -------------------------------------------------------------

// DigestChartToken signs an archive row id for the public chart image.
func DigestChartToken(deliveryID uint64) string {
	payload := base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatUint(deliveryID, 10)))
	return payload + "." + common.GenerateHMAC("lazu/digest-chart/v1|" + payload)[:24]
}

func ParseDigestChartToken(token string) (uint64, bool) {
	payload, signature, ok := strings.Cut(strings.TrimSuffix(token, ".png"), ".")
	if !ok || !hmac.Equal([]byte(signature), []byte(common.GenerateHMAC("lazu/digest-chart/v1|" + payload)[:24])) {
		return 0, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return 0, false
	}
	id, err := strconv.ParseUint(string(raw), 10, 64)
	return id, err == nil && id > 0
}

func digestChartURL(deliveryID uint64) template.URL {
	return template.URL(common.MailSiteURL("/api/mail/chart/" + DigestChartToken(deliveryID) + ".png"))
}

// DigestChartPNG draws the chart stored with an archived digest.
func DigestChartPNG(deliveryID uint64) ([]byte, error) {
	row, err := model.GetMailDelivery(deliveryID)
	if err != nil {
		return nil, err
	}
	raw, err := row.SnapshotJSON()
	if err != nil || raw == "" {
		return nil, errors.New("digest snapshot is not available")
	}
	var snap DigestSnapshot
	if err := json.Unmarshal([]byte(raw), &snap); err != nil {
		return nil, err
	}
	return RenderDigestChart(snap.Chart)
}

// ----- sending -----------------------------------------------------------------

// sendDigest renders a snapshot into a claimed archive row and sends it.
func sendDigest(row *model.MailDelivery, snap *DigestSnapshot, userID int, headers map[string]string) error {
	view := BuildDigestView(snap, userID, digestChartURL(row.ID))
	html, err := common.RenderDigestMail(view)
	if err != nil {
		_ = model.FailMailDelivery(row.ID, err)
		return err
	}
	encoded, err := json.Marshal(snap)
	if err != nil {
		_ = model.FailMailDelivery(row.ID, err)
		return err
	}
	subject := DigestSubject(snap)
	if row.Kind == common.MailKindCopy {
		subject = "[Lazu copy] " + subject
	}
	if err := model.SaveMailDeliveryContent(row.ID, subject, html, string(encoded)); err != nil {
		_ = model.FailMailDelivery(row.ID, err)
		return err
	}
	return common.SendMail(common.MailMeta{Kind: row.Kind, UserID: row.UserID, DeliveryID: row.ID, Template: digestTemplateVersion,
		Lang: snap.Lang, Period: snap.Period, Headers: headers}, subject, row.Receiver, html)
}

// upcomingSendDay is the send day of the next digest of a kind that has not
// gone out yet: its period is the one running now.
func upcomingSendDay(kind string, local time.Time) time.Time {
	day := digestSendDay(kind, local)
	if !local.Before(day.Add(digestSendAt)) {
		if kind == DigestMonthly {
			return day.AddDate(0, 1, 0)
		}
		return day.AddDate(0, 0, 7)
	}
	return day
}

// RenderDigestPreview renders a digest for a user without sending or
// archiving it: the most recent complete period, or with upcoming the
// period running now (figures so far). The chart is inlined.
func RenderDigestPreview(userID int, kind string, now time.Time, lang string, upcoming bool) (string, string, error) {
	user, err := model.GetUserById(userID, true)
	if err != nil {
		return "", "", err
	}
	setting := user.GetSetting()
	if lang != "" {
		setting.Language = lang
	}
	loc := DigestLocation(setting)
	sendDay := digestSendDay(kind, now.In(loc))
	if upcoming {
		sendDay = upcomingSendDay(kind, now.In(loc))
	}
	snap, err := BuildDigestSnapshot(userID, setting, kind, sendDay)
	if err != nil {
		return "", "", err
	}
	png, err := RenderDigestChart(snap.Chart)
	if err != nil {
		return "", "", err
	}
	chart := template.URL("data:image/png;base64," + base64.StdEncoding.EncodeToString(png))
	html, err := common.RenderDigestMail(BuildDigestView(snap, userID, chart))
	return DigestSubject(snap), html, err
}

// SendDigestCopy sends the most recent complete digest of a user to someone
// else (an operator's "send one to me"). It is archived as a copy.
func SendDigestCopy(userID int, kind string, receiver string, now time.Time) error {
	user, err := model.GetUserById(userID, true)
	if err != nil {
		return err
	}
	setting := user.GetSetting()
	loc := DigestLocation(setting)
	snap, err := BuildDigestSnapshot(userID, setting, kind, digestSendDay(kind, now.In(loc)))
	if err != nil {
		return err
	}
	row := &model.MailDelivery{Kind: common.MailKindCopy, UserID: userID, Receiver: receiver, Period: snap.Period,
		Template: digestTemplateVersion, Lang: snap.Lang, Timezone: snap.Timezone, Status: model.MailStatusGenerating, CreatedAt: now.Unix()}
	if err := model.TelemetryDB().Create(row).Error; err != nil {
		return err
	}
	return sendDigest(row, snap, userID, nil)
}

// RegenerateDigest rebuilds an archived digest from today's data with the
// current template and sends it again as a new archive row.
func RegenerateDigest(deliveryID uint64, now time.Time) error {
	old, err := model.GetMailDelivery(deliveryID)
	if err != nil {
		return err
	}
	if old.Status == model.MailStatusDraft {
		return errors.New("regenerate drafts from the usage digest page instead")
	}
	kind := DigestWeekly
	switch old.Kind {
	case common.MailKindMonthlyDigest:
		kind = DigestMonthly
	case common.MailKindWeeklyDigest:
	default:
		return errors.New("only usage digests can be regenerated")
	}
	user, err := model.GetUserById(old.UserID, true)
	if err != nil {
		return err
	}
	setting := user.GetSetting()
	loc := DigestLocation(setting)
	if old.Timezone != "" {
		if l, err := time.LoadLocation(old.Timezone); err == nil {
			loc = l
		}
	}
	sendDay, err := digestSendDayForPeriod(kind, old.Period, loc)
	if err != nil {
		return err
	}
	snap, err := BuildDigestSnapshot(old.UserID, setting, kind, sendDay)
	if err != nil {
		return err
	}
	row := &model.MailDelivery{Kind: old.Kind, UserID: old.UserID, Receiver: user.Email, Period: snap.Period, Template: digestTemplateVersion,
		Lang: snap.Lang, Timezone: snap.Timezone, Status: model.MailStatusGenerating, CreatedAt: now.Unix()}
	if err := model.TelemetryDB().Create(row).Error; err != nil {
		return err
	}
	return sendDigest(row, snap, old.UserID, digestHeaders(old.UserID, kind))
}

// digestHeaders carries the one-click opt-out for the weekly digest only;
// the monthly digest cannot be turned off.
func digestHeaders(userID int, kind string) map[string]string {
	if kind == DigestMonthly {
		return nil
	}
	return MailUnsubscribeHeaders(userID, MailOptOutWeekly)
}

func digestSendDayForPeriod(kind, period string, loc *time.Location) (time.Time, error) {
	if kind == DigestMonthly {
		month, err := time.ParseInLocation("2006-01", period, loc)
		if err != nil {
			return time.Time{}, err
		}
		return month.AddDate(0, 1, 0), nil
	}
	var year, week int
	if _, err := fmt.Sscanf(period, "%d-W%d", &year, &week); err != nil {
		return time.Time{}, err
	}
	// ISO week 1 contains January 4th.
	jan4 := time.Date(year, 1, 4, 0, 0, 0, 0, loc)
	monday := jan4.AddDate(0, 0, -((int(jan4.Weekday())+6)%7)+(week-1)*7)
	return monday.AddDate(0, 0, 7), nil
}

// ResendArchivedMail sends an archived body again, to its reader or to
// someone else, as a new archive row.
func ResendArchivedMail(deliveryID uint64, receiver string) error {
	row, err := model.GetMailDelivery(deliveryID)
	if err != nil {
		return err
	}
	html, err := row.HTML()
	if err != nil || html == "" {
		return errors.New("this mail's body is not archived")
	}
	kind, subject := row.Kind, row.Subject
	if receiver == "" {
		if row.Status == model.MailStatusDraft {
			return errors.New("a draft is never sent to its reader")
		}
		receiver = row.Receiver
	} else {
		kind, subject = common.MailKindCopy, "[Lazu copy] "+strings.TrimPrefix(row.Subject, "[Lazu copy] ")
	}
	return common.SendMail(common.MailMeta{Kind: kind, UserID: row.UserID, Template: row.Template, Lang: row.Lang, Period: row.Period}, subject, receiver, html)
}

// dueDigest is one digest whose send time has come for a reader.
type dueDigest struct {
	Kind    string
	SendDay time.Time
}

func dueDigests(now time.Time, loc *time.Location) []dueDigest {
	local := now.In(loc)
	var out []dueDigest
	for _, kind := range []string{DigestMonthly, DigestWeekly} {
		day := digestSendDay(kind, local)
		sendAt := day.Add(digestSendAt)
		if !local.Before(sendAt) && local.Before(sendAt.Add(digestGrace)) {
			out = append(out, dueDigest{Kind: kind, SendDay: day})
		}
	}
	return out
}

// RunUsageDigests sends every digest that is due. It runs every few minutes;
// the unique archive key makes each (reader, kind, period) go out once even
// with several instances running.
func RunUsageDigests(ctx context.Context, now time.Time) error {
	if !common.UsageDigestEnabled || model.CoreDB() == nil || model.TelemetryDB() == nil {
		return nil
	}
	model.
		// A claim whose process died before sending is released as failed; an
		// operator can regenerate it.
		TelemetryDB().
		Model(&model.MailDelivery{}).Where("status = ? AND created_at < ?", model.MailStatusGenerating, now.Add(-30*time.Minute).Unix()).
		Updates(map[string]any{"status": model.MailStatusFailed, "error": "interrupted before sending"})

	candidates, err := model.ListDigestCandidates()
	if err != nil {
		return err
	}
	for _, c := range candidates {
		if err := ctx.Err(); err != nil {
			return err
		}
		setting := (&model.User{Setting: c.Setting}).GetSetting()
		loc := DigestLocation(setting)
		due := dueDigests(now, loc)
		// When the 1st is a Monday only the monthly digest goes out; it carries
		// a line about last week instead.
		monthlyToday := false
		for _, d := range due {
			if d.Kind == DigestMonthly && d.SendDay.Weekday() == time.Monday {
				monthlyToday = true
			}
		}
		for _, d := range due {
			if err := runOneDigest(c, setting, d, monthlyToday, now); err != nil {
				common.SysError(fmt.Sprintf("usage digest %s for user %d: %v", d.Kind, c.Id, err))
			}
		}
	}
	return nil
}

func runOneDigest(c model.DigestCandidate, setting dto.UserSetting, d dueDigest, monthlyToday bool, now time.Time) error {
	_, _, period := DigestPeriods(d.Kind, d.SendDay)
	key := digestRefKey(d.Kind, c.Id, period)
	if exists, err := model.MailDeliveryExists(key); err != nil || exists {
		return err
	}
	row := &model.MailDelivery{Kind: digestMailKind(d.Kind), UserID: c.Id, Receiver: c.Email, RefKey: &key, Period: period,
		Template: digestTemplateVersion, Lang: common.MailLang(setting.Language), Timezone: d.SendDay.Location().String(), CreatedAt: now.Unix()}
	skip := func(reason string) error {
		row.Status, row.Error = model.MailStatusSkipped, reason
		_, err := model.ClaimMailDelivery(row)
		return err
	}
	if digestOptedOut(setting, d.Kind) {
		return skip(digestSkipOptedOut)
	}
	if d.Kind == DigestWeekly && monthlyToday {
		return skip(digestSkipMonthlySameDay)
	}
	snap, err := BuildDigestSnapshot(c.Id, setting, d.Kind, d.SendDay)
	if err != nil {
		return err
	}
	if snap.Usage.CurrentMissing {
		return skip(digestSkipIncomplete)
	}
	if snap.Usage.Current.Spend < DigestSettingsNow().MinSpendMicro() {
		return skip(digestSkipBelowMinimum)
	}
	if d.Kind == DigestMonthly && d.SendDay.Weekday() == time.Monday {
		week, prevWeek, _ := DigestPeriods(DigestWeekly, d.SendDay)
		if u, err := model.CollectDigestUsage(c.Id, week, prevWeek, false); err == nil && u.Current.Spend > 0 {
			snap.LastWeek = &DigestWeekLine{Start: week.Start.Unix(), End: week.End.Unix(), Spend: u.Current.Spend, PrevSpend: u.Previous.Spend}
		}
	}
	row.Status = model.MailStatusGenerating
	claimed, err := model.ClaimMailDelivery(row)
	if err != nil || !claimed {
		return err
	}
	return sendDigest(row, snap, c.Id, digestHeaders(c.Id, d.Kind))
}

// RefreshMailDeliveryStatus asks Resend what happened to recent mail.
func RefreshMailDeliveryStatus(ctx context.Context, now time.Time) error {
	if model.TelemetryDB() == nil {
		return nil
	}
	rows, err := model.MailDeliveriesToCheck(now, 40)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return err
		}
		event, err := common.ResendEmailStatus(row.ProviderID)
		if err != nil {
			_ = model.UpdateMailDeliveryEvent(row.ID, "", now)
			continue
		}
		_ = model.UpdateMailDeliveryEvent(row.ID, event, now)
		time.Sleep(300 * time.Millisecond)
	}
	return nil
}

// DigestDraft is one reader's upcoming digest as the scheduler would treat
// it if it ran with today's figures.
type DigestDraft struct {
	UserID    int    `json:"user_id"`
	Username  string `json:"username"`
	Receiver  string `json:"receiver"`
	Lang      string `json:"lang"`
	Timezone  string `json:"timezone"`
	Period    string `json:"period"`
	SendAt    int64  `json:"send_at"`
	Spend     int64  `json:"spend_microusd"`
	PrevSpend int64  `json:"prev_spend_microusd"`
	// Status is send, skip or claimed (already sent or skipped this period).
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

// DigestDrafts lists every reader's upcoming digest of a kind with the
// figures so far, without sending or archiving anything.
func DigestDrafts(kind string, now time.Time) ([]DigestDraft, error) {
	candidates, err := model.ListDigestCandidates()
	if err != nil {
		return nil, err
	}
	names := map[int]string{}
	var users []struct {
		Id       int
		Username string
	}
	model.CoreDB().
		Model(&model.User{}).Select("id, username").Where("has_topped_up = ?", true).Scan(&users)
	for _, u := range users {
		names[u.Id] = u.Username
	}
	out := make([]DigestDraft, 0, len(candidates))
	for _, c := range candidates {
		setting := (&model.User{Setting: c.Setting}).GetSetting()
		loc := DigestLocation(setting)
		sendDay := upcomingSendDay(kind, now.In(loc))
		current, previous, period := DigestPeriods(kind, sendDay)
		draft := DigestDraft{UserID: c.Id, Username: names[c.Id], Receiver: c.Email, Lang: common.MailLang(setting.Language),
			Timezone: loc.String(), Period: period, SendAt: sendDay.Add(digestSendAt).Unix(), Status: "send"}
		if exists, _ := model.MailDeliveryExists(digestRefKey(kind, c.Id, period)); exists {
			draft.Status = "claimed"
		}
		usage, err := model.CollectDigestUsage(c.Id, current, previous, false)
		if err != nil {
			return nil, err
		}
		draft.Spend, draft.PrevSpend = usage.Current.Spend, usage.Previous.Spend
		if draft.Status == "send" {
			switch {
			case digestOptedOut(setting, kind):
				draft.Status, draft.Reason = "skip", digestSkipOptedOut
			case usage.CurrentMissing:
				draft.Status, draft.Reason = "skip", digestSkipIncomplete
			case usage.Current.Spend < DigestSettingsNow().MinSpendMicro():
				draft.Status, draft.Reason = "skip", digestSkipBelowMinimum
			}
		}
		out = append(out, draft)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if (out[i].Status == "send") != (out[j].Status == "send") {
			return out[i].Status == "send"
		}
		return out[i].Spend > out[j].Spend
	})
	return out, nil
}

// GenerateDigestDrafts builds this period's digest for every reader it would
// go to, with the figures so far, and files each as a draft in the archive
// (运营 › 邮件 › 发送记录). Nothing is sent. Earlier drafts of the same kind
// and period are replaced.
func GenerateDigestDrafts(kind string, now time.Time) (int, error) {
	drafts, err := DigestDrafts(kind, now)
	if err != nil {
		return 0, err
	}
	if len(drafts) == 0 {
		return 0, nil
	}
	if err := model.TelemetryDB().Where("status = ? AND kind = ? AND period = ?", model.MailStatusDraft, digestMailKind(kind), drafts[0].Period).
		Delete(&model.MailDelivery{}).Error; err != nil {
		return 0, err
	}
	made := 0
	for _, d := range drafts {
		if d.Status != "send" {
			continue
		}
		user, err := model.GetUserById(d.UserID, true)
		if err != nil {
			return made, err
		}
		setting := user.GetSetting()
		loc := DigestLocation(setting)
		snap, err := BuildDigestSnapshot(d.UserID, setting, kind, upcomingSendDay(kind, now.In(loc)))
		if err != nil {
			return made, err
		}
		row := &model.MailDelivery{Kind: digestMailKind(kind), UserID: d.UserID, Receiver: d.Receiver, Period: snap.Period,
			Template: digestTemplateVersion, Lang: snap.Lang, Timezone: snap.Timezone, Status: model.MailStatusDraft,
			Error: "draft: not sent", CreatedAt: now.Unix()}
		if err := model.TelemetryDB().Create(row).Error; err != nil {
			return made, err
		}
		html, err := common.RenderDigestMail(BuildDigestView(snap, d.UserID, digestChartURL(row.ID)))
		if err != nil {
			return made, err
		}
		encoded, err := json.Marshal(snap)
		if err != nil {
			return made, err
		}
		if err := model.SaveMailDeliveryContent(row.ID, "[草稿] "+DigestSubject(snap), html, string(encoded)); err != nil {
			return made, err
		}
		made++
	}
	return made, nil
}

// DigestSettings are the digest thresholds operators tune under 运营 › 邮件
// without a release. They live in the Options table.
type DigestSettings struct {
	// A period that cost less than this is not mailed.
	MinSpendUSD float64 `json:"min_spend_usd"`
	// The "below list price" cell shows only when the saving reaches both.
	MinSavingUSD float64 `json:"min_saving_usd"`
	MinSavingPct float64 `json:"min_saving_pct"`
}

const (
	DigestMinSpendOption     = "UsageDigestMinSpendUSD"
	DigestMinSavingOption    = "UsageDigestMinSavingUSD"
	DigestMinSavingPctOption = "UsageDigestMinSavingPct"
)

func (s DigestSettings) MinSpendMicro() int64 { return int64(math.Round(s.MinSpendUSD * 1e6)) }

// DigestSettingsNow reads the thresholds, falling back to $1 / $1 / 15%.
func DigestSettingsNow() DigestSettings {
	read := func(key string, fallback float64) float64 {
		raw := strings.TrimSpace(model.GetOptionValue(key))
		if raw == "" {
			return fallback
		}
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil || v < 0 {
			return fallback
		}
		return v
	}
	return DigestSettings{
		MinSpendUSD:  read(DigestMinSpendOption, 1),
		MinSavingUSD: read(DigestMinSavingOption, 1),
		MinSavingPct: read(DigestMinSavingPctOption, 15),
	}
}

// SaveDigestSettings validates and stores the thresholds.
func SaveDigestSettings(s DigestSettings) error {
	if s.MinSpendUSD < 0 || s.MinSpendUSD > 10000 || s.MinSavingUSD < 0 || s.MinSavingUSD > 10000 || s.MinSavingPct < 0 || s.MinSavingPct > 100 {
		return errors.New("digest thresholds are out of range")
	}
	for key, v := range map[string]float64{DigestMinSpendOption: s.MinSpendUSD, DigestMinSavingOption: s.MinSavingUSD, DigestMinSavingPctOption: s.MinSavingPct} {
		if err := model.UpdateOption(key, strconv.FormatFloat(v, 'f', -1, 64)); err != nil {
			return err
		}
	}
	return nil
}

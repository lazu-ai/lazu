package alerting

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/robfig/cron/v3"
)

// digestTask names a recorded task in the digest and says how often it is
// expected to report. A snapshot older than twice its cadence means the task
// stopped running, so the digest shows it as stopped instead of repeating its
// last status. Snapshots of keys not listed here keep their own label and are
// never marked stopped.
type digestTask struct {
	name    string
	cadence time.Duration
}

// digestTaskCadence is the cadence a task is judged by: the upload follows
// its configured cron schedule, everything else its fixed one.
func digestTaskCadence(key string, task digestTask, now time.Time) time.Duration {
	if key == "hourly-upload" {
		if cadence, ok := cronCadence(common.AppConfig.Upload.Schedule, now); ok {
			return cadence
		}
	}
	return task.cadence
}

// cronCadence returns the longest gap between the next few runs of a cron
// schedule, so a schedule that fires unevenly is judged by its slowest gap.
func cronCadence(spec string, now time.Time) (time.Duration, bool) {
	schedule, err := cron.ParseStandard(strings.TrimSpace(spec))
	if err != nil {
		return 0, false
	}
	var longest time.Duration
	previous := schedule.Next(now)
	for i := 0; i < 24; i++ {
		next := schedule.Next(previous)
		if next.IsZero() {
			break
		}
		if gap := next.Sub(previous); gap > longest {
			longest = gap
		}
		previous = next
	}
	return longest, longest > 0
}

// digestTaskExpected reports whether this deployment starts the task at all,
// so a task that is configured off is not listed as never reported.
var digestTaskExpected = func(key string) bool {
	switch key {
	case "hourly-upload":
		return common.AppConfig.Upload.Enable
	case "pg-dump":
		return common.UsingPostgreSQL && common.AppConfig.ArtifactStorage.S3.IsConfigured()
	case "archive-compactor":
		return common.AppConfig.ArtifactStorage.S3.IsConfigured()
	default:
		return true
	}
}

var digestTasks = map[string]digestTask{
	"pg-dump":           {name: "数据库备份", cadence: 24 * time.Hour},
	"hourly-upload":     {name: "请求日志上传 R2", cadence: time.Hour},
	"archive-compactor": {name: "R2 旧归档压缩", cadence: 24 * time.Hour},
	"user-file-cleanup": {name: "过期用户文件清理", cadence: 24 * time.Hour},
	"relay-zero-token":  {name: "零 token 计费监控", cadence: 15 * time.Minute},
}

type digestStatus struct {
	rank  int
	icon  string
	label string
	// quiet statuses need no action: they are folded into one summary line
	// at the bottom instead of being listed under the headline.
	quiet bool
}

var digestStatuses = map[string]digestStatus{
	"failed":   {rank: 0, icon: "❌", label: "失败"},
	"alerting": {rank: 1, icon: "🔔", label: "告警未恢复"},
	"stale":    {rank: 1, icon: "⏸️", label: "已停止运行"},
	"warning":  {rank: 2, icon: "⚠️", label: "有警告"},
	"unknown":  {rank: 3, icon: "➖", label: "未检测", quiet: true},
	// missing is quiet: right after a deploy a daily task has simply not
	// reached its first run yet, which is not worth a warning.
	"missing": {rank: 3, icon: "➖", label: "从未上报", quiet: true},
	"skipped": {rank: 4, icon: "➖", label: "已跳过", quiet: true},
	"ok":      {rank: 5, icon: "✅", label: "正常", quiet: true},
}

type digestLine struct {
	snapshot HealthSnapshot
	name     string
	status   string
	age      time.Duration
	incident *incidentState
	// alertOnly marks a line with no health snapshot behind it: an open alert
	// from a task that records none, or an expected task that never reported.
	alertOnly bool
}

// DigestSection is a block of the daily digest computed outside this
// package, such as yesterday's business numbers. Attention items are the
// ones the reader should act on; they are listed at the top with the failing
// tasks and counted in the headline.
type DigestSection struct {
	Title     string
	Lines     []string
	Attention []string
}

// DigestSectionFunc computes one section. now is the digest time in the
// operations timezone.
type DigestSectionFunc func(ctx context.Context, now time.Time) (DigestSection, error)

type registeredDigestSection struct {
	title string
	build DigestSectionFunc
}

var (
	digestSectionsMu sync.Mutex
	digestSections   []registeredDigestSection
)

const digestSectionTimeout = 30 * time.Second

// RegisterDigestSection adds a section to every later digest, in
// registration order. title is shown when the section fails to build.
func RegisterDigestSection(title string, build DigestSectionFunc) {
	if build == nil {
		return
	}
	digestSectionsMu.Lock()
	digestSections = append(digestSections, registeredDigestSection{title: title, build: build})
	digestSectionsMu.Unlock()
}

func buildDigestSections(now time.Time) []DigestSection {
	digestSectionsMu.Lock()
	registered := append([]registeredDigestSection(nil), digestSections...)
	digestSectionsMu.Unlock()
	local := now.In(digestLocation())
	sections := make([]DigestSection, 0, len(registered))
	for _, entry := range registered {
		section, err := runDigestSection(entry.build, local)
		if err != nil {
			common.SysError(fmt.Sprintf("alerting: digest section %q failed: %v", entry.title, err))
			section = DigestSection{Title: entry.title, Lines: []string{"⚠️ 数据读取失败：" + truncateDigestText(err.Error(), 160)}}
		}
		if section.Title == "" {
			section.Title = entry.title
		}
		sections = append(sections, section)
	}
	return sections
}

// runDigestSection turns a panic into an error: sections query several
// stores from inside the API process, and the digest cron does not recover.
func runDigestSection(build DigestSectionFunc, now time.Time) (section DigestSection, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), digestSectionTimeout)
	defer cancel()
	defer func() {
		if recovered := recover(); recovered != nil {
			section, err = DigestSection{}, fmt.Errorf("panic: %v", recovered)
		}
	}()
	return build(ctx, now)
}

func digestLocation() *time.Location {
	if loc, err := time.LoadLocation(common.AppConfig.Upload.Timezone); err == nil && loc != nil {
		return loc
	}
	return time.FixedZone("Asia/Shanghai", 8*60*60)
}

// formatDailyDigest leads with what needs action (failing or stopped tasks,
// open alerts, section attention items), then the sections, then one line
// for the tasks that are fine.
func formatDailyDigest(now time.Time, snapshots []HealthSnapshot, incidents map[string]incidentState, sections []DigestSection) string {
	lines := digestTaskLines(now, snapshots, incidents)
	var problems, quiet []digestLine
	for _, line := range lines {
		if lookupDigestStatus(line.status).quiet {
			quiet = append(quiet, line)
		} else {
			problems = append(problems, line)
		}
	}
	attention := 0
	for _, section := range sections {
		attention += len(section.Attention)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "📊 Lazu 每日运维报告 · %s", now.Format("2006-01-02"))
	switch {
	case len(lines) == 0 && len(sections) == 0:
		b.WriteString("\n⚠️ 没有任何任务上报过结果")
		return b.String()
	case len(problems)+attention == 0:
		b.WriteString("\n✅ 没有需要处理的事")
	default:
		fmt.Fprintf(&b, "\n⚠️ 需要处理 %d 项", len(problems)+attention)
	}

	for _, line := range problems {
		writeDigestProblem(&b, now, line)
	}
	for _, section := range sections {
		for _, item := range section.Attention {
			fmt.Fprintf(&b, "\n\n⚠️ %s", item)
		}
	}
	for _, section := range sections {
		if len(section.Lines) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n\n【%s】", section.Title)
		for _, text := range section.Lines {
			b.WriteString("\n" + text)
		}
	}
	if len(quiet) > 0 {
		b.WriteString("\n\n【定时任务】")
		writeDigestQuiet(&b, quiet)
	}
	return b.String()
}

func digestTaskLines(now time.Time, snapshots []HealthSnapshot, incidents map[string]incidentState) []digestLine {
	lines := make([]digestLine, 0, len(snapshots)+len(incidents))
	withHealth := map[string]bool{}
	for _, snapshot := range snapshots {
		if _, known := digestTasks[snapshot.Key]; known && !digestTaskExpected(snapshot.Key) {
			// A task switched off after it last reported keeps its snapshot in
			// Redis; it is not stopped, just no longer run. An open alert for
			// it is still listed below.
			continue
		}
		line := digestLine{
			snapshot: snapshot,
			name:     snapshot.Label,
			status:   snapshot.Status,
			age:      now.Sub(time.Unix(snapshot.UpdatedAt, 0)),
		}
		if line.age < 0 {
			line.age = 0
		}
		if task, ok := digestTasks[snapshot.Key]; ok {
			line.name = task.name
			if line.age > 2*digestTaskCadence(snapshot.Key, task, now) {
				line.status = "stale"
			}
		}
		if state, ok := incidents[snapshot.Key]; ok {
			line.incident = &state
			if lookupDigestStatus(line.status).quiet {
				// A task that reads ok while its alert is still open has not
				// really recovered; the open alert wins.
				line.status = "alerting"
			}
		}
		withHealth[snapshot.Key] = true
		lines = append(lines, line)
	}
	for key, task := range digestTasks {
		if withHealth[key] || !digestTaskExpected(key) {
			continue
		}
		withHealth[key] = true
		line := digestLine{snapshot: HealthSnapshot{Key: key}, name: task.name, status: "missing", alertOnly: true}
		if state, ok := incidents[key]; ok {
			line.incident, line.status = &state, "alerting"
		}
		lines = append(lines, line)
	}
	for key, state := range incidents {
		if withHealth[key] {
			continue
		}
		state := state
		name := key
		if task, ok := digestTasks[key]; ok {
			name = task.name
		}
		lines = append(lines, digestLine{name: name, status: "alerting", incident: &state, alertOnly: true})
	}
	sort.SliceStable(lines, func(i, j int) bool {
		ri, rj := lookupDigestStatus(lines[i].status).rank, lookupDigestStatus(lines[j].status).rank
		if ri != rj {
			return ri < rj
		}
		return lines[i].name < lines[j].name
	})
	return lines
}

func writeDigestProblem(b *strings.Builder, now time.Time, line digestLine) {
	status := lookupDigestStatus(line.status)
	fmt.Fprintf(b, "\n\n%s %s：%s", status.icon, line.name, status.label)
	if line.incident != nil {
		writeDigestIncident(b, now, *line.incident, line.alertOnly)
	}
	if line.alertOnly {
		return
	}
	if line.status == "stale" {
		fmt.Fprintf(b, "\n   最后一次%s在 %s前（应%s运行一次）",
			lookupDigestStatus(line.snapshot.Status).label,
			formatDigestAge(line.age),
			formatDigestCadence(digestTaskCadence(line.snapshot.Key, digestTasks[line.snapshot.Key], now)))
		if line.snapshot.Summary != "" {
			fmt.Fprintf(b, "\n   上次结果：%s", line.snapshot.Summary)
		}
		return
	}
	fmt.Fprintf(b, "\n   %s", formatDigestWhen(line.age))
	if line.snapshot.Summary != "" {
		fmt.Fprintf(b, " · %s", line.snapshot.Summary)
	}
}

// writeDigestQuiet folds the tasks that need nothing into one line of names
// per status, ok first.
func writeDigestQuiet(b *strings.Builder, lines []digestLine) {
	names := map[string][]string{}
	var order []string
	for _, line := range lines {
		if len(names[line.status]) == 0 {
			order = append(order, line.status)
		}
		names[line.status] = append(names[line.status], line.name)
	}
	sort.SliceStable(order, func(i, j int) bool {
		return lookupDigestStatus(order[i]).rank > lookupDigestStatus(order[j]).rank
	})
	for _, status := range order {
		info := lookupDigestStatus(status)
		fmt.Fprintf(b, "\n%s %s %d 项：%s", info.icon, info.label, len(names[status]), strings.Join(names[status], "、"))
	}
}

// writeDigestIncident adds how long an open alert has lasted. Alerts with no
// health record also show their title, since nothing else names the problem.
func writeDigestIncident(b *strings.Builder, now time.Time, state incidentState, withTitle bool) {
	if withTitle {
		title := state.Title
		if title == "" {
			title = state.Fingerprint
		}
		fmt.Fprintf(b, "\n   %s", title)
	}
	firstSeen := state.FirstSeen
	if firstSeen == 0 {
		firstSeen = state.LastSeen
	}
	fmt.Fprintf(b, "\n   告警已持续 %s，共触发 %d 次，最近一次：%s",
		formatDigestAge(now.Sub(time.Unix(firstSeen, 0))),
		state.Occurrences,
		formatDigestAgo(now.Sub(time.Unix(state.LastSeen, 0))))
}

// lookupDigestStatus shows a status this file does not know verbatim and
// ranks it with the warnings rather than below ok.
func lookupDigestStatus(status string) digestStatus {
	if known, ok := digestStatuses[status]; ok {
		return known
	}
	fallback := digestStatuses["warning"]
	fallback.label = status
	return fallback
}

func formatDigestWhen(age time.Duration) string {
	return formatDigestAgo(age) + "更新"
}

func formatDigestAgo(age time.Duration) string {
	if age < time.Minute {
		return "刚刚"
	}
	return formatDigestAge(age) + "前"
}

func formatDigestCadence(cadence time.Duration) string {
	switch cadence {
	case 24 * time.Hour:
		return "每天"
	case time.Hour:
		return "每小时"
	default:
		return "每 " + formatDigestAge(cadence)
	}
}

// formatDigestAge renders a duration at the precision a daily reader needs:
// minutes under an hour, hours and minutes under a day, then days and hours.
func formatDigestAge(d time.Duration) string {
	d = d.Truncate(time.Minute)
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%d 分钟", int(d/time.Minute))
	case d < 24*time.Hour:
		hours, minutes := int(d/time.Hour), int(d%time.Hour/time.Minute)
		if minutes == 0 {
			return fmt.Sprintf("%d 小时", hours)
		}
		return fmt.Sprintf("%d 小时 %d 分钟", hours, minutes)
	default:
		days, hours := int(d/(24*time.Hour)), int(d%(24*time.Hour)/time.Hour)
		if hours == 0 {
			return fmt.Sprintf("%d 天", days)
		}
		return fmt.Sprintf("%d 天 %d 小时", days, hours)
	}
}

func truncateDigestText(value string, max int) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= max {
		return string(runes)
	}
	return string(runes[:max]) + "…"
}

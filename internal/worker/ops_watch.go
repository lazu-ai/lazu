package worker

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/service"
)

// Operational watches: each turns a failure that used to be visible only in
// container logs into one deduplicated [Lazu ops] mail. Every lease is
// released when the mail fails, so the next run retries it.

const (
	taskFailureAlertThreshold = 3
	taskFailureAlertEvery     = 6 * time.Hour
	snapshotStaleAlertEvery   = 6 * time.Hour
	// routeHealthRollupStaleAfter is how long the minutely rollup may go
	// without finishing before the operator hears about it.
	routeHealthRollupStaleAfter = 15 * time.Minute
	slowQueryReportEvery        = time.Hour
)

func sendLeasedOpsMail(ctx context.Context, lease string, ttl time.Duration, subject string, mail common.Mail) error {
	acquired, err := claimHostedTaskLease(ctx, lease, ttl)
	if err != nil || !acquired {
		return err
	}
	if err := service.SendOpsMail(subject, mail); err != nil {
		_ = common.RDB.Del(ctx, hostedTaskLeasePrefix+lease).Err()
		return err
	}
	return nil
}

// taskFailures counts consecutive failed runs per task in this process.
var taskFailures sync.Map // task name -> int

func noteTaskResult(ctx context.Context, task Task, err error) {
	name := task.Name()
	if err == nil {
		taskFailures.Delete(name)
		return
	}
	if ctx.Err() != nil {
		return // shutdown, not a failing task
	}
	count := 1
	if previous, ok := taskFailures.Load(name); ok {
		count = previous.(int) + 1
	}
	taskFailures.Store(name, count)
	if count < taskFailureAlertThreshold || !common.RedisEnabled || common.RDB == nil {
		return
	}
	message := err.Error()
	if len(message) > 2000 {
		message = message[:2000] + "…"
	}
	mail := common.Mail{
		Severity:  "ALERT",
		Source:    "worker",
		Title:     "后台任务 " + name + " 连续失败",
		Preheader: fmt.Sprintf("%s 已连续失败 %d 次", name, count),
		Lead:      "这个后台任务连续几轮都没有跑完。它负责的数据会停在最后一次成功时，请按下面的错误排查。",
		Blocks: []common.MailBlock{
			{Facts: []common.MailFact{
				{Label: "任务", Value: name, Mono: true},
				{Label: "连续失败", Value: fmt.Sprintf("%d 次", count)},
				{Label: "运行间隔", Value: task.Interval().String(), Mono: true},
				{Label: "下次提醒", Value: "仍失败时，至多每 6 小时一次"},
			}},
			{Snippet: message},
		},
	}
	if err := sendLeasedOpsMail(ctx, "task-failing:"+name, taskFailureAlertEvery, "后台任务 "+name+" 连续失败", mail); err != nil {
		common.SysLog("worker failure alert for " + name + ": " + err.Error())
	}
}

// RouteHealthRollupWatchTask mails the operator when the hourly route health
// rollup has stopped advancing. Pages keep working — they compute missing
// hours from raw facts — but they get slower with every hour it falls behind.
type RouteHealthRollupWatchTask struct{}

func NewRouteHealthRollupWatchTask() *RouteHealthRollupWatchTask {
	return &RouteHealthRollupWatchTask{}
}
func (*RouteHealthRollupWatchTask) Name() string            { return "route_health_rollup_watch" }
func (*RouteHealthRollupWatchTask) Interval() time.Duration { return 10 * time.Minute }

// InitialDelay lets the rollup task run a few times after a restart before
// the watch judges it.
func (*RouteHealthRollupWatchTask) InitialDelay(time.Time) time.Duration { return 15 * time.Minute }

func (*RouteHealthRollupWatchTask) Run(ctx context.Context) error {
	state, found, err := model.RouteHealthRollupStatus(ctx)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	lag := "从未运行"
	if found {
		age := now.Sub(time.UnixMilli(state.RefreshedAtMs))
		if age <= routeHealthRollupStaleAfter {
			return nil
		}
		lag = age.Truncate(time.Minute).String()
	}
	mail := common.Mail{
		Severity:  "ALERT",
		Source:    "channel-health",
		Title:     "模型健康汇总没有更新",
		Preheader: "距上次汇总 " + lag,
		Lead:      "lazu-jobs 的 route_health_rollup 任务没有按时把请求结果汇总成小时数据。模型广场和模型状态页仍会显示，但会改为现场计算，越拖越慢。请先看这个任务的错误。",
		Blocks: []common.MailBlock{{Facts: []common.MailFact{
			{Label: "距上次汇总", Value: lag},
			{Label: "允许的最长间隔", Value: routeHealthRollupStaleAfter.String(), Mono: true},
			{Label: "下次提醒", Value: "仍未恢复时，至多每 6 小时一次"},
		}}},
	}
	return sendLeasedOpsMail(ctx, "route-health-rollup-stale", snapshotStaleAlertEvery, "模型健康汇总没有更新", mail)
}

// RouteHealthRollupTask keeps the hourly route health rows current. It runs
// every minute and only reads the hours it recomputes (see
// model.RefreshRouteHealthRollup).
type RouteHealthRollupTask struct{}

func NewRouteHealthRollupTask() *RouteHealthRollupTask { return &RouteHealthRollupTask{} }
func (*RouteHealthRollupTask) Name() string            { return "route_health_rollup" }
func (*RouteHealthRollupTask) Interval() time.Duration { return time.Minute }

func (*RouteHealthRollupTask) Run(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(model.ChannelHealthAnalysisContext(ctx), 50*time.Second)
	defer cancel()
	return model.RefreshRouteHealthRollup(ctx, time.Now().UTC())
}

// SlowQueryReportTask mails the statements this process ran at or above the
// slow-SQL threshold, at most once an hour per process role.
type SlowQueryReportTask struct{ role string }

func NewSlowQueryReportTask(role string) *SlowQueryReportTask {
	return &SlowQueryReportTask{role: role}
}
func (t *SlowQueryReportTask) Name() string            { return "slow_query_report" }
func (t *SlowQueryReportTask) Interval() time.Duration { return 10 * time.Minute }

func (t *SlowQueryReportTask) Run(ctx context.Context) error {
	queries, dropped := model.DrainSlowQueries()
	if len(queries) == 0 || !common.RedisEnabled || common.RDB == nil {
		return nil
	}
	if len(queries) > 10 {
		dropped += len(queries) - 10
		queries = queries[:10]
	}
	var snippet strings.Builder
	for i, query := range queries {
		if i > 0 {
			snippet.WriteString("\n\n")
		}
		fmt.Fprintf(&snippet, "%d 次 · 最长 %s", query.Count, query.MaxDuration.Truncate(time.Millisecond))
		if query.LastError != "" {
			fmt.Fprintf(&snippet, " · 错误：%s", query.LastError)
		}
		snippet.WriteString("\n")
		snippet.WriteString(query.Fingerprint)
	}
	facts := []common.MailFact{
		{Label: "进程", Value: t.role, Mono: true},
		{Label: "最慢一条", Value: queries[0].MaxDuration.Truncate(time.Millisecond).String(), Mono: true},
		{Label: "下次提醒", Value: "仍有慢查询时，至多每小时一次"},
	}
	if dropped > 0 {
		facts = append(facts, common.MailFact{Label: "未列出", Value: fmt.Sprintf("另有 %d 种语句", dropped)})
	}
	mail := common.Mail{
		Severity:  "WARN",
		Source:    "database",
		Title:     "数据库出现慢查询",
		Preheader: fmt.Sprintf("%s 进程有 %d 种语句超过阈值", t.role, len(queries)),
		Lead:      "下面这些语句执行时间达到了慢查询阈值（SLOW_SQL_ALERT_MS，默认 1 秒）。一条逐渐变慢的查询会先出现在这里，然后才超时。",
		Blocks:    []common.MailBlock{{Facts: facts}, {Snippet: snippet.String()}},
	}
	return sendLeasedOpsMail(ctx, "slow-sql:"+t.role, slowQueryReportEvery, "数据库出现慢查询（"+t.role+"）", mail)
}

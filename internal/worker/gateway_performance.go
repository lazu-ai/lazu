package worker

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/service"
)

type GatewayPerformanceTask struct{}

func NewGatewayPerformanceTask() *GatewayPerformanceTask { return &GatewayPerformanceTask{} }
func (*GatewayPerformanceTask) Name() string             { return "gateway_performance_monitor" }
func (*GatewayPerformanceTask) Interval() time.Duration  { return time.Hour }

// Run checks yesterday once. The window ends at today's UTC midnight, so the
// answer only changes when the day does; the hourly tick just retries until a
// date-scoped lease records that the day was checked (and, on a breach, that
// the mail went out). Without Redis there is no lease to share, so every tick
// checks, as before.
func (*GatewayPerformanceTask) Run(ctx context.Context) error {
	today := time.Now().UTC().Truncate(24 * time.Hour)
	day := today.AddDate(0, 0, -1).Format("2006-01-02")
	lease := "gateway-performance:" + day
	leased := common.RedisEnabled && common.RDB != nil
	if leased {
		acquired, err := claimHostedTaskLease(ctx, lease, 48*time.Hour)
		if err != nil || !acquired {
			return err
		}
	}
	err := checkGatewayPreparation(ctx, today, day, leased)
	if err != nil && leased {
		_ = common.RDB.Del(ctx, hostedTaskLeasePrefix+lease).Err()
	}
	return err
}

func checkGatewayPreparation(ctx context.Context, today time.Time, day string, leased bool) error {
	days, err := model.GatewayPerformanceDays(ctx, today.AddDate(0, 0, -8), today)
	if err != nil {
		return err
	}
	current, baseline, breached := model.GatewayPreparationRegression(days, day)
	if !breached {
		return nil
	}
	if !leased {
		return errors.New("gateway performance alert needs Redis to send once")
	}
	return service.SendOpsMail("网关请求准备耗时上升", common.Mail{Severity: "WARN", Source: "gateway-performance", Title: "网关请求准备耗时上升", Lead: "请求发往上游之前的等待变长，请检查最近的请求路径变更。", Blocks: []common.MailBlock{{Facts: []common.MailFact{{Label: "统计日（UTC）", Value: day}, {Label: "当日 P50", Value: fmt.Sprintf("%.1f ms", current), Mono: true}, {Label: "前 7 天 P50 中位数", Value: fmt.Sprintf("%.1f ms", baseline), Mono: true}}}}})
}

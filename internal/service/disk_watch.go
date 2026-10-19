package service

import (
	"fmt"
	"sync"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
)

// diskAlertThreshold is when the root user hears about disk pressure: early
// enough to act before full-text capture pauses (85%) and long before the
// relay's own guard refuses traffic.
const diskAlertThreshold = 80.0

// diskAlertEvery keeps a full disk from sending one mail per check.
const diskAlertEvery = 6 * time.Hour

var diskWatchOnce sync.Once

// StartDiskWatch checks disk usage every five minutes and mails the root
// user when it crosses diskAlertThreshold.
func StartDiskWatch() {
	diskWatchOnce.Do(func() {
		common.SafeGo("disk-watch", func() {
			var lastAlert time.Time
			ticker := time.NewTicker(5 * time.Minute)
			defer ticker.Stop()
			for range ticker.C {
				usage := common.GetSystemStatus().DiskUsage
				if usage < diskAlertThreshold || time.Since(lastAlert) < diskAlertEvery {
					continue
				}
				lastAlert = time.Now()
				info := common.GetDiskSpaceInfo()
				node := common.NodeName
				title := fmt.Sprintf("%s 磁盘 %.0f%%", node, usage)
				if err := SendOpsMail(title, common.Mail{
					Source: "DISK", Severity: "ALERT", Title: title,
					Preheader: fmt.Sprintf("剩余 %.1f GB，85%% 时请求捕获暂停", float64(info.Free)/(1<<30)),
					Blocks: []common.MailBlock{
						{Facts: []common.MailFact{
							{Label: "使用率", Value: fmt.Sprintf("%.1f%%", usage)},
							{Label: "剩余", Value: fmt.Sprintf("%.1f GB", float64(info.Free)/(1<<30))},
							{Label: "请求捕获暂停", Value: "85%"},
							{Label: "下次提醒", Value: "6 小时后（若仍 ≥ 80%）"},
						}},
						{Text: "先看日志目录里 R2 还没取走的 JSONL 分片；到网关的磁盘阈值后会拒绝流量。"},
					},
				}); err != nil {
					common.SysError("disk watch alert: " + err.Error())
				}
			}
		})
	})
}

package worker

import (
	"context"
	"sync"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/service"
)

// UsageDigestTask sends the weekly and monthly usage digests that are due.
type UsageDigestTask struct{}

func NewUsageDigestTask() *UsageDigestTask         { return &UsageDigestTask{} }
func (t *UsageDigestTask) Name() string            { return "usage_digests" }
func (t *UsageDigestTask) Interval() time.Duration { return 5 * time.Minute }
func (t *UsageDigestTask) Run(ctx context.Context) error {
	now := time.Now()

	return service.RunUsageDigests(ctx, now)
}

// MailArchiveTask asks Resend how recent mail fared and, once an hour, drops
// archived bodies past 180 days and rows past a year.
type MailArchiveTask struct {
	mu         sync.Mutex
	lastPurged time.Time
}

func NewMailArchiveTask() *MailArchiveTask         { return &MailArchiveTask{} }
func (t *MailArchiveTask) Name() string            { return "mail_archive" }
func (t *MailArchiveTask) Interval() time.Duration { return 5 * time.Minute }
func (t *MailArchiveTask) Run(ctx context.Context) error {
	now := time.Now()
	t.mu.Lock()
	purge := now.Sub(t.lastPurged) >= time.Hour
	if purge {
		t.lastPurged = now
	}
	t.mu.Unlock()
	if purge && model.CoreDB() != nil {
		if err := model.PurgeDigestRollup(now); err != nil {
			common.SysError("purge digest rollup: " + err.Error())
		}
		if bodies, rows, err := model.PurgeMailArchive(now); err != nil {
			common.SysError("purge mail archive: " + err.Error())
		} else if bodies+rows > 0 {
			common.SysLog("mail archive purged")
		}
	}
	return service.RefreshMailDeliveryStatus(ctx, now)
}

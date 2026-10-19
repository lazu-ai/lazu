package worker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/controller"
	"github.com/lazu-ai/lazu/internal/service/uploader"
	"github.com/lazu-ai/lazu/internal/setting/operation_setting"
)

const hostedTaskLeasePrefix = "lazu:jobs:lease:"

// claimHostedTaskLease persists cadence/cooldown state across short-lived
// run-once containers. Hosted Jobs require Redis for this coordination; when
// it is unavailable we fail closed instead of repeating provider calls or
// operator notifications on every cron delivery.
func claimHostedTaskLease(ctx context.Context, name string, ttl time.Duration) (bool, error) {
	if !common.RedisEnabled || common.RDB == nil {
		return false, errors.New("hosted task coordination requires Redis")
	}
	return common.RDB.SetNX(
		ctx,
		hostedTaskLeasePrefix+name,
		time.Now().UTC().Format(time.RFC3339Nano),
		ttl,
	).Result()
}

func runHostedTaskWhenDue(ctx context.Context, name string, interval time.Duration, run func() error) error {
	acquired, err := claimHostedTaskLease(ctx, name, interval)
	if err != nil || !acquired {
		return err
	}
	return run()
}

type ChannelMaintenanceTask struct{}

func NewChannelMaintenanceTask() *ChannelMaintenanceTask  { return &ChannelMaintenanceTask{} }
func (t *ChannelMaintenanceTask) Name() string            { return "channel_maintenance" }
func (t *ChannelMaintenanceTask) Interval() time.Duration { return time.Minute }
func (t *ChannelMaintenanceTask) Run(ctx context.Context) error {
	var taskErrors []error
	if raw := strings.TrimSpace(os.Getenv("CHANNEL_UPDATE_FREQUENCY")); raw != "" {
		minutes, err := strconv.Atoi(raw)
		if err != nil || minutes <= 0 {
			taskErrors = append(taskErrors, fmt.Errorf("invalid CHANNEL_UPDATE_FREQUENCY %q", raw))
		} else if err := runHostedTaskWhenDue(
			ctx,
			"channel-balance-update",
			time.Duration(minutes)*time.Minute,
			controller.RunChannelBalanceUpdateOnce,
		); err != nil {
			taskErrors = append(taskErrors, fmt.Errorf("channel balance update: %w", err))
		}
	}

	monitor := operation_setting.GetMonitorSetting()
	if monitor.AutoTestChannelEnabled && monitor.AutoTestChannelMinutes > 0 {
		if err := runHostedTaskWhenDue(
			ctx,
			"channel-auto-test",
			time.Duration(monitor.AutoTestChannelMinutes)*time.Minute,
			controller.RunChannelAutoTestOnce,
		); err != nil {
			taskErrors = append(taskErrors, fmt.Errorf("channel auto-test: %w", err))
		}
	}
	return errors.Join(taskErrors...)
}

type PgDumpBackupTask struct{}

func NewPgDumpBackupTask() *PgDumpBackupTask        { return &PgDumpBackupTask{} }
func (t *PgDumpBackupTask) Name() string            { return "pg_dump_backup" }
func (t *PgDumpBackupTask) Interval() time.Duration { return 24 * time.Hour }
func (t *PgDumpBackupTask) Run(ctx context.Context) error {
	if !common.AppConfig.ArtifactStorage.S3.IsConfigured() {
		return nil
	}
	dsn := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if dsn == "" {
		dsn = strings.TrimSpace(common.AppConfig.Database.URL)
	}
	if !strings.HasPrefix(dsn, "postgres://") && !strings.HasPrefix(dsn, "postgresql://") {
		return nil
	}
	result := uploader.RunPgDumpOnce(ctx, dsn)
	uploader.NotifyPgDumpResult(result)
	return result.Err
}

type ArchiveCompactorTask struct{}

func NewArchiveCompactorTask() *ArchiveCompactorTask    { return &ArchiveCompactorTask{} }
func (t *ArchiveCompactorTask) Name() string            { return "archive_compactor" }
func (t *ArchiveCompactorTask) Interval() time.Duration { return 24 * time.Hour }
func (t *ArchiveCompactorTask) Run(ctx context.Context) error {
	if !common.AppConfig.ArtifactStorage.S3.IsConfigured() {
		return nil
	}
	result := uploader.RunCompactorOnce(ctx)
	uploader.NotifyCompactorResult(result)
	return result.Err
}

package worker

import (
	"context"
	"time"

	"github.com/lazu-ai/lazu/internal/service"
)

type LogRetentionTask struct{}

func NewLogRetentionTask() *LogRetentionTask { return &LogRetentionTask{} }
func (t *LogRetentionTask) Name() string     { return "log_retention" }
func (t *LogRetentionTask) Interval() time.Duration {
	return 24 * time.Hour
}
func (t *LogRetentionTask) Run(_ context.Context) error {
	service.RunLogRetentionOnce(time.Now())
	return nil
}

type UserFileCleanupTask struct{}

func NewUserFileCleanupTask() *UserFileCleanupTask { return &UserFileCleanupTask{} }
func (t *UserFileCleanupTask) Name() string        { return "user_file_cleanup" }
func (t *UserFileCleanupTask) Interval() time.Duration {
	return 24 * time.Hour
}
func (t *UserFileCleanupTask) Run(_ context.Context) error {
	return service.RunUserFileCleanupOnce(service.NowUnix())
}

type ModelRankingRefreshTask struct{}

func NewModelRankingRefreshTask() *ModelRankingRefreshTask { return &ModelRankingRefreshTask{} }
func (t *ModelRankingRefreshTask) Name() string            { return "model_ranking_refresh" }
func (t *ModelRankingRefreshTask) Interval() time.Duration {
	return time.Hour
}
func (t *ModelRankingRefreshTask) Run(_ context.Context) error {
	return service.RunModelRankingRefresh()
}

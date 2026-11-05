package worker

import (
	"context"
	"time"

	"github.com/lazu-ai/lazu/internal/controller"
)

// UpstreamSyncTask re-reads the model list of every source whose upstream
// check is due and applies what changed (controller/upstream_sync.go). Each
// source carries its own interval; this only decides how often to look.
type UpstreamSyncTask struct{}

func NewUpstreamSyncTask() *UpstreamSyncTask                   { return &UpstreamSyncTask{} }
func (*UpstreamSyncTask) Name() string                         { return "upstream_sync" }
func (*UpstreamSyncTask) Interval() time.Duration              { return 15 * time.Minute }
func (*UpstreamSyncTask) InitialDelay(time.Time) time.Duration { return 2 * time.Minute }

func (*UpstreamSyncTask) Run(ctx context.Context) error {
	return controller.RunDueUpstreamChecks(ctx)
}

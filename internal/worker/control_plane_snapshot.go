package worker

import (
	"context"
	"github.com/lazu-ai/lazu/internal/model"
	"time"
)

// API replicas read these snapshots; SQL polling and metadata writes belong to
// jobs. Run immediately at worker startup and periodically thereafter.
type ControlPlaneSnapshotTask struct{}

func NewControlPlaneSnapshotTask() *ControlPlaneSnapshotTask { return &ControlPlaneSnapshotTask{} }
func (*ControlPlaneSnapshotTask) Name() string               { return "control_plane_snapshot" }
func (*ControlPlaneSnapshotTask) Interval() time.Duration    { return time.Minute }
func (*ControlPlaneSnapshotTask) Run(ctx context.Context) error {
	return model.RefreshControlPlaneSnapshots(ctx)
}

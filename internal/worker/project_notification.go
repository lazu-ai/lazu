package worker

import (
	"context"
	"time"

	"github.com/lazu-ai/lazu/internal/service"
)

type ProjectNotificationTask struct{}

func NewProjectNotificationTask() *ProjectNotificationTask { return &ProjectNotificationTask{} }
func (t *ProjectNotificationTask) Name() string            { return "project_notifications" }
func (t *ProjectNotificationTask) Interval() time.Duration { return time.Minute }
func (t *ProjectNotificationTask) Run(ctx context.Context) error {
	return service.DeliverProjectNotifications(ctx, time.Now().UTC())
}

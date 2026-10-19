package worker

import (
	"context"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/controller"
)

// modelsDevSyncHour is when the daily models.dev sync runs (server local
// time) — early enough that the queue is ready before anyone opens 资料更新.
const modelsDevSyncHour = 4

// ModelsDevSyncTask reconciles the model catalog with models.dev once a day.
// It wakes hourly and runs on the first wake at or after 04:00 that finds no
// run yet today; the claim keeps API and worker processes from both running.
type ModelsDevSyncTask struct{}

func NewModelsDevSyncTask() *ModelsDevSyncTask { return &ModelsDevSyncTask{} }

func (t *ModelsDevSyncTask) Name() string            { return "models_dev_sync" }
func (t *ModelsDevSyncTask) Interval() time.Duration { return time.Hour }

func (t *ModelsDevSyncTask) Run(ctx context.Context) error {
	now := time.Now()
	if now.Hour() < modelsDevSyncHour {
		return nil
	}
	runCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	if _, err := controller.RunModelInfoSyncAndRefresh(runCtx, true); err != nil {
		common.SysLog("models.dev sync failed: " + err.Error())
		return err
	}
	return nil
}

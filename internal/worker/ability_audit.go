package worker

import (
	"context"
	"fmt"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/controller"
	"github.com/lazu-ai/lazu/internal/model"
)

// AbilityAuditTask keeps the derived ability table in step with each
// channel's model list, so stale routes cannot linger in the health grid or
// consume probe budget. It is cheap: one read of two small tables and a
// rebuild only for channels that actually drifted.
type AbilityAuditTask struct{}

func NewAbilityAuditTask() *AbilityAuditTask { return &AbilityAuditTask{} }

func (t *AbilityAuditTask) Name() string            { return "ability_audit" }
func (t *AbilityAuditTask) Interval() time.Duration { return 24 * time.Hour }

func (t *AbilityAuditTask) Run(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	result, err := model.AuditAbilities()
	if err != nil {
		return err
	}
	if result.Rebuilt == 0 {
		return nil
	}
	common.SysLog(fmt.Sprintf("ability audit: rebuilt %d of %d channels: %v",
		result.Rebuilt, result.Channels, result.RebuiltID))
	// The grid changed, so the cached health snapshots describe routes that
	// no longer exist.
	controller.InvalidateChannelHealthCache()
	return nil
}

package worker

import (
	"context"
	"time"

	"github.com/lazu-ai/lazu/internal/service"
	"github.com/lazu-ai/lazu/internal/setting"
	"github.com/stripe/stripe-go/v81/client"
)

// ProjectAutoReloadTask charges saved cards for wallets that fell under
// their auto-reload threshold. It runs off the request path: the gateway
// only spends; this task is the one place a reload is decided and settled.
type ProjectAutoReloadTask struct{}

func NewProjectAutoReloadTask() *ProjectAutoReloadTask { return &ProjectAutoReloadTask{} }

func (*ProjectAutoReloadTask) Name() string            { return "project_auto_reload" }
func (*ProjectAutoReloadTask) Interval() time.Duration { return 5 * time.Minute }

func (*ProjectAutoReloadTask) Run(ctx context.Context) error {
	var api *client.API
	if service.StripeSecretUsable() {
		api = &client.API{}
		api.Init(setting.StripeApiSecret, nil)
	}
	// Without a usable secret the pass still records cap pauses and skips
	// charging; ProcessProjectAutoReload reports the missing client per row.
	return service.RunProjectAutoReload(ctx, api, time.Now().UTC())
}

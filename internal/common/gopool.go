package common

import (
	"context"
	"fmt"
	"math"

	"github.com/bytedance/gopkg/util/gopool"
)

var relayGoPool gopool.Pool

type safeGoNameKey struct{}

func init() {
	relayGoPool = gopool.NewPool("gopool.RelayPool", math.MaxInt32, gopool.NewConfig())
	relayGoPool.SetPanicHandler(func(ctx context.Context, i interface{}) {
		if stopChan, ok := ctx.Value("stop_chan").(chan bool); ok {
			SafeSendBool(stopChan, true)
		}
		name, _ := ctx.Value(safeGoNameKey{}).(string)
		if name == "" {
			name = "gopool.RelayPool"
		}
		SysError(fmt.Sprintf("panic in %s: %v", name, i))
	})
}

func RelayCtxGo(ctx context.Context, f func()) {
	SafeCtxGo(ctx, "relay", f)
}

func SafeGo(name string, f func()) {
	SafeCtxGo(context.Background(), name, f)
}

func SafeCtxGo(ctx context.Context, name string, f func()) {
	if ctx == nil {
		ctx = context.Background()
	}
	if name != "" {
		ctx = context.WithValue(ctx, safeGoNameKey{}, name)
	}
	relayGoPool.CtxGo(ctx, f)
}

func SafeSendBool(ch chan bool, value bool) (closed bool) {
	defer func() {
		// Recover from panic if one occurred. A panic would mean the channel was closed.
		if recover() != nil {
			closed = true
		}
	}()

	// This will panic if the channel is closed.
	ch <- value

	// If the code reaches here, then the channel was not closed.
	return false
}

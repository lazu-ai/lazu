package common

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	redisv9 "github.com/redis/go-redis/v9"
)

// go-redis v9 (v9.12+) tries to enable CLIENT MAINT_NOTIFICATIONS on every new
// connection so it can coordinate failover with Redis Enterprise / ElastiCache.
// Against plain open-source Redis the server replies "ERR unknown subcommand",
// v9 silently disables the feature for that connection and prints
//
//	auto mode fallback: maintnotifications disabled due to handshake error: ...
//
// as a WARNING. It fires once per connection attempt, so the pool warmup spams
// the logs at startup.
//
// We drop that one message and pass everything else through to the default
// stderr logger.
type filteredRedisLogger struct {
	inner *log.Logger
}

func (l *filteredRedisLogger) Printf(_ context.Context, format string, v ...interface{}) {
	msg := fmt.Sprintf(format, v...)
	if strings.Contains(msg, "maintnotifications disabled") {
		return
	}
	_ = l.inner.Output(2, msg)
}

func init() {
	redisv9.SetLogger(&filteredRedisLogger{
		inner: log.New(os.Stderr, "redis: ", log.LstdFlags|log.Lshortfile),
	})
}

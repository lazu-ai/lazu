package api

import (
	"context"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
)

type runtimeHealth struct {
	draining            atomic.Bool
	dependencyMu        sync.RWMutex
	dependencies        map[string]dependencyHealth
	dependenciesOK      bool
	dependenciesChecked time.Time
	snapshotMaxAge      time.Duration
}

type dependencyHealth struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

func newRuntimeHealth() *runtimeHealth {
	return &runtimeHealth{
		dependencies:   map[string]dependencyHealth{},
		snapshotMaxAge: readinessSnapshotMaxAge(readinessProbeInterval()),
	}
}

func (h *runtimeHealth) beginDrain() {
	h.draining.Store(true)
}

func (h *runtimeHealth) livez(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":  "ok",
		"service": "lazu-api",
		"version": common.Version,
	})
}

func (h *runtimeHealth) readyz(c *gin.Context) {
	h.dependencyMu.RLock()
	checks := make(map[string]dependencyHealth, len(h.dependencies)+1)
	for name, check := range h.dependencies {
		checks[name] = check
	}
	ready := h.dependenciesOK
	checkedAt := h.dependenciesChecked
	maxAge := h.snapshotMaxAge
	h.dependencyMu.RUnlock()

	if checkedAt.IsZero() || maxAge <= 0 || time.Since(checkedAt) > maxAge {
		checks["probe"] = dependencyHealth{
			Status: "stale",
			Error:  "dependency probe snapshot expired",
		}
		ready = false
	} else {
		checks["probe"] = dependencyHealth{Status: "fresh"}
	}

	if h.draining.Load() {
		checks["drain"] = dependencyHealth{Status: "draining"}
		ready = false
	} else {
		checks["drain"] = dependencyHealth{Status: "accepting"}
	}

	status := "ready"
	statusCode := http.StatusOK
	if !ready {
		status = "not_ready"
		statusCode = http.StatusServiceUnavailable
	}
	c.JSON(statusCode, gin.H{
		"status": status,
		"checks": checks,
	})
}

func (h *runtimeHealth) refreshDependencies(ctx context.Context) {
	checks := map[string]dependencyHealth{}
	ready := true

	if err := model.PingDB(); err != nil {
		checks["database"] = dependencyHealth{Status: "unavailable", Error: "database ping failed"}
		ready = false
	} else {
		checks["database"] = dependencyHealth{Status: "ok"}
	}

	if err := model.ValidateSchemaVersion(); err != nil {
		checks["schema"] = dependencyHealth{Status: "incompatible", Error: err.Error()}
		ready = false
	} else {
		checks["schema"] = dependencyHealth{Status: "ok"}
	}

	if model.TelemetryDB() != nil && model.TelemetryDB() != model.CoreDB() {
		if err := model.PingLogDB(); err != nil {
			checks["log_database"] = dependencyHealth{Status: "unavailable", Error: "log database ping failed"}
			ready = false
		} else {
			checks["log_database"] = dependencyHealth{Status: "ok"}
		}
		if err := model.ValidateLogSchemaVersion(); err != nil {
			checks["log_schema"] = dependencyHealth{Status: "incompatible", Error: err.Error()}
			ready = false
		} else {
			checks["log_schema"] = dependencyHealth{Status: "ok"}
		}
	}

	redisRequired := envBool("READINESS_REDIS_REQUIRED", false)
	if !common.RedisEnabled || common.RDB == nil {
		status := "disabled"
		if redisRequired {
			status = "required"
			ready = false
		}
		checks["redis"] = dependencyHealth{Status: status}
	} else {
		pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		if err := common.RDB.Ping(pingCtx).Err(); err != nil {
			checks["redis"] = dependencyHealth{Status: "unavailable", Error: "redis ping failed"}
			ready = false
		} else {
			checks["redis"] = dependencyHealth{Status: "ok"}
		}
	}

	h.dependencyMu.Lock()
	h.dependencies = checks
	h.dependenciesOK = ready
	h.dependenciesChecked = time.Now()
	h.dependencyMu.Unlock()
}

func (h *runtimeHealth) startDependencyProbe(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 10 * time.Second
	}
	common.SafeCtxGo(ctx, "runtime-readiness-probe", func() {
		h.refreshDependencies(ctx)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				h.refreshDependencies(ctx)
			}
		}
	})
}

func readinessProbeInterval() time.Duration {
	seconds := common.GetEnvOrDefault("READINESS_PROBE_INTERVAL_SECONDS", 10)
	if seconds < 1 {
		seconds = 1
	}
	return time.Duration(seconds) * time.Second
}

func readinessSnapshotMaxAge(probeInterval time.Duration) time.Duration {
	if probeInterval <= 0 {
		probeInterval = 10 * time.Second
	}
	return 3 * probeInterval
}

func envBool(name string, fallback bool) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

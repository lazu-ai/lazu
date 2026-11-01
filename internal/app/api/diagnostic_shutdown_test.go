package api

import (
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/service"
	"github.com/oklog/ulid/v2"
	"gorm.io/gorm"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestShutdownDrainsDiagnosticsBeforeStoppingWriters(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "diagnostics.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Log{}); err != nil {
		t.Fatal(err)
	}
	oldDB, oldRedis := model.ConfiguredTelemetryDB(), common.RedisEnabled
	model.SetTelemetryDB(db)
	common.RedisEnabled = false
	t.Cleanup(func() { model.SetTelemetryDB(oldDB); common.RedisEnabled = oldRedis; raw, _ := db.DB(); raw.Close() })
	// The diagnostics update matches request_ts to the time in the id, so take
	// it from the id: a separate time.Now() can land a millisecond later.
	requestID := common.GenerateRequestID()
	id, err := ulid.ParseStrict(strings.TrimPrefix(requestID, common.RequestIDPrefix))
	if err != nil {
		t.Fatal(err)
	}
	row := model.Log{RequestID: requestID, Type: model.LogTypeConsume, RequestTS: int64(id.Time()), Other: "{}"}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set(common.RequestIdKey, row.RequestID)
	model.MarkRequestLogTarget(c, row.RequestID)
	service.RecordRelayTimingDiagnostics(c, map[string]interface{}{"gateway_post_ms": 12})
	drainAPIWriters(func() {
		var result model.Log
		if err := db.First(&result, row.Id).Error; err != nil {
			t.Fatal(err)
		}
		if result.Other == "{}" {
			t.Fatal("stopped writers before queued diagnostics committed")
		}
	})
}

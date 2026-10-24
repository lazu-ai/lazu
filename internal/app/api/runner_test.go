package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
	"gorm.io/gorm"
)

func TestAPIBackgroundTasksEnabledDefaultsOn(t *testing.T) {
	t.Setenv("PROCESS_ROLE", "")
	if !apiBackgroundTasksEnabled() {
		t.Fatal("expected self-hosted background tasks to remain enabled by default")
	}
}

func TestAPIBackgroundTasksCanBeDisabled(t *testing.T) {
	t.Setenv("PROCESS_ROLE", "api")
	if apiBackgroundTasksEnabled() {
		t.Fatal("expected API background tasks to be disabled")
	}
}

func TestAPIBackgroundWorkerTasksIncludeChannelProbe(t *testing.T) {
	tasks := apiBackgroundWorkerTasks()
	want := []string{"channel_probe", "video_canary", "ability_audit", "models_dev_sync", "project_notifications", "usage_digests", "mail_archive", "project_payment_documents", "dashboard_usage_rollup", "routing_retention", "route_health_rollup", "route_health_rollup_watch", "upstream_account_watch"}
	if len(tasks) != len(want) {
		t.Fatalf("background worker tasks = %d, want %v", len(tasks), want)
	}
	for i, name := range want {
		if tasks[i].Name() != name {
			t.Fatalf("task %d = %s, want %s", i, tasks[i].Name(), name)
		}
	}
}

func TestConfiguredDrainDelay(t *testing.T) {
	t.Setenv("SHUTDOWN_DRAIN_DELAY_SECONDS", "")
	if got := configuredDrainDelay(); got != 5*time.Second {
		t.Fatalf("expected default drain delay, got %s", got)
	}
	t.Setenv("SHUTDOWN_DRAIN_DELAY_SECONDS", "12")
	if got := configuredDrainDelay(); got != 12*time.Second {
		t.Fatalf("expected configured drain delay, got %s", got)
	}
	t.Setenv("SHUTDOWN_DRAIN_DELAY_SECONDS", "90")
	if got := configuredDrainDelay(); got != maxDrainDelay {
		t.Fatalf("expected drain delay clamp, got %s", got)
	}
	t.Setenv("SHUTDOWN_DRAIN_DELAY_SECONDS", "-1")
	if got := configuredDrainDelay(); got != 0 {
		t.Fatalf("expected negative drain delay to become zero, got %s", got)
	}
}

func TestRootSyncOnStartDefaultsOn(t *testing.T) {
	t.Setenv("ROOT_SYNC_ON_START", "")
	if !rootSyncOnStart() {
		t.Fatal("expected self-hosted root synchronization to remain enabled by default")
	}
}

func TestRootSyncOnStartCanBeDisabled(t *testing.T) {
	t.Setenv("ROOT_SYNC_ON_START", "false")
	if rootSyncOnStart() {
		t.Fatal("expected hosted API replicas to skip root synchronization")
	}
}

func TestReadinessProbeIntervalHasSafeMinimum(t *testing.T) {
	t.Setenv("READINESS_PROBE_INTERVAL_SECONDS", "0")
	if got := readinessProbeInterval(); got != time.Second {
		t.Fatalf("expected one-second minimum, got %s", got)
	}
	t.Setenv("READINESS_PROBE_INTERVAL_SECONDS", "7")
	if got := readinessProbeInterval(); got != 7*time.Second {
		t.Fatalf("expected configured interval, got %s", got)
	}
}

func TestRuntimeReadinessRejectsStaleDependencySnapshot(t *testing.T) {
	gin.SetMode(gin.TestMode)
	health := &runtimeHealth{
		dependencies: map[string]dependencyHealth{
			"database": {Status: "ok"},
		},
		dependenciesOK:      true,
		dependenciesChecked: time.Now().Add(-31 * time.Second),
		snapshotMaxAge:      30 * time.Second,
	}
	engine := gin.New()
	engine.GET("/readyz", health.readyz)
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected stale readiness snapshot to fail, got %d: %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"probe":{"status":"stale"`) {
		t.Fatalf("expected stale probe evidence, got %s", recorder.Body.String())
	}
}

func TestRuntimeReadinessStartsUnreadyBeforeFirstProbe(t *testing.T) {
	gin.SetMode(gin.TestMode)
	health := newRuntimeHealth()
	engine := gin.New()
	engine.GET("/readyz", health.readyz)
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected startup readiness to fail before the first probe, got %d: %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"probe":{"status":"stale"`) {
		t.Fatalf("expected missing initial probe evidence, got %s", recorder.Body.String())
	}
}

func TestNewHTTPServerCanBindLoopbackFixture(t *testing.T) {
	t.Setenv("LAZU_BIND_HOST", "127.0.0.1")
	if got := newHTTPServer("3000", http.NewServeMux()).Addr; got != "127.0.0.1:3000" {
		t.Fatalf("fixture bind=%s", got)
	}
}

func TestNewHTTPServerHasStreamingSafeTimeouts(t *testing.T) {
	t.Setenv("LAZU_BIND_HOST", "")
	server := newHTTPServer("3000", http.NewServeMux())
	if server.ReadHeaderTimeout != 10*time.Second {
		t.Fatalf("unexpected read header timeout: %s", server.ReadHeaderTimeout)
	}
	if server.IdleTimeout != 2*time.Minute {
		t.Fatalf("unexpected idle timeout: %s", server.IdleTimeout)
	}
	if server.WriteTimeout != 0 {
		t.Fatalf("streaming responses must not have a whole-response write timeout: %s", server.WriteTimeout)
	}
}

func TestRuntimeReadinessReflectsDependenciesAndDrain(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open("file:runtime_readiness?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open readiness database: %v", err)
	}
	previousDB := model.CoreDB()
	previousLogDB := model.ConfiguredTelemetryDB()
	previousRedisEnabled := common.RedisEnabled
	previousRedisClient := common.RDB
	model.SetCoreDB(db)
	model.SetTelemetryDB(db)
	common.RedisEnabled = false
	common.RDB = nil
	t.Cleanup(func() {
		model.SetCoreDB(previousDB)
		model.SetTelemetryDB(previousLogDB)
		common.RedisEnabled = previousRedisEnabled
		common.RDB = previousRedisClient
	})
	if err := db.AutoMigrate(&model.SchemaVersion{}, &model.Announcement{}, &model.Project{}, &model.ProjectMember{}, &model.ProjectInvite{}, &model.BudgetRequest{}, &model.ProjectRemoval{}, &model.ProjectRemovalKey{}, &model.ProjectBudgetHistory{}, &model.ProjectNotification{}, &model.TokenCredential{}, &model.TokenRotation{}, &model.ProjectSpend{}, &model.ProjectDataMigration{}, &model.User{}, &model.Token{}, &model.File{}, &model.MediaJob{}, &model.SpendBudget{}, &model.SpendBudgetAlert{}, &model.SpendBudgetReservation{}, &model.SpendBudgetCharge{}, &model.TopUp{}, &model.SettlementOutbox{}, &model.SettlementFailure{}); err != nil {
		t.Fatalf("migrate primary schema: %v", err)
	}
	if err := db.Create(&model.SchemaVersion{Scope: "primary", Version: model.SchemaVersionCurrent}).Error; err != nil {
		t.Fatalf("insert schema version: %v", err)
	}

	health := newRuntimeHealth()
	health.refreshDependencies(t.Context())
	engine := gin.New()
	engine.GET("/readyz", health.readyz)
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected ready status, got %d: %s", recorder.Code, recorder.Body.String())
	}

	health.beginDrain()
	recorder = httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected draining instance to be unavailable, got %d", recorder.Code)
	}
}

func TestRuntimeReadinessChecksSeparateLogSchema(t *testing.T) {
	gin.SetMode(gin.TestMode)
	primary, err := gorm.Open(sqlite.Open("file:runtime_primary?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open primary database: %v", err)
	}
	logDB, err := gorm.Open(sqlite.Open("file:runtime_log?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open log database: %v", err)
	}
	previousDB, previousLogDB := model.CoreDB(), model.ConfiguredTelemetryDB()
	model.SetCoreDB(primary)
	model.SetTelemetryDB(logDB)
	t.Cleanup(func() { model.SetCoreDB(previousDB); model.SetTelemetryDB(previousLogDB) })
	if err := primary.AutoMigrate(&model.SchemaVersion{}, &model.Announcement{}, &model.Project{}, &model.ProjectMember{}, &model.ProjectInvite{}, &model.BudgetRequest{}, &model.ProjectRemoval{}, &model.ProjectRemovalKey{}, &model.ProjectBudgetHistory{}, &model.ProjectNotification{}, &model.TokenCredential{}, &model.TokenRotation{}, &model.ProjectSpend{}, &model.ProjectDataMigration{}, &model.User{}, &model.Token{}, &model.File{}, &model.MediaJob{}, &model.SpendBudget{}, &model.SpendBudgetAlert{}, &model.SpendBudgetReservation{}, &model.SpendBudgetCharge{}, &model.TopUp{}, &model.SettlementOutbox{}, &model.SettlementFailure{}); err != nil {
		t.Fatalf("migrate primary schema: %v", err)
	}
	if err := primary.Create(&model.SchemaVersion{Scope: "primary", Version: model.SchemaVersionCurrent}).Error; err != nil {
		t.Fatalf("insert primary schema version: %v", err)
	}

	health := newRuntimeHealth()
	health.refreshDependencies(t.Context())
	engine := gin.New()
	engine.GET("/readyz", health.readyz)
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected missing log schema to fail readiness, got %d: %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"log_schema"`) {
		t.Fatalf("expected log schema check in readiness response: %s", recorder.Body.String())
	}
}

func TestRuntimeReadinessServesSnapshotUntilProbeRefresh(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open("file:runtime_snapshot?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open readiness database: %v", err)
	}
	previousDB := model.CoreDB()
	previousLogDB := model.ConfiguredTelemetryDB()
	previousRedisEnabled := common.RedisEnabled
	previousRedisClient := common.RDB
	model.SetCoreDB(db)
	model.SetTelemetryDB(db)
	common.RedisEnabled = false
	common.RDB = nil
	t.Cleanup(func() {
		model.SetCoreDB(previousDB)
		model.SetTelemetryDB(previousLogDB)
		common.RedisEnabled = previousRedisEnabled
		common.RDB = previousRedisClient
	})
	if err := db.AutoMigrate(&model.SchemaVersion{}, &model.Announcement{}, &model.Project{}, &model.ProjectMember{}, &model.ProjectInvite{}, &model.BudgetRequest{}, &model.ProjectRemoval{}, &model.ProjectRemovalKey{}, &model.ProjectBudgetHistory{}, &model.ProjectNotification{}, &model.TokenCredential{}, &model.TokenRotation{}, &model.ProjectSpend{}, &model.ProjectDataMigration{}, &model.User{}, &model.Token{}, &model.File{}, &model.MediaJob{}, &model.SpendBudget{}, &model.SpendBudgetAlert{}, &model.SpendBudgetReservation{}, &model.SpendBudgetCharge{}, &model.TopUp{}, &model.SettlementOutbox{}, &model.SettlementFailure{}); err != nil {
		t.Fatalf("migrate primary schema: %v", err)
	}
	if err := db.Create(&model.SchemaVersion{Scope: "primary", Version: model.SchemaVersionCurrent}).Error; err != nil {
		t.Fatalf("insert schema version: %v", err)
	}

	health := newRuntimeHealth()
	health.refreshDependencies(t.Context())
	if err := db.Where("scope = ?", "primary").Delete(&model.SchemaVersion{}).Error; err != nil {
		t.Fatalf("delete schema version: %v", err)
	}

	engine := gin.New()
	engine.GET("/readyz", health.readyz)
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected cached ready snapshot, got %d: %s", recorder.Code, recorder.Body.String())
	}

	health.refreshDependencies(t.Context())
	recorder = httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected refreshed schema failure, got %d: %s", recorder.Code, recorder.Body.String())
	}
}

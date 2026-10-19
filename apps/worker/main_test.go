package main

import (
	"context"
	"testing"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/worker"
)

type selectionTestTask struct {
	name string
}

func (t selectionTestTask) Name() string            { return t.name }
func (selectionTestTask) Interval() time.Duration   { return time.Minute }
func (selectionTestTask) Run(context.Context) error { return nil }

func TestHasPrimaryDatabaseConfig(t *testing.T) {
	previousURL := common.AppConfig.Database.URL
	defer func() {
		common.AppConfig.Database.URL = previousURL
	}()

	t.Setenv("DATABASE_URL", "")
	t.Setenv("SQL_DSN", "")
	common.AppConfig.Database.URL = ""
	if hasPrimaryDatabaseConfig() {
		t.Fatal("expected no primary database config")
	}

	common.AppConfig.Database.URL = "postgresql://config/database"
	if !hasPrimaryDatabaseConfig() {
		t.Fatal("expected YAML database URL to enable database config")
	}

	common.AppConfig.Database.URL = ""
	t.Setenv("SQL_DSN", "postgresql://legacy/database")
	if !hasPrimaryDatabaseConfig() {
		t.Fatal("expected SQL_DSN fallback to enable database config")
	}
}

func TestIsProductionWorkerRuntimeHonorsLazuEnv(t *testing.T) {
	for _, envName := range []string{"APP_ENV", "LAZU_ENV", "ENVIRONMENT", "NODE_ENV", "STRIPE_ENV"} {
		t.Setenv(envName, "")
	}
	t.Setenv("LAZU_ENV", "production")

	if !isProductionWorkerRuntime() {
		t.Fatal("expected LAZU_ENV=production to enable worker production runtime")
	}
}

func TestBuildWorkerTasksOnlyAddsDatabaseTasksWhenReady(t *testing.T) {
	withoutDatabase := buildWorkerTasks(&workerResources{}, false)
	if len(withoutDatabase) != 0 {
		t.Fatalf("expected no tasks without database, got %d", len(withoutDatabase))
	}

	withDatabase := buildWorkerTasks(&workerResources{databaseReady: true}, false)
	extensionTasks := len(worker.ExtensionTasks())
	if len(withDatabase) != 21+extensionTasks {
		t.Fatalf("expected database-backed tasks, got %d", len(withDatabase))
	}

	oneShot := buildWorkerTasks(&workerResources{databaseReady: true}, true)
	if len(oneShot) != 27+extensionTasks {
		t.Fatalf("expected hosted one-shot task coverage, got %d", len(oneShot))
	}

	// channel_probe fills stability gaps for routes real traffic never
	// exercises, so it has to run on the long-running worker rather than
	// being hosted-only: its whole value is a steady low-frequency cadence.
	longRunning := make(map[string]bool, len(withDatabase))
	for _, task := range withDatabase {
		longRunning[task.Name()] = true
	}
	if !longRunning["channel_probe"] {
		t.Fatal("channel_probe missing from the long-running worker registry")
	}
	// project_auto_reload charges saved cards off the request path; it must
	// run on the long-running worker and in a scheduled batch alike.
	if !longRunning["project_auto_reload"] {
		t.Fatal("project_auto_reload missing from the long-running worker registry")
	}
	for _, name := range []string{"control_plane_snapshot", "model_ranking_refresh", "route_health_rollup", "route_health_rollup_watch", "slow_query_report"} {
		if !longRunning[name] {
			t.Fatalf("jobs SQL task %q absent", name)
		}
	}
	longRunningNames := make(map[string]bool, len(withDatabase))
	for _, task := range withDatabase {
		longRunningNames[task.Name()] = true
	}
	oneShotNames := make(map[string]bool, len(oneShot))
	for _, task := range oneShot {
		oneShotNames[task.Name()] = true
	}
	for _, hostedOnly := range []string{
		"relay_event_drain",
		"log_retention",
		"user_file_cleanup",
		"channel_maintenance",
		"pg_dump_backup",
		"archive_compactor",
	} {
		if longRunningNames[hostedOnly] {
			t.Fatalf("hosted-only task %q leaked into long-running worker", hostedOnly)
		}
		if !oneShotNames[hostedOnly] {
			t.Fatalf("hosted-only task %q missing from run-once registry", hostedOnly)
		}
	}
}

func TestSelectWorkerTasks(t *testing.T) {
	tasks := []worker.Task{
		selectionTestTask{name: "alpha"},
		selectionTestTask{name: "beta"},
	}
	selected, err := selectWorkerTasks(tasks, " beta,alpha ")
	if err != nil {
		t.Fatalf("selectWorkerTasks returned error: %v", err)
	}
	if len(selected) != 2 || selected[0].Name() != "alpha" || selected[1].Name() != "beta" {
		t.Fatalf("unexpected selection: %+v", selected)
	}
}

func TestSelectWorkerTasksRejectsUnknownTask(t *testing.T) {
	_, err := selectWorkerTasks(
		[]worker.Task{selectionTestTask{name: "alpha"}},
		"missing",
	)
	if err == nil {
		t.Fatal("expected unknown task error")
	}
}

func TestSelectWorkerTasksRequiresSelection(t *testing.T) {
	_, err := selectWorkerTasks(
		[]worker.Task{selectionTestTask{name: "alpha"}},
		" ",
	)
	if err == nil {
		t.Fatal("expected empty selection error")
	}
}

func TestReleaseNoteCommandExitCode(t *testing.T) {
	deployment := &model.ReleaseDeployment{ReleaseNoteStatus: model.ReleaseNoteStatusGenerated}
	announcement := &model.Announcement{}
	if got := releaseNoteCommandExitCode(deployment, announcement); got != 0 {
		t.Fatalf("generated release note should exit zero, got %d", got)
	}
	if got := releaseNoteCommandExitCode(deployment, nil); got == 0 {
		t.Fatal("release note without announcement should exit non-zero")
	}
	if got := releaseNoteCommandExitCode(nil, nil); got == 0 {
		t.Fatal("skipped release note should exit non-zero")
	}
}

func TestDeploymentAnnouncementModes(t *testing.T) {
	for _, mode := range []string{"silent", "changelog", "notice", "critical"} {
		if !isSupportedDeploymentAnnouncementMode(mode) {
			t.Fatalf("expected %q to be supported", mode)
		}
	}
	if isSupportedDeploymentAnnouncementMode("all-users") {
		t.Fatal("unexpected unsupported announcement mode")
	}
}

func TestProductionWorkerHonorsUnrecognizedProductionMarkers(t *testing.T) {
	t.Setenv("RETIRED_WORKER_ENV", "production")
	if !isProductionWorkerRuntime() {
		t.Fatal("an unfamiliar production marker must keep worker protections enabled")
	}
}

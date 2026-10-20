package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"

	"github.com/joho/godotenv"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/logger"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/service"
	"github.com/lazu-ai/lazu/internal/service/uploader"
	"github.com/lazu-ai/lazu/internal/setting"
	"github.com/lazu-ai/lazu/internal/setting/email_setting"
	"github.com/lazu-ai/lazu/internal/worker"
)

type workerResources struct {
	databaseReady bool
}

type workerInitMode int

const (
	workerInitLongRunning workerInitMode = iota
	workerInitOneShot
)

func hasPrimaryDatabaseConfig() bool {
	return strings.TrimSpace(os.Getenv("DATABASE_URL")) != "" ||
		strings.TrimSpace(common.AppConfig.Database.URL) != "" ||
		strings.TrimSpace(os.Getenv("SQL_DSN")) != ""
}

func isProductionWorkerRuntime() bool {
	return common.IsProductionRuntime()
}

func initWorkerResources(mode workerInitMode) (*workerResources, error) {
	_ = godotenv.Load(".env")
	_ = os.Setenv("PROCESS_ROLE", common.ProcessRoleWorker)
	common.InitEnv()
	email_setting.LoadFromAppConfig(common.AppConfig)
	setting.LoadStripeFromAppConfig(common.AppConfig.Payment.Stripe)
	if mode == workerInitOneShot {
		// Schema migration belongs to an explicit deployment/leader step. A
		// cron-started batch must only connect to the already-migrated schema.
		common.IsMasterNode = false
	}
	logger.SetupLogger()
	service.InitTokenEncoders()

	resources := &workerResources{}
	if !hasPrimaryDatabaseConfig() {
		if isProductionWorkerRuntime() {
			return nil, errors.New("worker primary database config is required in production")
		}
		common.SysLog("worker primary database config not found; DB-backed routing tasks disabled")
		return resources, nil
	}
	if err := model.InitDB(); err != nil {
		return nil, err
	}
	model.InitOptionMap()
	setting.ApplyCredentialConfig()
	if err := model.InitLogDB(); err != nil {
		return nil, err
	}
	if err := common.InitRedisClient(); err != nil {
		return nil, err
	}
	if err := model.InitGatewayLedger(); err != nil {
		return nil, err
	}
	model.GetPricing()
	resources.databaseReady = true
	return resources, nil
}

func initWorkerDBOnly() error {
	_ = godotenv.Load(".env")
	_ = os.Setenv("PROCESS_ROLE", common.ProcessRoleWorker)
	common.InitEnv()
	setting.LoadStripeFromAppConfig(common.AppConfig.Payment.Stripe)
	logger.SetupLogger()
	if !hasPrimaryDatabaseConfig() {
		return errors.New("worker primary database config is required")
	}
	if err := model.InitDB(); err != nil {
		return err
	}
	model.InitOptionMap()
	setting.ApplyCredentialConfig()
	return nil
}

// scheduledMaintenanceEnabled lets a long-running jobs container own the
// clock-scheduled maintenance that only an all-in-one API starts: JSONL
// upload, pg_dump, archive compaction, user-file cleanup and log retention.
// A split deployment (API with PROCESS_ROLE=api) that has no external
// run-once scheduler turns it on; it stays off by default so an all-in-one
// API and its jobs container never run the same maintenance twice.
func scheduledMaintenanceEnabled() bool {
	return common.GetEnvOrDefaultBool("LAZU_JOBS_SCHEDULED_MAINTENANCE", false)
}

// startScheduledMaintenance starts the same schedulers the all-in-one API
// runs, so times, notifications and digest health records are unchanged.
// The upload reads the JSONL shards the API writes, so the jobs container
// must mount the API's data volume.
func startScheduledMaintenance() {
	common.SysLog("worker: scheduled maintenance enabled (upload, pg_dump, compactor, user-file cleanup, log retention)")
	uploader.StartUploadScheduler()
	uploader.StartPgDumpScheduler()
	uploader.StartCompactorScheduler()
	service.StartUserFileCleanup()
	service.StartLogRetention()
}

func buildWorkerTasks(resources *workerResources, oneShot bool) []worker.Task {
	configuredTasks := []worker.Task{}
	if resources != nil && resources.databaseReady {
		zeroTokenMonitor := worker.Task(worker.NewRelayZeroTokenMonitorTask())
		dashboardRollup := worker.Task(worker.NewDashboardUsageRollupTask())
		if oneShot {
			zeroTokenMonitor = worker.NewRelayZeroTokenMonitorOneShotTask()
			dashboardRollup = worker.NewDashboardUsageRollupOneShotTask()
		}
		configuredTasks = append(configuredTasks,
			worker.NewControlPlaneSnapshotTask(),
			worker.NewModelRankingRefreshTask(),
			zeroTokenMonitor,
			worker.NewSettlementFailureReconcilerTask(),
			worker.NewSettlementOutboxReconcilerTask(),
			worker.NewProjectNotificationTask(),
			worker.NewGatewayPerformanceTask(),
			worker.NewUsageDigestTask(),
			worker.NewMailArchiveTask(),
			worker.NewProjectPaymentDocumentTask(),
			worker.NewProjectAutoReloadTask(), // charges saved cards for wallets under their reload line
			dashboardRollup,
			worker.NewMediaJobPollerTask(),
			worker.NewRoutingQualityBackfillTask(), // runs worker.BackfillQualitySignals
			worker.NewRoutingRetentionTask(),       // runs worker.PurgeOldRoutingData
			worker.NewChannelProbeTask(),           // fills health gaps real traffic leaves
			worker.NewPricingCalendarTask(),
			worker.NewAbilityAuditTask(),           // keeps abilities in step with channel model lists
			worker.NewModelsDevSyncTask(),          // daily model facts + review queue from models.dev
			worker.NewRouteHealthRollupTask(),      // hourly route health rows the health pages read
			worker.NewRouteHealthRollupWatchTask(), // mails when that rollup stops advancing
			worker.NewSlowQueryReportTask("jobs"),  // mails this process's slow SQL, at most hourly
		)
		configuredTasks = append(configuredTasks, worker.ExtensionTasks()...)
		if oneShot {
			// These service-level loops still run inside the API for self-hosted
			// deployments. Register them only for externally scheduled batches
			// so the long-running worker cannot duplicate maintenance.
			configuredTasks = append(configuredTasks,
				worker.NewRelayEventDrainTask(),
				worker.NewLogRetentionTask(),
				worker.NewUserFileCleanupTask(),
				worker.NewChannelMaintenanceTask(),
				worker.NewPgDumpBackupTask(),
				worker.NewArchiveCompactorTask(),
			)
		}
	}
	return configuredTasks
}

func selectWorkerTasks(tasks []worker.Task, rawNames string) ([]worker.Task, error) {
	requested := make(map[string]struct{})
	for _, name := range strings.Split(rawNames, ",") {
		name = strings.TrimSpace(name)
		if name != "" {
			requested[name] = struct{}{}
		}
	}
	if len(requested) == 0 {
		return nil, errors.New("LAZU_WORKER_TASKS must select at least one task")
	}

	selected := make([]worker.Task, 0, len(requested))
	for _, task := range tasks {
		if _, ok := requested[task.Name()]; ok {
			selected = append(selected, task)
			delete(requested, task.Name())
		}
	}
	if len(requested) > 0 {
		unknown := make([]string, 0, len(requested))
		for name := range requested {
			unknown = append(unknown, name)
		}
		slices.Sort(unknown)
		return nil, fmt.Errorf("unknown worker tasks: %s", strings.Join(unknown, ", "))
	}
	return selected, nil
}

func runReleaseNotePreviousSHACommand() int {
	if err := initWorkerDBOnly(); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		return 1
	}
	defer func() {
		_ = model.CloseDB()
	}()
	sha, err := service.LatestReleaseDeploymentCommitSHA()
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		return 1
	}
	fmt.Println(sha)
	return 0
}

func releaseNoteCommandExitCode(deployment *model.ReleaseDeployment, announcement *model.Announcement) int {
	if deployment == nil || announcement == nil {
		return 1
	}
	return 0
}

func isSupportedDeploymentAnnouncementMode(mode string) bool {
	switch strings.TrimSpace(mode) {
	case "silent", model.AnnouncementDeliveryChangelog, model.AnnouncementDeliveryNotice, model.AnnouncementDeliveryCritical:
		return true
	default:
		return false
	}
}

func runRecordReleaseDeploymentCommand(args []string) int {
	flags := flag.NewFlagSet("record-release-deployment", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	commitSHA := flags.String("commit-sha", "", "current deployed commit SHA")
	previousSHA := flags.String("previous-sha", "", "previous user-visible commit SHA")
	version := flags.String("version", "", "deployed version label")
	announcementMode := flags.String("announcement-mode", "silent", "silent, changelog, notice, or critical")
	if err := flags.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		return 1
	}
	mode := strings.TrimSpace(*announcementMode)
	if !isSupportedDeploymentAnnouncementMode(mode) {
		fmt.Fprintf(os.Stderr, "unsupported announcement mode %q\n", mode)
		return 1
	}
	summaryBytes, err := io.ReadAll(io.LimitReader(os.Stdin, 1<<20))
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		return 1
	}
	if err := initWorkerDBOnly(); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		return 1
	}
	defer func() {
		_ = model.CloseDB()
	}()
	if mode == "silent" {
		deployment, err := service.RecordReleaseDeployment(service.ReleaseDeploymentRecordRequest{
			CommitSHA:         *commitSHA,
			PreviousCommitSHA: *previousSHA,
			Version:           *version,
		})
		if err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			return 1
		}
		fmt.Printf("deployment recorded silently deployment_id=%d\n", deployment.ID)
		return 0
	}

	deployment, announcement, err := service.GenerateAndRecordReleaseNote(context.Background(), service.ReleaseNoteGenerationRequest{
		CommitSHA:         *commitSHA,
		PreviousCommitSHA: *previousSHA,
		Version:           *version,
		CommitSummary:     string(summaryBytes),
		Delivery:          mode,
		Draft:             true,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		return 1
	}
	if announcement != nil {
		fmt.Printf("release note drafted delivery=%s deployment_id=%d announcement_id=%d\n", mode, deployment.ID, announcement.ID)
		return releaseNoteCommandExitCode(deployment, announcement)
	}
	fmt.Printf("release note %s deployment_id=%d reason=%s\n", deployment.ReleaseNoteStatus, deployment.ID, deployment.ReleaseNoteError)
	return releaseNoteCommandExitCode(deployment, announcement)
}

func runGenerateReleaseNoteCommand(args []string) int {
	flags := flag.NewFlagSet("generate-release-note", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	commitSHA := flags.String("commit-sha", "", "current deployed commit SHA")
	previousSHA := flags.String("previous-sha", "", "previous deployed commit SHA")
	version := flags.String("version", "", "deployed version label")
	delivery := flags.String("delivery", model.AnnouncementDeliveryNotice, "changelog, notice, or critical")
	if err := flags.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		return 1
	}
	summaryBytes, err := io.ReadAll(io.LimitReader(os.Stdin, 1<<20))
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		return 1
	}
	if err := initWorkerDBOnly(); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		return 1
	}
	defer func() {
		_ = model.CloseDB()
	}()
	deployment, announcement, err := service.GenerateAndRecordReleaseNote(context.Background(), service.ReleaseNoteGenerationRequest{
		CommitSHA:         *commitSHA,
		PreviousCommitSHA: *previousSHA,
		Version:           *version,
		CommitSummary:     string(summaryBytes),
		Delivery:          *delivery,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		return 1
	}
	if deployment == nil {
		fmt.Println("release note skipped")
		return releaseNoteCommandExitCode(deployment, announcement)
	}
	if announcement != nil {
		fmt.Printf("release note generated deployment_id=%d announcement_id=%d\n", deployment.ID, announcement.ID)
		return releaseNoteCommandExitCode(deployment, announcement)
	}
	fmt.Printf("release note %s deployment_id=%d reason=%s\n", deployment.ReleaseNoteStatus, deployment.ID, deployment.ReleaseNoteError)
	return releaseNoteCommandExitCode(deployment, announcement)
}

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "release-note-previous-sha":
			os.Exit(runReleaseNotePreviousSHACommand())
		case "generate-release-note":
			os.Exit(runGenerateReleaseNoteCommand(os.Args[2:]))
		case "record-release-deployment":
			os.Exit(runRecordReleaseDeploymentCommand(os.Args[2:]))
		}
		if run, ok := extensionCommands[os.Args[1]]; ok {
			os.Exit(run(os.Args[2:]))
		}
	}

	oneShot := len(os.Args) > 1 && os.Args[1] == "run-once"
	mode := workerInitLongRunning
	if oneShot {
		mode = workerInitOneShot
	}
	resources, err := initWorkerResources(mode)
	if err != nil {
		common.FatalLog("worker init failed: " + err.Error())
		return
	}
	defer func() {
		if resources != nil && resources.databaseReady {
			if err := model.CloseDB(); err != nil {
				common.SysError("worker failed to close database: " + err.Error())
			}
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if !oneShot && resources.databaseReady {
		model.StartRelayEventConsumer(ctx)
		go model.UpdateUsageData()
		if engine := model.GatewayLedger(); engine != nil {
			go engine.Run(ctx, fmt.Sprintf("ledger-jobs-%d", os.Getpid()), func(err error) { common.SysError("ledger writer: " + err.Error()) })
		}
	}
	if !oneShot && resources.databaseReady && scheduledMaintenanceEnabled() {
		startScheduledMaintenance()
	}

	if oneShot {
		selected, selectErr := selectWorkerTasks(
			buildWorkerTasks(resources, true),
			os.Getenv("LAZU_WORKER_TASKS"),
		)
		if selectErr != nil {
			fmt.Fprintln(os.Stderr, selectErr.Error())
			if resources != nil && resources.databaseReady {
				_ = model.CloseDB()
			}
			os.Exit(1)
		}
		if runErr := worker.RunOnce(ctx, selected...); runErr != nil {
			fmt.Fprintln(os.Stderr, runErr.Error())
			if resources != nil && resources.databaseReady {
				_ = model.CloseDB()
			}
			os.Exit(1)
		}
		return
	}

	tasks := buildWorkerTasks(resources, false)
	if names := strings.TrimSpace(os.Getenv("LAZU_WORKER_TASKS")); names != "" {
		selected, err := selectWorkerTasks(tasks, names)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		tasks = selected
	}
	worker.Run(ctx, tasks...)
}

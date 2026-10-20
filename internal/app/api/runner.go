package api

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/lazu-ai/lazu/internal/httperr"
	"github.com/lazu-ai/lazu/internal/types"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/controller"
	"github.com/lazu-ai/lazu/internal/logger"
	"github.com/lazu-ai/lazu/internal/middleware"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/router"
	"github.com/lazu-ai/lazu/internal/service"
	"github.com/lazu-ai/lazu/internal/service/alerting"
	"github.com/lazu-ai/lazu/internal/service/uploader"
	"github.com/lazu-ai/lazu/internal/setting"
	"github.com/lazu-ai/lazu/internal/setting/email_setting"
	"github.com/lazu-ai/lazu/internal/worker"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/securecookie"
	"github.com/joho/godotenv"

	_ "net/http/pprof"
)

func Run() {
	startTime := time.Now()

	err := InitResources()
	if err != nil {
		common.FatalLog("failed to initialize resources: " + err.Error())
		return
	}

	if os.Getenv("GIN_MODE") != "debug" {
		gin.SetMode(gin.ReleaseMode)
	}
	if common.DebugEnabled {
		common.SysLog("running in debug mode")
	}

	defer func() {
		// 关闭日志写入器，确保数据完整写入
		if err := service.CloseModelLogWriter(); err != nil {
			common.SysLog(fmt.Sprintf("failed to close model log writer: %v", err))
		}
		if err := service.CloseErrorLogWriter(); err != nil {
			common.SysLog(fmt.Sprintf("failed to close error log writer: %v", err))
		}

		err := model.CloseDB()
		if err != nil {
			common.FatalLog("failed to close database: " + err.Error())
		}
	}()

	if common.RedisEnabled || (model.CoreDB() != nil && model.CoreDB().Dialector.Name() == "postgres") {
		// for compatibility with old versions
		common.MemoryCacheEnabled = true
	}
	if common.MemoryCacheEnabled {
		common.SysDebug("memory cache enabled")
		common.SysDebug(fmt.Sprintf("sync frequency: %d seconds", common.SyncFrequency))

		// Add panic recovery and retry for InitChannelCache
		func() {
			defer func() {
				if r := recover(); r != nil {
					common.SysLog(fmt.Sprintf("InitChannelCache panic: %v, retrying once", r))
					// Retry once
					_, _, fixErr := model.FixAbility()
					if fixErr != nil {
						common.FatalLog(fmt.Sprintf("InitChannelCache failed: %s", fixErr.Error()))
					}
				}
			}()
			runStartupTask("channel_cache", model.InitChannelCache)
		}()

		go model.SyncChannelCache(common.SyncFrequency)
	}

	// The breaker's hot-path view refreshes on its own schedule. It must not
	// depend on the channel cache being enabled: a deployment without it would
	// otherwise record verdicts that routing never acts on.
	go model.SyncRouteHealthCache(common.SyncFrequency)
	go model.SyncCatalogCache(common.SyncFrequency)

	// 热更新配置
	common.SafeGo("sync-options", func() {
		model.SyncOptions(common.SyncFrequency)
	})
	common.StartBackgroundLoops()
	common.StartSystemMonitor()
	service.StartDiskWatch()

	if apiBackgroundTasksEnabled() {
		if os.Getenv("CHANNEL_UPDATE_FREQUENCY") != "" {
			frequency, err := strconv.Atoi(os.Getenv("CHANNEL_UPDATE_FREQUENCY"))
			if err != nil {
				common.FatalLog("failed to parse CHANNEL_UPDATE_FREQUENCY: " + err.Error())
			}
			common.SafeGo("channel-auto-update", func() {
				controller.AutomaticallyUpdateChannels(frequency)
			})
		}

		common.SafeGo("channel-auto-test", controller.AutomaticallyTestChannels)

	}

	if os.Getenv("ENABLE_PPROF") == "true" {
		common.SafeGo("pprof-server", func() {
			log.Println(http.ListenAndServe("127.0.0.1:8005", nil))
		})
		common.SafeGo("runtime-monitor", common.Monitor)
		common.SysLog("pprof enabled")
	}

	signalContext, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	if apiBackgroundTasksEnabled() {
		// Self-hosted/all-in-one launchers such as ./run.sh do not start the
		// separate worker process. Run the same probe and catalog-snapshot task
		// here so idle routes receive evidence in every supported deployment.
		common.SafeCtxGo(signalContext, "channel-probe-worker", func() {
			worker.Run(signalContext, apiBackgroundWorkerTasks()...)
		})
	}
	// Request-serving queries are the ones customers wait on; report this
	// process's slow statements whatever else it runs.
	common.SafeCtxGo(signalContext, "slow-query-report", func() {
		worker.Run(signalContext, worker.NewSlowQueryReportTask("api"))
	})

	// Keep financial/telemetry consumers alive through HTTP draining, then join
	// them before the deferred database close.
	stopWriters := startAPIWriters()
	defer drainAPIWriters(stopWriters)

	// Initialize HTTP server. Dependency checks run in the background so
	// /readyz returns the latest snapshot without adding remote DB and Redis
	// round trips to every health request.
	runtimeState := newRuntimeHealth()
	runtimeState.startDependencyProbe(signalContext, readinessProbeInterval())
	server := gin.New()
	server.Use(common.FinishRelayTimingAfterMiddleware(service.RecordRelayTimingDiagnostics))
	server.Use(middleware.RuntimeIdentity())
	server.GET("/livez", runtimeState.livez)
	server.GET("/readyz", runtimeState.readyz)
	server.Use(gin.CustomRecovery(recoverPanic))
	// This will cause SSE not to work!!!
	//server.Use(gzip.Gzip(gzip.DefaultCompression))
	server.Use(middleware.InternalProxyAuth())
	server.Use(middleware.RequestId())
	server.Use(middleware.StatsMiddleware())
	middleware.SetUpLogger(server)
	// Initialize session store
	const sessionCookieName = "session"
	sessionKey := []byte(common.SessionSecret)
	store := cookie.NewStore(sessionKey)
	sessionCookieSecure := common.ResolveSessionCookieSecure()
	sessionCookieSameSite := common.ResolveSessionCookieSameSite()
	if sessionCookieSameSite == http.SameSiteNoneMode && !sessionCookieSecure {
		common.FatalLog("SESSION_COOKIE_SAME_SITE=none requires SESSION_COOKIE_SECURE=true")
	}
	store.Options(sessions.Options{
		Path:     "/",
		MaxAge:   2592000, // 30 days
		HttpOnly: true,
		Secure:   sessionCookieSecure,
		SameSite: sessionCookieSameSite,
	})
	// Drop stale/invalid session cookies before they reach the session store,
	// otherwise every request carrying one logs a securecookie decode error.
	server.Use(middleware.SessionCookieGuard(sessionCookieName, securecookie.CodecsFromPairs(sessionKey)))
	server.Use(sessions.Sessions(sessionCookieName, store))

	// 设置路由
	router.SetRouter(server)
	var port = os.Getenv("PORT")
	if port == "" {
		port = strconv.Itoa(*common.Port)
	}

	// Log startup success message
	common.LogStartupSuccess(startTime, port)

	httpServer := newHTTPServer(port, server)
	if err = serveHTTP(signalContext, httpServer, runtimeState); err != nil {
		common.FatalLog("failed to start HTTP server: " + err.Error())
	}
}

func drainAPIWriters(stop func()) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := service.DrainRelayDiagnosticWork(ctx); err != nil {
		common.SysError("drain relay diagnostics: " + err.Error())
	}
	stop()
}

func startAPIWriters() func() {
	if !apiBackgroundTasksEnabled() {
		return func() {}
	}
	ctx, cancel := context.WithCancel(context.Background())
	var workers sync.WaitGroup
	start := func(run func()) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			run()
		}()
	}
	start(func() { model.RunRelayEventConsumer(ctx) })
	start(func() { model.RunUsageDataUpdater(ctx) })
	if engine := model.GatewayLedger(); engine != nil {
		start(func() {
			engine.Run(ctx, fmt.Sprintf("ledger-api-%d", os.Getpid()), func(err error) { common.SysError("ledger writer: " + err.Error()) })
		})
	}
	return func() {
		cancel()
		workers.Wait()
		model.SaveUsageDataCache()
	}
}

func apiBackgroundWorkerTasks() []worker.Task {
	return []worker.Task{worker.NewChannelProbeTask(), worker.NewAbilityAuditTask(), worker.NewModelsDevSyncTask(), worker.NewProjectNotificationTask(), worker.NewUsageDigestTask(), worker.NewMailArchiveTask(), worker.NewProjectPaymentDocumentTask(), worker.NewDashboardUsageRollupTask(), worker.NewRoutingRetentionTask(), worker.NewRouteHealthRollupTask(), worker.NewRouteHealthRollupWatchTask()}
}

func newHTTPServer(port string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              net.JoinHostPort(strings.TrimSpace(os.Getenv("LAZU_BIND_HOST")), port),
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
}

func serveHTTP(ctx context.Context, server *http.Server, health *runtimeHealth) error {
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		health.beginDrain()
		drainDelay := configuredDrainDelay()
		if drainDelay > 0 {
			time.Sleep(drainDelay)
		}

		shutdownTimeout := time.Duration(common.GetEnvOrDefault("SHUTDOWN_TIMEOUT_SECONDS", 60)) * time.Second
		shutdownContext, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := server.Shutdown(shutdownContext); err != nil {
			_ = server.Close()
			return fmt.Errorf("graceful shutdown: %w", err)
		}
		if err := controller.WaitForAuthEmailDeliveries(shutdownContext); err != nil {
			return fmt.Errorf("drain authentication email jobs: %w", err)
		}
		return nil
	}
}

func InitResources() error {
	// Initialize resources here if needed
	// This is a placeholder function for future resource initialization
	err := godotenv.Load(".env")
	if err != nil {
		if common.DebugEnabled {
			common.SysLog("No .env file found, using default environment variables. If needed, please create a .env file and set the relevant variables.")
		}
	}

	// 加载环境变量
	common.InitEnv()

	// Apply YAML overrides before options are hydrated
	email_setting.LoadFromAppConfig(common.AppConfig)
	setting.LoadStripeFromAppConfig(common.AppConfig.Payment.Stripe)

	logger.SetupLogger()

	service.InitHttpClient()

	service.InitTokenEncoders()
	service.InitOSSURLCache()

	// Initialize SQL Database
	err = runStartupStep("database", model.InitDB)
	if err != nil {
		common.FatalLog("failed to initialize database: " + err.Error())
		return err
	}

	// Cache-invalidating database migrations and root synchronization need Redis
	// to be ready before their committed changes can leave stale projections.
	err = runStartupStep("redis", common.InitRedisClient)
	if err != nil {
		return err
	}
	// Both SQL stores and options must be initialized before ledger admission
	// installs ownership guards or any one-time telemetry backfill runs.
	runStartupTask("options", func() {
		model.InitOptionMap()
		setting.ApplyCredentialConfig()
	})
	if err = runStartupStep("log_database", model.InitLogDB); err != nil {
		return err
	}
	if err = model.InitGatewayLedger(); err != nil {
		return err
	}
	if err = runStartupStep("retired_referral_cache_invalidation", model.InvalidateRetiredReferralCreditUserCaches); err != nil {
		common.FatalLog("failed to invalidate migrated referral credit caches: " + err.Error())
		return err
	}

	if rootSyncOnStart() {
		if err = runStartupStep("root_sync", model.SyncRootUserFromConfig); err != nil {
			common.FatalLog("failed to sync root user: " + err.Error())
			return err
		}
	}

	// 初始化模型
	runStartupTask("pricing", func() {
		model.WarmCatalogCacheFromCore()
	})
	if apiBackgroundTasksEnabled() {
		if err = runStartupStep("retired_model_rank_recalculation", model.RecalculateRetiredModelRatingRanks); err != nil {
			common.FatalLog("failed to recalculate migrated model rankings: " + err.Error())
			return err
		}
	}

	// File retention owns this container's ephemeral JSONL/log shards, so it
	// must stay local even when database-wide maintenance moves to Jobs.
	service.StartFileRetention()
	if apiBackgroundTasksEnabled() {
		service.StartLogRetention()
		service.StartUserFileCleanup()
		service.StartModelRankingScheduler()
	}

	// The digest is safe on replicated/hosted API processes because Redis
	// claims one delivery per day. Keep it outside the all-in-one-only block so
	// hosted deployments also receive the operations summary.
	service.RegisterOpsDigestSections()
	alerting.StartDailyDigestScheduler()

	if apiBackgroundTasksEnabled() {
		// Hourly upload of JSONL captures to R2 (yaml-gated).
		uploader.StartUploadScheduler()
		// Daily pg_dump → R2, independent cron.
		uploader.StartPgDumpScheduler()
		// Daily compactor: gzips R2 archive files older than 30 days in
		// place, leaving the recent window uncompressed for easy access.
		uploader.StartCompactorScheduler()
	}
	return nil
}

func runStartupStep(name string, fn func() error) error {
	started := time.Now()
	err := fn()
	status := "ok"
	if err != nil {
		status = "error"
	}
	common.SysLog(fmt.Sprintf(
		"startup_step name=%s status=%s duration_ms=%d",
		name,
		status,
		time.Since(started).Milliseconds(),
	))
	return err
}

func runStartupTask(name string, fn func()) {
	started := time.Now()
	fn()
	common.SysLog(fmt.Sprintf(
		"startup_step name=%s status=ok duration_ms=%d",
		name,
		time.Since(started).Milliseconds(),
	))
}

func rootSyncOnStart() bool {
	return common.GetEnvOrDefaultBool("ROOT_SYNC_ON_START", true)
}

func apiBackgroundTasksEnabled() bool {
	return common.ResolveProcessRole(os.Getenv("PROCESS_ROLE")) == common.ProcessRoleAllInOne
}

const maxDrainDelay = 30 * time.Second

func configuredDrainDelay() time.Duration {
	seconds := common.GetEnvOrDefault("SHUTDOWN_DRAIN_DELAY_SECONDS", 5)
	if seconds < 0 {
		common.SysWarn("SHUTDOWN_DRAIN_DELAY_SECONDS must not be negative; using 0")
		return 0
	}
	delay := time.Duration(seconds) * time.Second
	if delay > maxDrainDelay {
		common.SysWarn(fmt.Sprintf(
			"SHUTDOWN_DRAIN_DELAY_SECONDS=%d exceeds the %s maximum; clamping",
			seconds,
			maxDrainDelay,
		))
		return maxDrainDelay
	}
	return delay
}

// recoverPanic keeps the client protocol when a request fails unexpectedly.
func recoverPanic(c *gin.Context, err any) {
	panicErr := types.WrapErr(fmt.Errorf("panic: %v", err), types.ErrorCodeInternalError)
	if strings.HasPrefix(c.Request.URL.Path, "/api/") {
		httperr.Abort(c, panicErr)
		return
	}
	httperr.AbortGateway(c, panicErr)
}

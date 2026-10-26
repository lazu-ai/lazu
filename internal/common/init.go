package common

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/lazu-ai/lazu/internal/constant"
)

var (
	Port         = flag.Int("port", 3000, "the listening port")
	PrintVersion = flag.Bool("version", false, "print version and exit")
	PrintHelp    = flag.Bool("help", false, "print help and exit")
	// 应用日志默认写入 ./data/logs
	LogDir = flag.String("log-dir", "./data/logs", "specify the log directory")
)

func printHelp() {
	fmt.Println("Lazu " + Version + " - LLM Gateway & AI Asset Management System")
	fmt.Println("Source: https://github.com/lazu-ai/lazu")
	fmt.Println("Usage: lazu [--port <port>] [--log-dir <log directory>] [--version] [--help]")
}

// IsProductionRuntime keeps security protections enabled for every *_ENV
// production marker, including deployment-specific or retired marker names.
// APP_ENV is the canonical deployment setting; an unfamiliar production marker
// must never silently downgrade secret requirements or cookie security.
func IsProductionRuntime() bool {
	return productionEnvironment(os.Environ())
}

func productionEnvironment(environment []string) bool {
	for _, entry := range environment {
		name, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		if (strings.HasSuffix(name, "_ENV") || name == "ENVIRONMENT") && strings.EqualFold(value, "production") {
			return true
		}
		if name == "GIN_MODE" && strings.EqualFold(value, "release") {
			return true
		}
	}
	return false
}

// ResolveSessionCookieSecure decides the Secure attribute for the session cookie.
// SESSION_COOKIE_SECURE: "auto" (default) follows the production runtime;
// "true"/"false" force the value, e.g. for HTTPS-terminating proxies or local
// production testing over plain HTTP.
func ResolveSessionCookieSecure() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("SESSION_COOKIE_SECURE"))) {
	case "true", "1", "yes", "on":
		return true
	case "false", "0", "no", "off":
		return false
	default:
		return IsProductionRuntime()
	}
}

// ResolveSessionCookieSameSite keeps the safest default for the normal
// same-origin (or same-site subdomain) console deployment while allowing an
// operator to opt into a genuinely cross-site console/API topology. Browsers
// require SameSite=None cookies to also be Secure; the API runner enforces that
// pairing before it starts serving requests.
func ResolveSessionCookieSameSite() http.SameSite {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("SESSION_COOKIE_SAME_SITE"))) {
	case "none":
		return http.SameSiteNoneMode
	case "lax":
		return http.SameSiteLaxMode
	default:
		return http.SameSiteStrictMode
	}
}

func requireRuntimeSecret(name string, production bool) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "random_string" {
		log.Fatalf("%s is set to the default value 'random_string'; set a strong random value.", name)
	}
	if production && value == "" {
		log.Fatalf("%s is required in production.", name)
	}
	return value
}

func InitEnv() {
	flag.Parse()
	ConfigureLogLevelFromEnv()

	// 先加载 YAML 配置（若存在），作为默认值来源
	LoadAppConfig()

	// 调整日志目录的动态默认值，避免在容器内出现 /data/data/logs
	if *LogDir == "./data/logs" || *LogDir == "data/logs" {
		*LogDir = filepath.Join(GetDataDir(), "logs")
	}

	if *PrintVersion {
		fmt.Println(Version)
		os.Exit(0)
	}

	if *PrintHelp {
		printHelp()
		os.Exit(0)
	}

	productionRuntime := IsProductionRuntime()
	if ss := requireRuntimeSecret("SESSION_SECRET", productionRuntime); ss != "" {
		SessionSecret = ss
	}
	if cs := requireRuntimeSecret("CRYPTO_SECRET", productionRuntime); cs != "" {
		CryptoSecret = cs
	} else {
		CryptoSecret = SessionSecret
	}
	if os.Getenv("SQLITE_PATH") != "" {
		SQLitePath = os.Getenv("SQLITE_PATH")
	}
	if *LogDir != "" {
		var err error
		*LogDir, err = filepath.Abs(*LogDir)
		if err != nil {
			log.Fatal(err)
		}
		if _, err := os.Stat(*LogDir); os.IsNotExist(err) {
			// Create logs directory with parents to ensure ./data/logs works when ./data is missing
			err = os.MkdirAll(*LogDir, 0o755)
			if err != nil {
				log.Fatal(err)
			}
		}
	}

	// 模型日志开关：优先环境变量，否则取 YAML 配置默认
	ModelLogEnabled = GetEnvOrDefaultBool("MODEL_LOG_ENABLED", AppConfig.ModelLogEnabled)
	modelLogPath := os.Getenv("MODEL_LOG_PATH")
	if modelLogPath == "" {
		// Defaults to data/export/requests.jsonl (rotated daily).
		exportDir := GetExportDir()
		if exportDir == "" {
			exportDir = "./data/export"
		}
		absExportDir, err := filepath.Abs(exportDir)
		if err != nil {
			SysError(fmt.Sprintf("failed to resolve export dir %s: %v", exportDir, err))
			absExportDir = exportDir
		}
		if err := os.MkdirAll(absExportDir, 0o755); err != nil {
			SysError(fmt.Sprintf("failed to create export dir %s: %v", absExportDir, err))
		}
		ModelLogPath = filepath.Join(absExportDir, AppConfig.ModelLogFilename)
	} else {
		if !filepath.IsAbs(modelLogPath) {
			// 相对路径基于 export 目录
			modelLogPath = filepath.Join(GetExportDir(), modelLogPath)
		}
		absPath, err := filepath.Abs(modelLogPath)
		if err != nil {
			SysError(fmt.Sprintf("failed to resolve MODEL_LOG_PATH %s: %v", modelLogPath, err))
			ModelLogPath = filepath.Join(GetExportDir(), AppConfig.ModelLogFilename)
		} else {
			ModelLogPath = absPath
		}
	}
	if ModelLogEnabled {
		logDir := filepath.Dir(ModelLogPath)
		if err := os.MkdirAll(logDir, 0o755); err != nil {
			SysError(fmt.Sprintf("failed to create model log directory %s: %v", logDir, err))
		}
	}

	// 错误日志文件路径初始化
	// 注意：必须先读取 ERROR_LOG_ENABLED 环境变量，因为 initConstantEnv() 还没执行
	errorLogEnabled := GetEnvOrDefaultBool("ERROR_LOG_ENABLED", false)
	if errorLogEnabled {
		errorLogPath := os.Getenv("ERROR_LOG_PATH")
		if errorLogPath == "" {
			exportDir := GetExportDir()
			if exportDir == "" {
				exportDir = "./data/export"
			}
			absExportDir, err := filepath.Abs(exportDir)
			if err != nil {
				SysError(fmt.Sprintf("failed to resolve export dir %s: %v", exportDir, err))
				absExportDir = exportDir
			}
			if err := os.MkdirAll(absExportDir, 0o755); err != nil {
				SysError(fmt.Sprintf("failed to create export dir %s: %v", absExportDir, err))
			}
			ErrorLogPath = filepath.Join(absExportDir, "errors.jsonl")
		} else {
			if !filepath.IsAbs(errorLogPath) {
				errorLogPath = filepath.Join(GetExportDir(), errorLogPath)
			}
			absPath, err := filepath.Abs(errorLogPath)
			if err != nil {
				SysError(fmt.Sprintf("failed to resolve ERROR_LOG_PATH %s: %v", errorLogPath, err))
				ErrorLogPath = filepath.Join(GetExportDir(), "errors.jsonl")
			} else {
				ErrorLogPath = absPath
			}
		}
		logDir := filepath.Dir(ErrorLogPath)
		if err := os.MkdirAll(logDir, 0o755); err != nil {
			SysError(fmt.Sprintf("failed to create error log directory %s: %v", logDir, err))
		}
		SysLog(fmt.Sprintf("Error log enabled, path: %s", ErrorLogPath))
	}

	// Initialize variables from constants.go that were using environment variables
	DebugEnabled = os.Getenv("DEBUG") == "true"
	MemoryCacheEnabled = os.Getenv("MEMORY_CACHE_ENABLED") == "true"
	IsMasterNode = ResolveProcessRole(os.Getenv("PROCESS_ROLE")) != ProcessRoleAPI
	NodeName = os.Getenv("NODE_NAME")
	NodeRegion = GetEnvOrDefaultString("NODE_REGION", "")
	NodeProvider = GetEnvOrDefaultString("NODE_PROVIDER", "")

	LogRetentionDays = GetEnvOrDefault("LOG_RETENTION_DAYS", 30)
	LogRetentionIntervalHours = GetEnvOrDefault("LOG_RETENTION_INTERVAL_HOURS", 24)

	// Parse requestInterval and set RequestInterval
	requestInterval, _ = strconv.Atoi(os.Getenv("POLLING_INTERVAL"))
	RequestInterval = time.Duration(requestInterval) * time.Second

	// Initialize variables with GetEnvOrDefault
	SyncFrequency = GetEnvOrDefault("SYNC_FREQUENCY", 60)
	PricingSnapshotEnabled = GetEnvOrDefaultBool("PRICING_SNAPSHOT_ENABLED", true)
	PricingSnapshotRefreshSeconds = GetEnvOrDefault("PRICING_SNAPSHOT_REFRESH_SECONDS", 60)
	RelayEventQueueEnabled = GetEnvOrDefaultBool("RELAY_EVENT_QUEUE_ENABLED", true)
	RelayEventStream = GetEnvOrDefaultString("RELAY_EVENT_STREAM", "lazu:relay_events")
	RelayEventGroup = GetEnvOrDefaultString("RELAY_EVENT_GROUP", "lazu-relay-event-workers")
	RelayEventConsumer = GetEnvOrDefaultString("RELAY_EVENT_CONSUMER", NodeName)
	RelayEventConsumerEnabled = GetEnvOrDefaultBool("RELAY_EVENT_CONSUMER_ENABLED", true)
	HotPathMetricsEnabled = GetEnvOrDefaultBool("HOT_PATH_METRICS_ENABLED", false)
	RedisVerificationCodeEnabled = GetEnvOrDefaultBool("REDIS_VERIFICATION_CODE_ENABLED", false)
	SettlementOutboxEnabled = GetEnvOrDefaultBool("SETTLEMENT_OUTBOX_ENABLED", false)
	for _, notice := range deprecatedEnvNotices() {
		SysLog("deprecated setting: " + notice)
	}
	MetricsEnabled = GetEnvOrDefaultBool("METRICS_ENABLED", false)
	MetricsToken = GetEnvOrDefaultString("METRICS_TOKEN", "")
	OIDCEnabled = GetEnvOrDefaultBool("OIDC_ENABLED", false)
	OIDCIssuerURL = GetEnvOrDefaultString("OIDC_ISSUER_URL", "")
	OIDCClientID = GetEnvOrDefaultString("OIDC_CLIENT_ID", "")
	OIDCClientSecret = GetEnvOrDefaultString("OIDC_CLIENT_SECRET", "")
	OIDCRedirectURL = GetEnvOrDefaultString("OIDC_REDIRECT_URL", "")
	OIDCFrontendBaseURL = GetEnvOrDefaultString("OIDC_FRONTEND_BASE_URL", "")
	OIDCPostLoginRedirectURL = GetEnvOrDefaultString("OIDC_POST_LOGIN_REDIRECT_URL", "/console")
	OIDCScopes = GetEnvOrDefaultString("OIDC_SCOPES", "openid email profile")
	OIDCUsernameClaim = GetEnvOrDefaultString("OIDC_USERNAME_CLAIM", "preferred_username")
	OIDCEmailClaim = GetEnvOrDefaultString("OIDC_EMAIL_CLAIM", "email")
	OIDCRoleClaim = GetEnvOrDefaultString("OIDC_ROLE_CLAIM", "groups")
	// One platform role maps from the IdP: 管理员 (role 100). OIDC_ROOT_ROLES
	// is the retired name for the same mapping; it is still read so an existing
	// deployment that only set it does not demote its administrators.
	OIDCAdminRoles = joinCSV(GetEnvOrDefaultString("OIDC_ADMIN_ROLES", ""), GetEnvOrDefaultString("OIDC_ROOT_ROLES", ""))
	OIDCAutoCreateUsers = GetEnvOrDefaultBool("OIDC_AUTO_CREATE_USERS", true)
	OIDCBreakGlassPasswordEnabled = GetEnvOrDefaultBool("OIDC_BREAK_GLASS_PASSWORD_ENABLED", true)
	GoogleOAuthClientID = GetEnvOrDefaultString("GOOGLE_OAUTH_CLIENT_ID", "")
	GoogleOAuthClientSecret = GetEnvOrDefaultString("GOOGLE_OAUTH_CLIENT_SECRET", "")
	GoogleOAuthRedirectURL = GetEnvOrDefaultString("GOOGLE_OAUTH_REDIRECT_URL", "")
	GitHubOAuthClientID = GetEnvOrDefaultString("GITHUB_OAUTH_CLIENT_ID", "")
	GitHubOAuthClientSecret = GetEnvOrDefaultString("GITHUB_OAUTH_CLIENT_SECRET", "")
	GitHubOAuthRedirectURL = GetEnvOrDefaultString("GITHUB_OAUTH_REDIRECT_URL", "")
	TrustedProxyCIDRs = GetEnvOrDefaultString("TRUSTED_PROXY_CIDRS", TrustedProxyCIDRs)
	RelayTimeout = RelayTimeoutFromEnv()

	// Initialize string variables with GetEnvOrDefaultString
	GeminiSafetySetting = GetEnvOrDefaultString("GEMINI_SAFETY_SETTING", "BLOCK_NONE")
	CohereSafetySetting = GetEnvOrDefaultString("COHERE_SAFETY_SETTING", "NONE")

	initConstantEnv()
}

func initConstantEnv() {
	constant.StreamingTimeout = GetEnvOrDefault("STREAMING_TIMEOUT", constant.DefaultStreamingTimeoutSeconds)
	constant.DifyDebug = GetEnvOrDefaultBool("DIFY_DEBUG", true)
	constant.MaxFileDownloadMB = GetEnvOrDefault("MAX_FILE_DOWNLOAD_MB", 20)
	constant.MaxVideoDownloadMB = GetEnvOrDefault("MAX_VIDEO_DOWNLOAD_MB", 200)
	// ForceStreamOption 覆盖请求参数，强制返回usage信息
	constant.ForceStreamOption = GetEnvOrDefaultBool("FORCE_STREAM_OPTION", true)
	constant.GetMediaToken = GetEnvOrDefaultBool("GET_MEDIA_TOKEN", true)
	constant.GetMediaTokenNotStream = GetEnvOrDefaultBool("GET_MEDIA_TOKEN_NOT_STREAM", true)
	constant.AzureDefaultAPIVersion = GetEnvOrDefaultString("AZURE_DEFAULT_API_VERSION", "2025-04-01-preview")
	constant.GeminiVisionMaxImageNum = GetEnvOrDefault("GEMINI_VISION_MAX_IMAGE_NUM", 16)
	constant.NotifyLimitCount = GetEnvOrDefault("NOTIFY_LIMIT_COUNT", 2)
	constant.NotificationLimitDurationMinute = GetEnvOrDefault("NOTIFICATION_LIMIT_DURATION_MINUTE", 10)
	// GenerateDefaultToken 是否生成初始令牌，默认关闭。
	constant.GenerateDefaultToken = GetEnvOrDefaultBool("GENERATE_DEFAULT_TOKEN", false)
	// 是否启用错误日志
	constant.ErrorLogEnabled = GetEnvOrDefaultBool("ERROR_LOG_ENABLED", false)
}

func joinCSV(values ...string) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			parts = append(parts, trimmed)
		}
	}
	return strings.Join(parts, ",")
}

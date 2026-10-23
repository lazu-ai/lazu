package common

import (
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/lazu-ai/lazu/internal/storage"
	yaml "gopkg.in/yaml.v3"
)

var envPlaceholderPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// AppConfig holds YAML-configurable settings with sensible defaults.
type AppConfigStruct struct {
	Data struct {
		Dir          string `yaml:"dir"`
		LogsSubDir   string `yaml:"logs_subdir"`
		ExportSubDir string `yaml:"export_subdir"`
	} `yaml:"data"`

	Database struct {
		URL string `yaml:"url"`
	} `yaml:"database"`

	Root struct {
		Username string `yaml:"username"`
		Password string `yaml:"password"`
	} `yaml:"root"`

	Payment struct {
		Stripe PaymentStripeConfig `yaml:"stripe"`
	} `yaml:"payment"`

	// Model log JSONL control
	ModelLogEnabled  bool   `yaml:"model_log_enabled"`
	ModelLogFilename string `yaml:"model_log_filename"`

	Upload struct {
		Enable            bool             `yaml:"enable"`
		Schedule          string           `yaml:"schedule"`
		Timezone          string           `yaml:"timezone"`
		DeleteAfterVerify bool             `yaml:"delete_after_verify"`
		VerifyViaPublic   bool             `yaml:"verify_via_public_url"`
		PublicBaseURL     string           `yaml:"public_base_url"`
		S3                storage.S3Config `yaml:"s3"`
		Email             struct {
			Enable bool   `yaml:"enable"`
			To     string `yaml:"to"`
			From   string `yaml:"from"`
		} `yaml:"email"`
	} `yaml:"upload"`

	ArtifactStorage struct {
		Enable        bool   `yaml:"enable"`
		BasePath      string `yaml:"base_path"`
		PublicBaseURL string `yaml:"public_base_url"`
		// APIVideoTTLHours keeps videos generated through /v1/videos for this
		// many hours instead of the 30-day file default. Videos made by an
		// extension (Studio) keep the default. 0 leaves the default.
		APIVideoTTLHours int              `yaml:"api_video_ttl_hours"`
		S3               storage.S3Config `yaml:"s3"`
	} `yaml:"artifact_storage"`

	OSSURLCache struct {
		Enable          bool             `yaml:"enable"`
		RequestFilePath string           `yaml:"request_file_path"`
		CacheDir        string           `yaml:"cache_dir"`
		CacheTTLMinute  int              `yaml:"cache_ttl_minutes"`
		S3              storage.S3Config `yaml:"s3"`
	} `yaml:"oss_url_cache"`

	Resend struct {
		APIKey string `yaml:"api_key"`
		From   string `yaml:"from"`
	} `yaml:"resend"`

	SMTP struct {
		Server     string `yaml:"server"`
		Port       int    `yaml:"port"`
		SSLEnabled bool   `yaml:"ssl_enabled"`
		Account    string `yaml:"account"`
		From       string `yaml:"from"`
		FromName   string `yaml:"from_name"`
		Token      string `yaml:"token"`
	} `yaml:"smtp"`

	EmailNotifications struct {
		FromName      string                `yaml:"from_name"`
		BatchSize     int                   `yaml:"batch_size"`
		AdminEmail    string                `yaml:"admin_email"`
		BalanceChange EmailSwitchConfig     `yaml:"balance_change"`
		LowBalance    LowBalanceEmailConfig `yaml:"low_balance"`
	} `yaml:"email_notifications"`

	// Site names this deployment: where its web console and gateway are
	// reached, who operates it, and where mail tells people to write. Every
	// field is optional; an empty one falls back as documented on the
	// accessor that reads it, never to another deployment's values.
	Site SiteConfig `yaml:"site"`

	// Telegram connects one bot to this deployment. Every field is optional;
	// without a bot token nothing Telegram-related runs.
	Telegram TelegramConfig `yaml:"telegram"`

	// ProbeIdentities are the client identities that health probes and
	// observation checks present to a source restricted to one client, keyed
	// by the restriction (ProbeClientClaudeCode). Customer requests never use
	// them. Without an entry, probes reach such a source with no client
	// identity and are usually refused.
	ProbeIdentities map[string]ProbeIdentityConfig `yaml:"probe_identities"`

	// SourceChecks are the bars a new source's routes must clear while they
	// are watched before going live. It is read from the raw source_checks
	// section, one key at a time, so a malformed value falls back to its
	// default instead of failing the whole file.
	SourceChecks SourceChecksConfig `yaml:"-"`
}

// SourceChecksConfig sets how a watched route is judged. Every field has a
// generic default in defaultConfig; a deployment tunes them in config.yaml.
type SourceChecksConfig struct {
	// ObservationDays is how long a new route is watched before it may
	// carry traffic.
	ObservationDays int `yaml:"observation_days"`
	// MinSamples is the least number of liveness probes that can back a
	// success-rate verdict.
	MinSamples int `yaml:"min_samples"`
	// MinSuccessRate is the share of liveness probes that must succeed.
	MinSuccessRate float64 `yaml:"min_success_rate"`
	// MaxBillingRatio is how far billed input may exceed what was sent, as
	// a ratio, before the billing check fails.
	MaxBillingRatio float64 `yaml:"max_billing_ratio"`
	// BillingFramingTokens is an absolute excess that passes the billing
	// check whatever the ratio says, for chat templates with a fixed cost.
	BillingFramingTokens int `yaml:"billing_framing_tokens"`
	// LongContextTokens is the size of the long-context check's input.
	LongContextTokens int `yaml:"long_context_tokens"`
	// LongContextMinShare is how much of that input the upstream must bill.
	LongContextMinShare float64 `yaml:"long_context_min_share"`
	// CheckEveryHours is how often each check runs on a watched route.
	CheckEveryHours int `yaml:"check_every_hours"`
}

// Observation is how long a new route is watched.
func (c SourceChecksConfig) Observation() time.Duration {
	return time.Duration(c.ObservationDays) * 24 * time.Hour
}

// CheckEvery is how often each check runs on a watched route.
func (c SourceChecksConfig) CheckEvery() time.Duration {
	return time.Duration(c.CheckEveryHours) * time.Hour
}

// SiteConfig identifies one deployment.
type SiteConfig struct {
	WebOrigin    string `yaml:"web_origin"`    // e.g. https://console.example.com
	APIOrigin    string `yaml:"api_origin"`    // e.g. https://api.example.com
	Operator     string `yaml:"operator"`      // legal operator named on legal pages
	SupportEmail string `yaml:"support_email"` // contact address in mail footers
	// SourceURL is where this deployment's source code is published. AGPL
	// section 13 asks a network service to offer it; the console links here.
	SourceURL string `yaml:"source_url"`
}

// TelegramConfig is the deployment's Telegram bot. The token is a secret:
// set it through the TELEGRAM_BOT_TOKEN environment variable or an
// env-expanded placeholder, never in a file that is committed.
type TelegramConfig struct {
	BotToken string `yaml:"bot_token"`
	// APIBase is the Bot API server. Empty uses https://api.telegram.org;
	// set it to run against a local telegram-bot-api server.
	APIBase string `yaml:"api_base"`
	// OpsChat receives a copy of every operational alert: a chat id such as
	// -1001234567890, optionally followed by :<topic id> for one topic of a
	// forum group. Empty sends alerts by mail only.
	OpsChat string `yaml:"ops_chat"`
	// Updates is how the bot receives messages: "webhook" (Telegram posts
	// to /api/telegram/webhook; needs a public HTTPS origin and
	// WebhookSecret), "polling" (one process asks Telegram; for a single
	// replica without a public origin) or empty (the bot only sends).
	Updates string `yaml:"updates"`
	// WebhookSecret is echoed by Telegram in every webhook call; requests
	// without it are refused. 1-256 characters of A-Z, a-z, 0-9, _ and -.
	WebhookSecret string `yaml:"webhook_secret"`
	// WebhookURL overrides where Telegram posts updates. Empty uses
	// site.api_origin, then the web origin, plus /api/telegram/webhook.
	WebhookURL string `yaml:"webhook_url"`
	// Answer lets the bot answer questions from this deployment's public
	// documentation.
	Answer TelegramAnswerConfig `yaml:"answer"`
}

// TelegramAnswerConfig is the documentation bot. It answers in private
// chats and in the group chats listed in Chats, through this gateway's own
// /v1 with an API key the operator owns, so every answer is billed and
// logged like any other request.
type TelegramAnswerConfig struct {
	// Model and APIKey together turn answering on.
	Model  string `yaml:"model"`
	APIKey string `yaml:"api_key"`
	// BaseURL is the OpenAI-compatible base the bot calls. Empty calls this
	// process over loopback.
	BaseURL string `yaml:"base_url"`
	// Chats are the group chat ids where the bot answers when mentioned or
	// replied to. Groups not listed are ignored, so a stranger adding the
	// bot to a group cannot spend the key.
	Chats []string `yaml:"chats"`
	// KnowledgeURLs are the documents answers are grounded in. Empty uses
	// /llms.txt, /docs/llms.txt and /docs/llms-full.txt on the web origin.
	KnowledgeURLs []string `yaml:"knowledge_urls"`
	// DailyLimit caps answers per UTC day across all chats; 0 uses 300.
	DailyLimit int `yaml:"daily_limit"`
	// Instructions are the operator's own rules, added after the built-in
	// ones (what to say about partnerships, which topics to leave alone).
	// They cannot loosen the built-in rules.
	Instructions string `yaml:"instructions"`
}

// ProbeIdentityConfig is one client identity for probes. Metadata is a JSON
// object placed in the request's `metadata` when the probe carries none;
// {{device_id}} becomes a stable per-source id and {{session_id}} a fresh
// UUID for every probe.
type ProbeIdentityConfig struct {
	UserAgent string            `yaml:"user_agent"`
	Headers   map[string]string `yaml:"headers"`
	Metadata  string            `yaml:"metadata"`
}

type PaymentStripeConfig struct {
	SecretKey             string `yaml:"secret_key"`
	WebhookSecret         string `yaml:"webhook_secret"`
	PriceID               string `yaml:"price_id"`
	ServerAddress         string `yaml:"server_address"`
	ReturnBase            string `yaml:"return_base"`
	PromotionCodesEnabled bool   `yaml:"promotion_codes_enabled"`
}

var AppConfig AppConfigStruct = defaultConfig()

// EmailSwitchConfig turns one kind of mail on or off. Wording and layout are
// fixed in code (brand/foundations/emails.md).
type EmailSwitchConfig struct {
	Enable bool `yaml:"enabled"`
}

type LowBalanceEmailConfig struct {
	Enable       bool    `yaml:"enabled"`
	ThresholdUSD float64 `yaml:"threshold_usd"`
	RemindDays   []int   `yaml:"remind_days"`
}

func defaultConfig() AppConfigStruct {
	var c AppConfigStruct
	// Data directories
	c.Data.Dir = "./data"
	c.Data.LogsSubDir = "logs"
	c.Data.ExportSubDir = "export"
	// Model log
	c.ModelLogEnabled = false
	c.ModelLogFilename = "model-responses.jsonl"
	// Upload defaults
	c.Upload.Enable = false
	c.Upload.Schedule = "0 4 * * *" // 4am everyday
	c.Upload.Timezone = "Asia/Shanghai"
	c.Upload.DeleteAfterVerify = true
	c.Upload.VerifyViaPublic = true
	c.Upload.PublicBaseURL = ""
	c.Upload.Email.Enable = true
	c.Upload.Email.To = "admin@example.com"
	c.Upload.Email.From = "noreply@example.com"

	c.ArtifactStorage.Enable = false
	c.ArtifactStorage.BasePath = "artifacts"
	c.ArtifactStorage.PublicBaseURL = ""

	c.OSSURLCache.Enable = false
	c.OSSURLCache.RequestFilePath = "request_file"
	c.OSSURLCache.CacheDir = ""
	c.OSSURLCache.CacheTTLMinute = 0

	// SMTP defaults (placeholders; must be configured to actually send)
	c.SMTP.Server = ""
	c.SMTP.Port = 587
	c.SMTP.SSLEnabled = false
	c.SMTP.Account = ""
	c.SMTP.From = ""
	c.SMTP.FromName = ""
	c.SMTP.Token = ""

	// Email notifications defaults
	c.EmailNotifications.FromName = "Lazu"
	c.EmailNotifications.BatchSize = 200
	c.EmailNotifications.BalanceChange = EmailSwitchConfig{Enable: true}
	c.EmailNotifications.LowBalance = LowBalanceEmailConfig{
		Enable:       true,
		ThresholdUSD: 20,
		RemindDays:   []int{1, 3, 7, 15, 30},
	}
	c.EmailNotifications.AdminEmail = ""

	// Stripe defaults. Secrets are expected from env-expanded YAML or direct env.
	c.Payment.Stripe.ServerAddress = "http://localhost:3000"
	c.Payment.Stripe.PromotionCodesEnabled = false

	// Source checks: generic defaults. A deployment sets its own bars in
	// config.yaml (source_checks).
	c.SourceChecks = SourceChecksConfig{
		ObservationDays:      5,
		MinSamples:           20,
		MinSuccessRate:       0.95,
		MaxBillingRatio:      1.10,
		BillingFramingTokens: 256,
		LongContextTokens:    32_000,
		LongContextMinShare:  0.7,
		CheckEveryHours:      24,
	}

	return c
}

func applyAppConfigEnvironmentOverrides() {
	bucket := strings.TrimSpace(os.Getenv("R2_BUCKET"))
	accessKeyID := strings.TrimSpace(os.Getenv("R2_ACCESS_KEY_ID"))
	secretAccessKey := strings.TrimSpace(os.Getenv("R2_SECRET_ACCESS_KEY"))
	if bucket != "" && accessKeyID != "" && secretAccessKey != "" {
		AppConfig.ArtifactStorage.Enable = true
		AppConfig.ArtifactStorage.S3 = storage.S3Config{
			Endpoint:        strings.TrimSpace(os.Getenv("R2_ENDPOINT")),
			Region:          GetEnvOrDefaultString("R2_REGION", "auto"),
			Bucket:          bucket,
			AccessKeyID:     accessKeyID,
			SecretAccessKey: secretAccessKey,
		}
	}
	if apiKey := strings.TrimSpace(os.Getenv("RESEND_API_KEY")); apiKey != "" {
		AppConfig.Resend.APIKey = apiKey
	}
	if token := strings.TrimSpace(os.Getenv("TELEGRAM_BOT_TOKEN")); token != "" {
		AppConfig.Telegram.BotToken = token
	}
	if chat := strings.TrimSpace(os.Getenv("TELEGRAM_OPS_CHAT")); chat != "" {
		AppConfig.Telegram.OpsChat = chat
	}
	if v := strings.ToLower(strings.TrimSpace(os.Getenv("TELEGRAM_UPDATES"))); v != "" {
		AppConfig.Telegram.Updates = v
	}
	if v := strings.TrimSpace(os.Getenv("TELEGRAM_WEBHOOK_SECRET")); v != "" {
		AppConfig.Telegram.WebhookSecret = v
	}
	if v := strings.TrimSpace(os.Getenv("TELEGRAM_ANSWER_MODEL")); v != "" {
		AppConfig.Telegram.Answer.Model = v
	}
	if v := strings.TrimSpace(os.Getenv("TELEGRAM_ANSWER_API_KEY")); v != "" {
		AppConfig.Telegram.Answer.APIKey = v
	}
	if chats := nonEmptyTrimmed(strings.Split(os.Getenv("TELEGRAM_ANSWER_CHATS"), ",")); len(chats) > 0 {
		AppConfig.Telegram.Answer.Chats = chats
	}
	applyExtensionConfigEnvironment()
}

// LoadAppConfig loads config/backend/config.yaml if present, merging into defaults.
func LoadAppConfig() {
	defer runAppConfigLoadedHooks()
	defer applyAppConfigEnvironmentOverrides()
	// Allow override via env
	if p := os.Getenv("APP_CONFIG_FILE"); p != "" {
		if _, err := os.Stat(p); err == nil {
			loadConfigFile(p)
			return
		}
	}

	// Canonical config path is config/backend/config.yaml; Docker mounts to /app/config/backend/config.yaml.
	candidates := []string{
		filepath.Join("config", "backend", "config.yaml"),
		filepath.Join("config", "backend", "config.yml"),
		filepath.Join(string(os.PathSeparator), "app", "config", "backend", "config.yaml"),
		filepath.Join(string(os.PathSeparator), "app", "config", "backend", "config.yml"),
	}
	var found string
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			found = p
			break
		}
	}
	if found == "" {
		// ensure default directories exist
		_ = os.MkdirAll(GetLogsDir(), 0o755)
		_ = os.MkdirAll(GetExportDir(), 0o755)
		return
	}
	loadConfigFile(found)
}

func loadConfigFile(found string) {
	data, err := os.ReadFile(found)
	if err != nil {
		SysError("failed to read YAML config: " + err.Error())
		return
	}
	data = expandConfigEnvPlaceholders(data)
	type merged AppConfigStruct
	var userCfg merged
	if err := yaml.Unmarshal(data, &userCfg); err != nil {
		SysError("failed to parse YAML config: " + err.Error())
		return
	}
	var raw map[string]any
	_ = yaml.Unmarshal(data, &raw)
	// Merge into defaults (shallow)
	if userCfg.Data.Dir != "" {
		AppConfig.Data.Dir = userCfg.Data.Dir
	}
	if userCfg.Data.LogsSubDir != "" {
		AppConfig.Data.LogsSubDir = userCfg.Data.LogsSubDir
	}
	if userCfg.Data.ExportSubDir != "" {
		AppConfig.Data.ExportSubDir = userCfg.Data.ExportSubDir
	}
	if userCfg.Database.URL != "" {
		AppConfig.Database.URL = userCfg.Database.URL
	}
	if userCfg.Root.Username != "" {
		AppConfig.Root.Username = userCfg.Root.Username
	}
	if userCfg.Root.Password != "" {
		AppConfig.Root.Password = userCfg.Root.Password
	}
	if userCfg.Payment.Stripe.SecretKey != "" {
		AppConfig.Payment.Stripe.SecretKey = userCfg.Payment.Stripe.SecretKey
	}
	if userCfg.Payment.Stripe.WebhookSecret != "" {
		AppConfig.Payment.Stripe.WebhookSecret = userCfg.Payment.Stripe.WebhookSecret
	}
	if userCfg.Payment.Stripe.PriceID != "" {
		AppConfig.Payment.Stripe.PriceID = userCfg.Payment.Stripe.PriceID
	}
	if userCfg.Payment.Stripe.ServerAddress != "" {
		AppConfig.Payment.Stripe.ServerAddress = userCfg.Payment.Stripe.ServerAddress
	}
	if userCfg.Payment.Stripe.ReturnBase != "" {
		AppConfig.Payment.Stripe.ReturnBase = userCfg.Payment.Stripe.ReturnBase
	}
	if paymentSection, ok := yamlSection(raw, "payment"); ok {
		if stripeSection, ok := yamlSection(paymentSection, "stripe"); ok && yamlKeyExists(stripeSection, "promotion_codes_enabled") {
			AppConfig.Payment.Stripe.PromotionCodesEnabled = userCfg.Payment.Stripe.PromotionCodesEnabled
		}
	}
	if userCfg.ModelLogFilename != "" {
		AppConfig.ModelLogFilename = userCfg.ModelLogFilename
	}
	if keyExists(data, "model_log_enabled") {
		AppConfig.ModelLogEnabled = userCfg.ModelLogEnabled
	}

	// Upload cfg
	uploadSection, hasUploadSection := yamlSection(raw, "upload")
	if hasUploadSection && yamlKeyExists(uploadSection, "enable") {
		AppConfig.Upload.Enable = userCfg.Upload.Enable
	}
	if userCfg.Upload.Schedule != "" {
		AppConfig.Upload.Schedule = userCfg.Upload.Schedule
	}
	if userCfg.Upload.Timezone != "" {
		AppConfig.Upload.Timezone = userCfg.Upload.Timezone
	}
	if userCfg.Upload.PublicBaseURL != "" {
		AppConfig.Upload.PublicBaseURL = userCfg.Upload.PublicBaseURL
	}
	// boolean flags: if explicitly false in YAML, honor it
	if hasUploadSection && yamlKeyExists(uploadSection, "delete_after_verify") {
		AppConfig.Upload.DeleteAfterVerify = userCfg.Upload.DeleteAfterVerify
	}
	if hasUploadSection && yamlKeyExists(uploadSection, "verify_via_public_url") {
		AppConfig.Upload.VerifyViaPublic = userCfg.Upload.VerifyViaPublic
	}

	// Upload S3 config
	if userCfg.Upload.S3.Endpoint != "" {
		AppConfig.Upload.S3.Endpoint = userCfg.Upload.S3.Endpoint
	}
	if userCfg.Upload.S3.Region != "" {
		AppConfig.Upload.S3.Region = userCfg.Upload.S3.Region
	}
	if userCfg.Upload.S3.Bucket != "" {
		AppConfig.Upload.S3.Bucket = userCfg.Upload.S3.Bucket
	}
	if userCfg.Upload.S3.AccessKeyID != "" {
		AppConfig.Upload.S3.AccessKeyID = userCfg.Upload.S3.AccessKeyID
	}
	if userCfg.Upload.S3.SecretAccessKey != "" {
		AppConfig.Upload.S3.SecretAccessKey = userCfg.Upload.S3.SecretAccessKey
	}

	// Upload email
	if uploadEmailSection, ok := yamlSection(uploadSection, "email"); ok {
		if yamlKeyExists(uploadEmailSection, "to") && userCfg.Upload.Email.To != "" {
			AppConfig.Upload.Email.To = userCfg.Upload.Email.To
		}
		if yamlKeyExists(uploadEmailSection, "from") && userCfg.Upload.Email.From != "" {
			AppConfig.Upload.Email.From = userCfg.Upload.Email.From
		}
		if yamlKeyExists(uploadEmailSection, "enable") {
			AppConfig.Upload.Email.Enable = userCfg.Upload.Email.Enable
		}
	}

	if strings.Contains(string(data), "artifact_storage:") {
		AppConfig.ArtifactStorage.Enable = userCfg.ArtifactStorage.Enable
		if userCfg.ArtifactStorage.BasePath != "" {
			AppConfig.ArtifactStorage.BasePath = strings.Trim(userCfg.ArtifactStorage.BasePath, "/")
		}
		if userCfg.ArtifactStorage.PublicBaseURL != "" {
			AppConfig.ArtifactStorage.PublicBaseURL = strings.TrimRight(userCfg.ArtifactStorage.PublicBaseURL, "/")
		}
		if userCfg.ArtifactStorage.APIVideoTTLHours > 0 {
			AppConfig.ArtifactStorage.APIVideoTTLHours = userCfg.ArtifactStorage.APIVideoTTLHours
		}
		// ArtifactStorage S3 config
		if userCfg.ArtifactStorage.S3.Endpoint != "" {
			AppConfig.ArtifactStorage.S3.Endpoint = userCfg.ArtifactStorage.S3.Endpoint
		}
		if userCfg.ArtifactStorage.S3.Region != "" {
			AppConfig.ArtifactStorage.S3.Region = userCfg.ArtifactStorage.S3.Region
		}
		if userCfg.ArtifactStorage.S3.Bucket != "" {
			AppConfig.ArtifactStorage.S3.Bucket = userCfg.ArtifactStorage.S3.Bucket
		}
		if userCfg.ArtifactStorage.S3.AccessKeyID != "" {
			AppConfig.ArtifactStorage.S3.AccessKeyID = userCfg.ArtifactStorage.S3.AccessKeyID
		}
		if userCfg.ArtifactStorage.S3.SecretAccessKey != "" {
			AppConfig.ArtifactStorage.S3.SecretAccessKey = userCfg.ArtifactStorage.S3.SecretAccessKey
		}
	}

	applyExtensionConfigSections(raw)

	if section, ok := raw["oss_url_cache"].(map[string]any); ok {
		if _, ok := section["enable"]; ok {
			AppConfig.OSSURLCache.Enable = userCfg.OSSURLCache.Enable
		}
		if path, ok := section["request_file_path"].(string); ok && path != "" {
			AppConfig.OSSURLCache.RequestFilePath = strings.Trim(path, "/")
		}
		if dir, ok := section["cache_dir"].(string); ok && dir != "" {
			AppConfig.OSSURLCache.CacheDir = strings.Trim(dir, " ")
		}
		if ttl, ok := section["cache_ttl_minutes"].(int); ok && ttl > 0 {
			AppConfig.OSSURLCache.CacheTTLMinute = ttl
		} else if ttlFloat, ok := section["cache_ttl_minutes"].(float64); ok && ttlFloat > 0 {
			AppConfig.OSSURLCache.CacheTTLMinute = int(ttlFloat)
		}
		// OSSURLCache S3 config
		if userCfg.OSSURLCache.S3.Endpoint != "" {
			AppConfig.OSSURLCache.S3.Endpoint = userCfg.OSSURLCache.S3.Endpoint
		}
		if userCfg.OSSURLCache.S3.Region != "" {
			AppConfig.OSSURLCache.S3.Region = userCfg.OSSURLCache.S3.Region
		}
		if userCfg.OSSURLCache.S3.Bucket != "" {
			AppConfig.OSSURLCache.S3.Bucket = userCfg.OSSURLCache.S3.Bucket
		}
		if userCfg.OSSURLCache.S3.AccessKeyID != "" {
			AppConfig.OSSURLCache.S3.AccessKeyID = userCfg.OSSURLCache.S3.AccessKeyID
		}
		if userCfg.OSSURLCache.S3.SecretAccessKey != "" {
			AppConfig.OSSURLCache.S3.SecretAccessKey = userCfg.OSSURLCache.S3.SecretAccessKey
		}
	}

	// Resend cfg
	if userCfg.Resend.APIKey != "" {
		AppConfig.Resend.APIKey = userCfg.Resend.APIKey
	}
	if userCfg.Resend.From != "" {
		AppConfig.Resend.From = userCfg.Resend.From
	}

	// SMTP cfg (fallback when Resend not configured)
	if userCfg.SMTP.Server != "" {
		AppConfig.SMTP.Server = userCfg.SMTP.Server
	}
	if userCfg.SMTP.Port != 0 {
		AppConfig.SMTP.Port = userCfg.SMTP.Port
	}
	if keyExists(data, "smtp:") {
		AppConfig.SMTP.SSLEnabled = userCfg.SMTP.SSLEnabled
	}
	if userCfg.SMTP.Account != "" {
		AppConfig.SMTP.Account = userCfg.SMTP.Account
	}
	if userCfg.SMTP.From != "" {
		AppConfig.SMTP.From = userCfg.SMTP.From
	}
	if userCfg.SMTP.FromName != "" {
		AppConfig.SMTP.FromName = userCfg.SMTP.FromName
	}
	if userCfg.SMTP.Token != "" {
		AppConfig.SMTP.Token = userCfg.SMTP.Token
	}

	// Apply SMTP to global if set (env or DB options may override elsewhere)
	if AppConfig.SMTP.Server != "" {
		SMTPServer = AppConfig.SMTP.Server
	}
	if AppConfig.SMTP.Port != 0 {
		SMTPPort = AppConfig.SMTP.Port
	}
	SMTPSSLEnabled = AppConfig.SMTP.SSLEnabled
	if AppConfig.SMTP.Account != "" {
		SMTPAccount = AppConfig.SMTP.Account
	}
	if AppConfig.SMTP.From != "" {
		SMTPFrom = AppConfig.SMTP.From
	}
	// FromName is consumed directly in common/email.go from AppConfig.SMTP.FromName
	if AppConfig.SMTP.Token != "" {
		SMTPToken = AppConfig.SMTP.Token
	}

	// Email notifications
	if section, ok := raw["email_notifications"].(map[string]any); ok {
		if v, ok := section["from_name"].(string); ok && v != "" {
			AppConfig.EmailNotifications.FromName = v
		}
		if v, ok := section["batch_size"].(int); ok && v > 0 {
			AppConfig.EmailNotifications.BatchSize = v
		} else if vFloat, ok := section["batch_size"].(float64); ok && vFloat > 0 {
			AppConfig.EmailNotifications.BatchSize = int(vFloat)
		}
		if v, ok := section["admin_email"].(string); ok && v != "" {
			AppConfig.EmailNotifications.AdminEmail = v
		}
		if _, ok := section["balance_change"].(map[string]any); ok {
			AppConfig.EmailNotifications.BalanceChange.Enable = userCfg.EmailNotifications.BalanceChange.Enable
		}
		if lbSec, ok := section["low_balance"].(map[string]any); ok {
			AppConfig.EmailNotifications.LowBalance.Enable = userCfg.EmailNotifications.LowBalance.Enable
			if th, ok := lbSec["threshold_usd"].(float64); ok && th > 0 {
				AppConfig.EmailNotifications.LowBalance.ThresholdUSD = th
			} else if thInt, ok := lbSec["threshold_usd"].(int); ok && thInt > 0 {
				AppConfig.EmailNotifications.LowBalance.ThresholdUSD = float64(thInt)
			}
			if remindDays := NormalizePositiveIntSlice(parseDays(lbSec["remind_days"])); len(remindDays) > 0 {
				AppConfig.EmailNotifications.LowBalance.RemindDays = remindDays
			}
		}
	}

	if v := strings.TrimRight(strings.TrimSpace(userCfg.Site.WebOrigin), "/"); v != "" {
		AppConfig.Site.WebOrigin = v
	}
	if v := strings.TrimRight(strings.TrimSpace(userCfg.Site.APIOrigin), "/"); v != "" {
		AppConfig.Site.APIOrigin = v
	}
	if v := strings.TrimSpace(userCfg.Site.Operator); v != "" {
		AppConfig.Site.Operator = v
	}
	if v := strings.TrimSpace(userCfg.Site.SupportEmail); v != "" {
		AppConfig.Site.SupportEmail = v
	}
	if v := strings.TrimRight(strings.TrimSpace(userCfg.Site.SourceURL), "/"); v != "" {
		AppConfig.Site.SourceURL = v
	}
	if v := strings.TrimSpace(userCfg.Telegram.BotToken); v != "" {
		AppConfig.Telegram.BotToken = v
	}
	if v := strings.TrimSpace(userCfg.Telegram.OpsChat); v != "" {
		AppConfig.Telegram.OpsChat = v
	}
	if v := strings.TrimRight(strings.TrimSpace(userCfg.Telegram.APIBase), "/"); v != "" {
		AppConfig.Telegram.APIBase = v
	}
	if v := strings.ToLower(strings.TrimSpace(userCfg.Telegram.Updates)); v != "" {
		AppConfig.Telegram.Updates = v
	}
	if v := strings.TrimSpace(userCfg.Telegram.WebhookSecret); v != "" {
		AppConfig.Telegram.WebhookSecret = v
	}
	if v := strings.TrimSpace(userCfg.Telegram.WebhookURL); v != "" {
		AppConfig.Telegram.WebhookURL = v
	}
	{
		answer := userCfg.Telegram.Answer
		if v := strings.TrimSpace(answer.Model); v != "" {
			AppConfig.Telegram.Answer.Model = v
		}
		if v := strings.TrimSpace(answer.APIKey); v != "" {
			AppConfig.Telegram.Answer.APIKey = v
		}
		if v := strings.TrimRight(strings.TrimSpace(answer.BaseURL), "/"); v != "" {
			AppConfig.Telegram.Answer.BaseURL = v
		}
		if chats := nonEmptyTrimmed(answer.Chats); len(chats) > 0 {
			AppConfig.Telegram.Answer.Chats = chats
		}
		if urls := nonEmptyTrimmed(answer.KnowledgeURLs); len(urls) > 0 {
			AppConfig.Telegram.Answer.KnowledgeURLs = urls
		}
		if answer.DailyLimit > 0 {
			AppConfig.Telegram.Answer.DailyLimit = answer.DailyLimit
		}
		if v := strings.TrimSpace(answer.Instructions); v != "" {
			AppConfig.Telegram.Answer.Instructions = v
		}
	}
	if len(userCfg.ProbeIdentities) > 0 {
		AppConfig.ProbeIdentities = userCfg.ProbeIdentities
	}
	if section, ok := yamlSection(raw, "source_checks"); ok {
		applySourceChecksSection(section, &AppConfig.SourceChecks)
	}

	// ensure directories exist
	_ = os.MkdirAll(GetLogsDir(), 0o755)
	_ = os.MkdirAll(GetExportDir(), 0o755)
	_ = filepath.WalkDir(AppConfig.Data.Dir, func(path string, d fs.DirEntry, err error) error { return nil })
	if AppConfig.OSSURLCache.CacheDir != "" {
		cacheDir := GetOSSURLCacheDir()
		_ = os.MkdirAll(cacheDir, 0o755)
	}
}

// applySourceChecksSection merges the keys present in a source_checks
// section into cfg. A key whose value is not a number, or is outside the
// range that makes sense for it, is reported and leaves the default.
func applySourceChecksSection(section map[string]any, cfg *SourceChecksConfig) {
	intKey := func(key string, min, max int, target *int) {
		if !yamlKeyExists(section, key) {
			return
		}
		value, ok := yamlNumber(section[key])
		if !ok || value != math.Trunc(value) || value < float64(min) || value > float64(max) {
			SysError(fmt.Sprintf("config: source_checks.%s must be a whole number from %d to %d; keeping %d", key, min, max, *target))
			return
		}
		*target = int(value)
	}
	floatKey := func(key string, min, max float64, minInclusive bool, target *float64) {
		if !yamlKeyExists(section, key) {
			return
		}
		value, ok := yamlNumber(section[key])
		belowMin := value < min || (!minInclusive && value == min)
		if !ok || math.IsNaN(value) || belowMin || value > max {
			bound := "above"
			if minInclusive {
				bound = "at least"
			}
			SysError(fmt.Sprintf("config: source_checks.%s must be %s %g and at most %g; keeping %g", key, bound, min, max, *target))
			return
		}
		*target = value
	}
	intKey("observation_days", 1, 90, &cfg.ObservationDays)
	intKey("min_samples", 1, 100_000, &cfg.MinSamples)
	floatKey("min_success_rate", 0, 1, false, &cfg.MinSuccessRate)
	floatKey("max_billing_ratio", 1, 100, true, &cfg.MaxBillingRatio)
	intKey("billing_framing_tokens", 1, 1_000_000, &cfg.BillingFramingTokens)
	intKey("long_context_tokens", 1, 10_000_000, &cfg.LongContextTokens)
	floatKey("long_context_min_share", 0, 1, false, &cfg.LongContextMinShare)
	intKey("check_every_hours", 1, 24*90, &cfg.CheckEveryHours)
}

// yamlNumber reads a YAML scalar decoded into an untyped map as a number.
func yamlNumber(value any) (float64, bool) {
	switch v := value.(type) {
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case uint64:
		return float64(v), true
	case float64:
		return v, true
	default:
		return 0, false
	}
}

func GetDataDir() string {
	dir := AppConfig.Data.Dir
	if dir == "" {
		dir = "./data"
	}
	// 如果当前工作目录已经是 data（例如 Docker WORKDIR=/data），
	// 而配置仍然是相对的 ./data 或 data，则退化为当前目录，避免 /data/data 的情况。
	if !filepath.IsAbs(dir) && (dir == "data" || dir == "./data") {
		cwd, _ := os.Getwd()
		if filepath.Base(cwd) == "data" {
			return "."
		}
	}
	return dir
}

func GetLogsDir() string {
	dir := GetDataDir()
	sub := AppConfig.Data.LogsSubDir
	if sub == "" {
		sub = "logs"
	}
	return filepath.Join(dir, sub)
}

func GetExportDir() string {
	dir := GetDataDir()
	sub := AppConfig.Data.ExportSubDir
	if sub == "" {
		sub = "export"
	}
	return filepath.Join(dir, sub)
}

func GetOSSURLCacheDir() string {
	cacheDir := AppConfig.OSSURLCache.CacheDir
	if cacheDir == "" {
		cacheDir = "url-cache"
	}
	if filepath.IsAbs(cacheDir) {
		return cacheDir
	}
	return filepath.Join(GetDataDir(), cacheDir)
}

func expandConfigEnvPlaceholders(data []byte) []byte {
	return envPlaceholderPattern.ReplaceAllFunc(data, func(match []byte) []byte {
		parts := envPlaceholderPattern.FindSubmatch(match)
		if len(parts) != 2 {
			return match
		}
		return []byte(os.Getenv(string(parts[1])))
	})
}

// keyExists is a best-effort detector if a YAML key string appears in content.
func keyExists(data []byte, key string) bool {
	if len(data) == 0 || key == "" {
		return false
	}
	s := string(data)
	return strings.Contains(s, key+":") || strings.Contains(s, key+" :")
}

func yamlSection(raw map[string]any, key string) (map[string]any, bool) {
	if len(raw) == 0 || key == "" {
		return nil, false
	}
	value, ok := raw[key]
	if !ok {
		return nil, false
	}
	switch section := value.(type) {
	case map[string]any:
		return section, true
	case map[any]any:
		converted := make(map[string]any, len(section))
		for rawKey, rawValue := range section {
			stringKey, ok := rawKey.(string)
			if !ok {
				continue
			}
			converted[stringKey] = rawValue
		}
		return converted, true
	default:
		return nil, false
	}
}

func yamlKeyExists(section map[string]any, key string) bool {
	if len(section) == 0 || key == "" {
		return false
	}
	_, ok := section[key]
	return ok
}

func parseDays(raw any) []int {
	switch v := raw.(type) {
	case []int:
		return append([]int(nil), v...)
	case []any:
		days := make([]int, 0, len(v))
		for _, item := range v {
			switch val := item.(type) {
			case int:
				if val > 0 {
					days = append(days, val)
				}
			case float64:
				day := int(val)
				if day > 0 {
					days = append(days, day)
				}
			}
		}
		return days
	default:
		return nil
	}
}

// nonEmptyTrimmed trims every value and drops the empty ones.
func nonEmptyTrimmed(values []string) []string {
	var out []string
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

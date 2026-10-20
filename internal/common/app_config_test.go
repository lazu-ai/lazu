package common

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestLoadAppConfig_PathResolution(t *testing.T) {
	originalConfig := AppConfig
	originalWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get cwd: %v", err)
	}
	t.Cleanup(func() {
		AppConfig = originalConfig
		_ = os.Chdir(originalWd)
	})

	writeConfig := func(t *testing.T, path, dataDir string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("failed to create config dir: %v", err)
		}
		content := fmt.Sprintf("data:\n  dir: \"%s\"\n", dataDir)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("failed to write config %s: %v", path, err)
		}
	}

	resolvePath := func(root, p string) string {
		if filepath.IsAbs(p) {
			return p
		}
		return filepath.Join(root, p)
	}

	tests := []struct {
		name              string
		backendDataDir    string
		envDataDir        string
		useMissingEnvPath bool
		wantDataDir       string
	}{
		{
			name:           "app config file env has highest priority",
			backendDataDir: "./from-backend",
			envDataDir:     "./from-env",
			wantDataDir:    "./from-env",
		},
		{
			name:              "missing app config file env falls back to backend config",
			backendDataDir:    "./from-backend",
			useMissingEnvPath: true,
			wantDataDir:       "./from-backend",
		},
		{
			name:           "without app config file env uses backend config",
			backendDataDir: "./from-backend",
			wantDataDir:    "./from-backend",
		},
		{
			name:        "without any config uses defaults and creates runtime dirs",
			wantDataDir: "./data",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()

			previousWd, err := os.Getwd()
			if err != nil {
				t.Fatalf("failed to get cwd before chdir: %v", err)
			}
			if err := os.Chdir(root); err != nil {
				t.Fatalf("failed to chdir to temp root: %v", err)
			}
			defer func() {
				_ = os.Chdir(previousWd)
			}()

			AppConfig = defaultConfig()

			if tt.backendDataDir != "" {
				writeConfig(t, filepath.Join(root, "config", "backend", "config.yaml"), tt.backendDataDir)
			}

			switch {
			case tt.envDataDir != "":
				customConfigPath := filepath.Join(root, "custom", "app-config.yaml")
				writeConfig(t, customConfigPath, tt.envDataDir)
				t.Setenv("APP_CONFIG_FILE", customConfigPath)
			case tt.useMissingEnvPath:
				t.Setenv("APP_CONFIG_FILE", filepath.Join(root, "custom", "missing.yaml"))
			default:
				t.Setenv("APP_CONFIG_FILE", "")
			}

			LoadAppConfig()

			if AppConfig.Data.Dir != tt.wantDataDir {
				t.Fatalf("expected data dir %q, got %q", tt.wantDataDir, AppConfig.Data.Dir)
			}

			logsDir := resolvePath(root, GetLogsDir())
			exportDir := resolvePath(root, GetExportDir())

			logsStat, err := os.Stat(logsDir)
			if err != nil {
				t.Fatalf("expected logs dir %s to exist: %v", logsDir, err)
			}
			if !logsStat.IsDir() {
				t.Fatalf("expected logs path %s to be dir", logsDir)
			}

			exportStat, err := os.Stat(exportDir)
			if err != nil {
				t.Fatalf("expected export dir %s to exist: %v", exportDir, err)
			}
			if !exportStat.IsDir() {
				t.Fatalf("expected export path %s to be dir", exportDir)
			}
		})
	}
}

func TestLoadAppConfig_ExpandsEnvironmentVariables(t *testing.T) {
	originalConfig := AppConfig
	originalWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get cwd: %v", err)
	}
	t.Cleanup(func() {
		AppConfig = originalConfig
		_ = os.Chdir(originalWd)
	})

	root := t.TempDir()
	if err := os.Chdir(root); err != nil {
		t.Fatalf("failed to chdir to temp root: %v", err)
	}
	configPath := filepath.Join(root, "config", "backend", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}
	config := []byte(`data:
  dir: "${APP_DATA_DIR}"
upload:
  email:
    to: "${ADMIN_EMAIL}"
resend:
  api_key: "${RESEND_API_KEY}"
email_notifications:
  admin_email: "${ADMIN_EMAIL}"
payment:
  stripe:
    secret_key: "${STRIPE_SECRET_KEY}"
    webhook_secret: "literal$webhook"
    return_base: "https://console.example.test"
`)
	if err := os.WriteFile(configPath, config, 0o644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}
	t.Setenv("APP_CONFIG_FILE", "")
	t.Setenv("APP_DATA_DIR", "./from-env")
	t.Setenv("ADMIN_EMAIL", "admin@example.com")
	t.Setenv("RESEND_API_KEY", "dummy_resend_config_expansion")
	t.Setenv("STRIPE_SECRET_KEY", "dummy_stripe_config_expansion")

	AppConfig = defaultConfig()
	LoadAppConfig()

	if AppConfig.Data.Dir != "./from-env" {
		t.Fatalf("expected env-expanded data dir, got %q", AppConfig.Data.Dir)
	}
	if AppConfig.Resend.APIKey != "dummy_resend_config_expansion" {
		t.Fatalf("expected env-expanded resend key")
	}
	if AppConfig.Upload.Email.To != "admin@example.com" {
		t.Fatalf("expected env-expanded upload email recipient, got %q", AppConfig.Upload.Email.To)
	}
	if AppConfig.EmailNotifications.AdminEmail != "admin@example.com" {
		t.Fatalf("expected env-expanded admin email, got %q", AppConfig.EmailNotifications.AdminEmail)
	}
	if AppConfig.Payment.Stripe.SecretKey != "dummy_stripe_config_expansion" {
		t.Fatalf("expected env-expanded stripe secret")
	}
	if AppConfig.Payment.Stripe.WebhookSecret != "literal$webhook" {
		t.Fatalf("expected literal dollar sequence to be preserved, got %q", AppConfig.Payment.Stripe.WebhookSecret)
	}
	if AppConfig.Payment.Stripe.ReturnBase != "https://console.example.test" {
		t.Fatalf("expected browser checkout return base, got %q", AppConfig.Payment.Stripe.ReturnBase)
	}
}

func TestLoadAppConfig_AppliesCloudflareServiceEnvironment(t *testing.T) {
	originalConfig := AppConfig
	originalWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get cwd: %v", err)
	}
	t.Cleanup(func() {
		AppConfig = originalConfig
		_ = os.Chdir(originalWd)
	})

	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatalf("failed to chdir to temp root: %v", err)
	}
	t.Setenv("APP_CONFIG_FILE", "")
	t.Setenv("R2_ENDPOINT", "https://example.r2.cloudflarestorage.com")
	t.Setenv("R2_REGION", "auto")
	t.Setenv("R2_BUCKET", "test-files")
	t.Setenv("R2_ACCESS_KEY_ID", "test-access")
	t.Setenv("R2_SECRET_ACCESS_KEY", "test-secret")
	t.Setenv("RESEND_API_KEY", "test-resend")

	AppConfig = defaultConfig()
	LoadAppConfig()

	if !AppConfig.ArtifactStorage.Enable || !AppConfig.ArtifactStorage.S3.IsConfigured() {
		t.Fatal("expected complete R2 environment to enable artifact storage")
	}
	if AppConfig.ArtifactStorage.S3.Endpoint != "https://example.r2.cloudflarestorage.com" {
		t.Fatalf("unexpected R2 endpoint %q", AppConfig.ArtifactStorage.S3.Endpoint)
	}
	if AppConfig.Resend.APIKey != "test-resend" {
		t.Fatal("expected RESEND_API_KEY environment override")
	}
}

func TestLoadAppConfig_UploadEnableIsScopedToUploadSection(t *testing.T) {
	originalConfig := AppConfig
	originalWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get cwd: %v", err)
	}
	t.Cleanup(func() {
		AppConfig = originalConfig
		_ = os.Chdir(originalWd)
	})

	root := t.TempDir()
	if err := os.Chdir(root); err != nil {
		t.Fatalf("failed to chdir to temp root: %v", err)
	}
	configPath := filepath.Join(root, "config", "backend", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}
	config := []byte(`artifact_storage:
  enable: false
smtp:
  account: ops@example.com
  token: token
`)
	if err := os.WriteFile(configPath, config, 0o644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}
	t.Setenv("APP_CONFIG_FILE", "")

	AppConfig = defaultConfig()
	LoadAppConfig()

	if AppConfig.Upload.Enable {
		t.Fatalf("artifact_storage.enable should not enable upload when upload.enable is omitted")
	}
	if !AppConfig.Upload.Email.Enable {
		t.Fatalf("smtp settings should not disable upload.email when upload.email.enable is omitted")
	}

	config = []byte(`upload:
  enable: false
  email:
    enable: false
`)
	if err := os.WriteFile(configPath, config, 0o644); err != nil {
		t.Fatalf("failed to rewrite config: %v", err)
	}

	AppConfig = defaultConfig()
	LoadAppConfig()

	if AppConfig.Upload.Enable {
		t.Fatalf("explicit upload.enable=false should be honored")
	}
	if AppConfig.Upload.Email.Enable {
		t.Fatalf("explicit upload.email.enable=false should be honored")
	}
}

func TestExpandConfigEnvPlaceholdersOnlyExpandsBracedNames(t *testing.T) {
	t.Setenv("CONFIG_DIR", "./from-env")

	input := []byte(`data:
  dir: "${CONFIG_DIR}"
resend:
  api_key: "$CONFIG_DIR"
smtp:
  token: "pa$$word"
payment:
  stripe:
    secret_key: "${MISSING_CONFIG_SECRET}"
`)
	got := string(expandConfigEnvPlaceholders(input))

	if !strings.Contains(got, `dir: "./from-env"`) {
		t.Fatalf("expected braced placeholder expansion, got:\n%s", got)
	}
	if !strings.Contains(got, `api_key: "$CONFIG_DIR"`) {
		t.Fatalf("expected unbraced dollar sequence to remain literal, got:\n%s", got)
	}
	if !strings.Contains(got, `token: "pa$$word"`) {
		t.Fatalf("expected dollar-containing literal to remain unchanged, got:\n%s", got)
	}
	if !strings.Contains(got, `secret_key: ""`) {
		t.Fatalf("expected missing braced placeholder to expand to empty, got:\n%s", got)
	}
}

func TestParseDays(t *testing.T) {
	inputInts := []int{3, 0, -1, 7}
	gotInts := parseDays(inputInts)
	wantInts := []int{3, 0, -1, 7}
	if !reflect.DeepEqual(gotInts, wantInts) {
		t.Fatalf("unexpected []int parse result: got=%v want=%v", gotInts, wantInts)
	}
	inputInts[0] = 99
	if gotInts[0] != 3 {
		t.Fatalf("expected []int parse to return a copy, got=%v", gotInts)
	}

	gotMixed := parseDays([]any{float64(2), int(5), float64(0), int(-3), "x", float64(4.9)})
	wantMixed := []int{2, 5, 4}
	if !reflect.DeepEqual(gotMixed, wantMixed) {
		t.Fatalf("unexpected []any parse result: got=%v want=%v", gotMixed, wantMixed)
	}

	if got := parseDays(nil); got != nil {
		t.Fatalf("expected nil for nil input, got=%v", got)
	}
	if got := parseDays("not-a-slice"); got != nil {
		t.Fatalf("expected nil for unsupported input, got=%v", got)
	}
}

func TestLoadAppConfig_SourceChecks(t *testing.T) {
	originalConfig := AppConfig
	originalWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get cwd: %v", err)
	}
	t.Cleanup(func() {
		AppConfig = originalConfig
		_ = os.Chdir(originalWd)
	})

	root := t.TempDir()
	if err := os.Chdir(root); err != nil {
		t.Fatalf("failed to chdir to temp root: %v", err)
	}
	configPath := filepath.Join(root, "config.yaml")
	load := func(t *testing.T, content string) SourceChecksConfig {
		t.Helper()
		if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
			t.Fatalf("failed to write config: %v", err)
		}
		AppConfig = defaultConfig()
		loadConfigFile(configPath)
		return AppConfig.SourceChecks
	}
	defaults := defaultConfig().SourceChecks

	t.Run("without the section keeps defaults", func(t *testing.T) {
		if got := load(t, "data:\n  dir: ./data\n"); got != defaults {
			t.Fatalf("source checks = %+v, want defaults %+v", got, defaults)
		}
	})

	t.Run("every key loads", func(t *testing.T) {
		got := load(t, `source_checks:
  observation_days: 9
  min_samples: 30
  min_success_rate: 0.9
  max_billing_ratio: 1.2
  billing_framing_tokens: 64
  long_context_tokens: 50000
  long_context_min_share: 0.5
  check_every_hours: 12
`)
		want := SourceChecksConfig{
			ObservationDays: 9, MinSamples: 30, MinSuccessRate: 0.9, MaxBillingRatio: 1.2,
			BillingFramingTokens: 64, LongContextTokens: 50_000, LongContextMinShare: 0.5, CheckEveryHours: 12,
		}
		if got != want {
			t.Fatalf("source checks = %+v, want %+v", got, want)
		}
		if got.Observation() != 9*24*time.Hour || got.CheckEvery() != 12*time.Hour {
			t.Fatalf("durations = %v / %v", got.Observation(), got.CheckEvery())
		}
	})

	t.Run("missing keys keep defaults", func(t *testing.T) {
		got := load(t, "source_checks:\n  min_samples: 40\n  max_billing_ratio: 1\n")
		want := defaults
		want.MinSamples = 40
		want.MaxBillingRatio = 1
		if got != want {
			t.Fatalf("source checks = %+v, want %+v", got, want)
		}
	})

	t.Run("invalid values fall back to defaults", func(t *testing.T) {
		got := load(t, `source_checks:
  observation_days: 0
  min_samples: -3
  min_success_rate: 1.5
  max_billing_ratio: 0.8
  billing_framing_tokens: 12.5
  long_context_tokens: lots
  long_context_min_share: 0
  check_every_hours: -1
`)
		if got != defaults {
			t.Fatalf("source checks = %+v, want defaults %+v", got, defaults)
		}
	})
}

func TestLoadAppConfig_ReadsAPIVideoTTL(t *testing.T) {
	originalConfig := AppConfig
	originalWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get cwd: %v", err)
	}
	t.Cleanup(func() {
		AppConfig = originalConfig
		_ = os.Chdir(originalWd)
	})
	root := t.TempDir()
	if err := os.Chdir(root); err != nil {
		t.Fatalf("failed to chdir to temp root: %v", err)
	}
	configPath := filepath.Join(root, "config", "backend", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}
	t.Setenv("APP_CONFIG_FILE", "")
	for _, tc := range []struct {
		yaml string
		want int
	}{
		{"artifact_storage:\n  enable: true\n  api_video_ttl_hours: 72\n", 72},
		{"artifact_storage:\n  enable: true\n", 0},
	} {
		if err := os.WriteFile(configPath, []byte(tc.yaml), 0o644); err != nil {
			t.Fatalf("failed to write config: %v", err)
		}
		AppConfig = defaultConfig()
		LoadAppConfig()
		if got := AppConfig.ArtifactStorage.APIVideoTTLHours; got != tc.want {
			t.Fatalf("api_video_ttl_hours=%d want %d for %q", got, tc.want, tc.yaml)
		}
	}
}

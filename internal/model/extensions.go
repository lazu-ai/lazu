package model

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/lazu-ai/lazu/pkg/billing"
	"gorm.io/gorm"
)

// Extensions are features a build compiles in that this repository does not
// carry. They register from init() in their own files of this package; a
// build without them leaves every hook empty and behaves exactly as the core.
// Hooks run in registration order and the first error stops the chain.

// MediaJobSucceededHook runs inside the transaction that marks a media job
// succeeded, after the job row is updated.
type MediaJobSucceededHook func(tx *gorm.DB, jobID uint, input CompleteMediaJobInput, artifactFileID string, artifactURL string) error

// MediaJobFailedHook runs inside the transaction that marks a media job
// failed, after the job row is updated.
type MediaJobFailedHook func(tx *gorm.DB, jobID uint, code string, message string) error

// MediaJobChargedHook runs once a media job's charge is settled.
type MediaJobChargedHook func(job MediaJob, charge billing.MicroUSD)

// TopUpCompletingHook runs inside the transaction that completes a top-up,
// before the credit is applied, and may add to it (for example a bonus).
type TopUpCompletingHook func(tx *gorm.DB, topUp *TopUp) error

// TopUpReversedHook runs inside the transaction that reverses a top-up.
type TopUpReversedHook func(tx *gorm.DB, topUpID int) error

// UserDeletingHook runs inside the transaction that deletes a user's data.
type UserDeletingHook func(tx *gorm.DB, userID int) error

// FileDeletingHook runs inside the transaction that deletes a stored file.
type FileDeletingHook func(tx *gorm.DB, file *File) error

// SignupVisitsCounter adds visits by acquisition channel to the overview.
type SignupVisitsCounter func(ctx context.Context, start int64, end int64) ([]OverviewSignupSource, error)

// HiddenTokenKind names credentials an extension creates for itself. They
// bill like any key but never appear in a customer's key list or counts.
type HiddenTokenKind struct {
	Product string
	Name    string
}

var extensions struct {
	coreModels             []any
	telemetryModels        []any
	migrations             []func(db *gorm.DB) error
	optionDefaults         []func(options map[string]string)
	mediaJobSucceeded      []MediaJobSucceededHook
	mediaJobFailed         []MediaJobFailedHook
	mediaJobCharged        []MediaJobChargedHook
	topUpCompleting        []TopUpCompletingHook
	topUpReversed          []TopUpReversedHook
	userDeleting           []UserDeletingHook
	fileDeleting           []FileDeletingHook
	signupVisits           SignupVisitsCounter
	retention              []retentionPurge
	hiddenTokens           []HiddenTokenKind
	operatedAccountRemarks []string
}

// RegisterCoreModels adds tables to the core store's AutoMigrate.
func RegisterCoreModels(models ...any) {
	extensions.coreModels = append(extensions.coreModels, models...)
}

// RegisterTelemetryModels adds tables to the telemetry store. table names
// the same tables for export and import.
func RegisterTelemetryModels(tables []string, models ...any) {
	TelemetryTables = append(TelemetryTables, tables...)
	extensions.telemetryModels = append(extensions.telemetryModels, models...)
}

// RegisterMigration adds an idempotent schema step. Steps run after the core
// steps and before the rollback floor is recorded.
func RegisterMigration(step func(db *gorm.DB) error) {
	extensions.migrations = append(extensions.migrations, step)
}

// RegisterOptionDefaults adds option defaults before stored options load.
func RegisterOptionDefaults(apply func(options map[string]string)) {
	extensions.optionDefaults = append(extensions.optionDefaults, apply)
}

func RegisterMediaJobSucceededHook(hook MediaJobSucceededHook) {
	extensions.mediaJobSucceeded = append(extensions.mediaJobSucceeded, hook)
}

func RegisterMediaJobFailedHook(hook MediaJobFailedHook) {
	extensions.mediaJobFailed = append(extensions.mediaJobFailed, hook)
}

func RegisterMediaJobChargedHook(hook MediaJobChargedHook) {
	extensions.mediaJobCharged = append(extensions.mediaJobCharged, hook)
}

func RegisterTopUpCompletingHook(hook TopUpCompletingHook) {
	extensions.topUpCompleting = append(extensions.topUpCompleting, hook)
}

func RegisterTopUpReversedHook(hook TopUpReversedHook) {
	extensions.topUpReversed = append(extensions.topUpReversed, hook)
}

func RegisterUserDeletingHook(hook UserDeletingHook) {
	extensions.userDeleting = append(extensions.userDeleting, hook)
}

func RegisterFileDeletingHook(hook FileDeletingHook) {
	extensions.fileDeleting = append(extensions.fileDeleting, hook)
}

// RegisterSignupVisitsCounter sets the source of visit counts for the
// overview's acquisition channels. Without one, channels show sign-ups only.
func RegisterSignupVisitsCounter(counter SignupVisitsCounter) {
	extensions.signupVisits = counter
}

// RegisterHiddenTokenKind hides an extension's own credentials.
func RegisterHiddenTokenKind(kind HiddenTokenKind) {
	kind.Product = strings.TrimSpace(kind.Product)
	kind.Name = strings.TrimSpace(kind.Name)
	if kind.Product == "" || kind.Name == "" {
		return
	}
	extensions.hiddenTokens = append(extensions.hiddenTokens, kind)
}

// RegisterOperatedAccountRemarkPrefix marks accounts the operator runs
// itself (showcase or seed accounts) by their remark; the overview leaves
// them out of customer counts.
func RegisterOperatedAccountRemarkPrefix(prefix string) {
	if prefix = strings.TrimSpace(prefix); prefix != "" {
		extensions.operatedAccountRemarks = append(extensions.operatedAccountRemarks, prefix)
	}
}

func runExtensionMigrations(db *gorm.DB) error {
	for _, step := range extensions.migrations {
		if err := step(db); err != nil {
			return err
		}
	}
	return nil
}

func applyExtensionOptionDefaults(options map[string]string) {
	for _, apply := range extensions.optionDefaults {
		apply(options)
	}
}

func runMediaJobSucceededHooks(tx *gorm.DB, jobID uint, input CompleteMediaJobInput, artifactFileID string, artifactURL string) error {
	for _, hook := range extensions.mediaJobSucceeded {
		if err := hook(tx, jobID, input, artifactFileID, artifactURL); err != nil {
			return err
		}
	}
	return nil
}

func runMediaJobFailedHooks(tx *gorm.DB, jobID uint, code string, message string) error {
	for _, hook := range extensions.mediaJobFailed {
		if err := hook(tx, jobID, code, message); err != nil {
			return err
		}
	}
	return nil
}

func runMediaJobChargedHooks(job MediaJob, charge billing.MicroUSD) {
	for _, hook := range extensions.mediaJobCharged {
		hook(job, charge)
	}
}

func runTopUpCompletingHooks(tx *gorm.DB, topUp *TopUp) error {
	for _, hook := range extensions.topUpCompleting {
		if err := hook(tx, topUp); err != nil {
			return err
		}
	}
	return nil
}

func runTopUpReversedHooks(tx *gorm.DB, topUpID int) error {
	for _, hook := range extensions.topUpReversed {
		if err := hook(tx, topUpID); err != nil {
			return err
		}
	}
	return nil
}

func runUserDeletingHooks(tx *gorm.DB, userID int) error {
	for _, hook := range extensions.userDeleting {
		if err := hook(tx, userID); err != nil {
			return err
		}
	}
	return nil
}

func runFileDeletingHooks(tx *gorm.DB, file *File) error {
	for _, hook := range extensions.fileDeleting {
		if err := hook(tx, file); err != nil {
			return err
		}
	}
	return nil
}

// hiddenTokenCondition is the WHERE fragment that matches every hidden
// credential: an internal scope, or a registered product and name. Values
// are lower-cased for the LOWER(...) comparison.
func hiddenTokenCondition() (string, []any) {
	clauses := []string{"TRIM(COALESCE(internal_scope, '')) <> ''"}
	args := []any{}
	for _, kind := range extensions.hiddenTokens {
		clauses = append(clauses, "(LOWER(COALESCE(product, '')) = ? AND LOWER(COALESCE(name, '')) = ?)")
		args = append(args, strings.ToLower(kind.Product), strings.ToLower(kind.Name))
	}
	return strings.Join(clauses, " OR "), args
}

func isHiddenTokenKind(product string, name string) bool {
	product = strings.TrimSpace(product)
	name = strings.TrimSpace(name)
	for _, kind := range extensions.hiddenTokens {
		if strings.EqualFold(product, kind.Product) && strings.EqualFold(name, kind.Name) {
			return true
		}
	}
	return false
}

// excludeOperatedAccounts leaves out accounts registered as operated.
func excludeOperatedAccounts(query *gorm.DB) *gorm.DB {
	for _, prefix := range extensions.operatedAccountRemarks {
		query = query.Where("COALESCE(remark, '') NOT LIKE ?", prefix+"%")
	}
	return query
}

// operatedAccountIDs lists accounts registered as operated.
func operatedAccountIDs(db *gorm.DB) ([]int, error) {
	ids := []int{}
	for _, prefix := range extensions.operatedAccountRemarks {
		var batch []int
		if err := db.Model(&User{}).Where("COALESCE(remark, '') LIKE ?", prefix+"%").Pluck("id", &batch).Error; err != nil {
			return nil, err
		}
		ids = append(ids, batch...)
	}
	return ids, nil
}

type retentionPurge struct {
	name  string
	purge func(now time.Time) error
}

// RegisterRetentionPurge adds a cleanup to the daily retention run. purge
// receives the run time and applies its own window.
func RegisterRetentionPurge(name string, purge func(now time.Time) error) {
	extensions.retention = append(extensions.retention, retentionPurge{name: name, purge: purge})
}

// RunExtensionRetention runs every registered cleanup and returns the
// failures; one failure does not stop the others.
func RunExtensionRetention(now time.Time) []error {
	var failures []error
	for _, item := range extensions.retention {
		if err := item.purge(now); err != nil {
			failures = append(failures, fmt.Errorf("failed to purge %s: %w", item.name, err))
		}
	}
	return failures
}

var (
	mediaJobSources       = map[string]bool{}
	mediaJobArtifactRetry []func(job MediaJob) bool
)

// RegisterMediaJobSource lets an extension submit media jobs under its own
// source; anything unregistered is the public media API.
func RegisterMediaJobSource(source string) {
	if source = strings.ToLower(strings.TrimSpace(source)); source != "" && source != MediaJobSourceAPI {
		mediaJobSources[source] = true
	}
}

// IsExtensionMediaJobSource reports whether source is a registered extension.
func IsExtensionMediaJobSource(source string) bool {
	return mediaJobSources[strings.ToLower(strings.TrimSpace(source))]
}

// RegisterMediaJobArtifactRetry marks jobs whose artifact download is
// retried after a transient failure instead of failing the job.
func RegisterMediaJobArtifactRetry(retry func(job MediaJob) bool) {
	mediaJobArtifactRetry = append(mediaJobArtifactRetry, retry)
}

// MediaJobRetriesArtifact reports whether a registered rule retries job's
// artifact download.
func MediaJobRetriesArtifact(job MediaJob) bool {
	for _, retry := range mediaJobArtifactRetry {
		if retry(job) {
			return true
		}
	}
	return false
}

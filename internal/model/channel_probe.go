package model

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ChannelProbeResult is one synthetic check of a single (lane, model,
// channel) cell.
//
// Probes live in their own table rather than in routing_outcome on purpose.
// routing_outcome feeds billing, usage reporting and customer spend; mixing
// synthetic calls into it would mean auditing every existing consumer for an
// is_probe exclusion, and a single missed spot would corrupt a customer-visible
// number. The health layer unions the two tables explicitly instead.
//
// A probe is a small streamed request, so besides "did it answer" it measures
// time to first token, the longest pause between chunks, and decode speed.
// Those three do not depend on how long the reply is, which is what lets the
// health page treat a probe as one more sample of the lane rather than as a
// separate kind of evidence. Total duration does depend on reply length, so
// the page prefers real traffic for it; cache hits and truncation cannot be
// observed on a fixed prompt at all and come from traffic only.
type ChannelProbeResult struct {
	RequestTS int64  `json:"-" gorm:"primaryKey;autoIncrement:false"`
	Id        int64  `json:"id" gorm:"primaryKey"`
	Lane      string `json:"lane" gorm:"type:varchar(64);index:idx_channel_probe_cell,priority:1"`
	ModelName string `json:"model_name" gorm:"type:varchar(255);index:idx_channel_probe_cell,priority:2"`
	ChannelId int    `json:"channel_id" gorm:"index:idx_channel_probe_cell,priority:3"`
	// ProbedAt drives both the recency check in the scheduler and the bucket
	// placement in the health heatmap, so it carries its own index.
	ProbedAt  time.Time `json:"probed_at" gorm:"index:idx_channel_probe_probed_at"`
	Success   bool      `json:"success"`
	LatencyMs int       `json:"latency_ms"`
	// TTFTMs, MaxStallMs and OutputTokens are only set by a streamed probe
	// that produced output. A non-streamed probe (embeddings, rerank) leaves
	// them at zero, which the health queries read as "not observed".
	TTFTMs       int `json:"ttft_ms"`
	MaxStallMs   int `json:"max_stall_ms"`
	OutputTokens int `json:"output_tokens"`
	// AnswerTTFTMs and ReasoningTokens bound the decode-speed sample to the
	// answer: reasoning is inside OutputTokens and, when not streamed, all of
	// it happens before AnswerTTFTMs.
	AnswerTTFTMs    int    `json:"answer_ttft_ms"`
	ReasoningTokens int    `json:"reasoning_tokens"`
	ErrorMessage    string `json:"error_message,omitempty" gorm:"type:varchar(512)"`
	// CostUSD is the estimated upstream spend of this single probe. The
	// scheduler sums it per UTC day to enforce the probe budget.
	CostUSD float64 `json:"cost_usd" gorm:"type:decimal(12,8)"`
	// Skipped marks an attempt that is not valid provider-health evidence: a
	// local pre-provider error, a model with no probeable endpoint, or an
	// in-flight request interrupted by scheduler shutdown. It is kept for the
	// scheduler's backoff but excluded from health aggregates. A canceled
	// in-flight request may retain CostUSD because upstream spend is uncertain.
	Skipped bool `json:"skipped" gorm:"index"`
	// Modality is what the probed route produces — text, embedding, image,
	// rerank. It decides whether the probe's total duration is a latency
	// sample of the lane: for text it grows with the capped reply and is kept
	// apart, for everything else the request has one shape whoever sends it.
	Modality string `json:"modality,omitempty" gorm:"type:varchar(16)"`
}

func (ChannelProbeResult) TableName() string {
	return "channel_probe_result"
}

// ChannelProbeBudget is the UTC-day reservation ledger for synthetic provider
// calls. Keeping the ceiling in one database row makes it process-independent:
// API and worker replicas all compete on the same atomic UPDATE.
type ChannelProbeBudget struct {
	Day         string    `json:"day" gorm:"primaryKey;type:varchar(10)"`
	ReservedUSD float64   `json:"reserved_usd" gorm:"type:decimal(12,8);not null;default:0"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func (ChannelProbeBudget) TableName() string {
	return "channel_probe_budget"
}

// ChannelProbeClaim is a short database lease for one model/lane/channel cell.
// Candidate selection is intentionally read-only, so replicas can discover the
// same stale cell; this lease ensures only one of them contacts the provider.
type ChannelProbeClaim struct {
	CellKey      string    `json:"cell_key" gorm:"primaryKey;type:varchar(512)"`
	ClaimToken   string    `json:"claim_token" gorm:"type:varchar(64);index"`
	ClaimedUntil time.Time `json:"claimed_until" gorm:"index"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func (ChannelProbeClaim) TableName() string {
	return "channel_probe_claim"
}

// ClaimChannelProbe atomically leases one due cell across every API/worker
// replica. The seed-then-conditional-update sequence works on all supported
// databases and lets exactly one concurrent caller replace the expired lease.
func ClaimChannelProbe(cellKey string, claimToken string, now time.Time, leaseUntil time.Time) (bool, error) {
	return ClaimChannelProbeContext(context.Background(), cellKey, claimToken, now, leaseUntil)
}

func ClaimChannelProbeContext(ctx context.Context, cellKey string, claimToken string, now time.Time, leaseUntil time.Time) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	db := CoreDB()
	if db != nil {
		db = db.WithContext(ctx)
	}
	if db == nil {
		return true, nil
	}
	cellKey = strings.TrimSpace(cellKey)
	claimToken = strings.TrimSpace(claimToken)
	if cellKey == "" || claimToken == "" {
		return false, nil
	}
	now = now.UTC()
	leaseUntil = leaseUntil.UTC()
	seed := ChannelProbeClaim{
		CellKey: cellKey, ClaimedUntil: now.Add(-time.Nanosecond), UpdatedAt: now,
	}
	if err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&seed).Error; err != nil {
		return false, err
	}
	result := db.Model(&ChannelProbeClaim{}).
		Where("cell_key = ? AND claimed_until <= ?", cellKey, now).
		Updates(map[string]any{
			"claim_token": claimToken, "claimed_until": leaseUntil, "updated_at": now,
		})
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected == 1, nil
}

func ReleaseChannelProbeClaim(cellKey string, claimToken string) error {
	return ReleaseChannelProbeClaimContext(context.Background(), cellKey, claimToken)
}

func ReleaseChannelProbeClaimContext(ctx context.Context, cellKey string, claimToken string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	db := CoreDB()
	if db != nil {
		db = db.WithContext(ctx)
	}
	if db == nil {
		return nil
	}
	return db.Where("cell_key = ? AND claim_token = ?", strings.TrimSpace(cellKey), strings.TrimSpace(claimToken)).
		Delete(&ChannelProbeClaim{}).Error
}

// RecordChannelProbe stores one probe outcome. Probe recording must never fail
// a caller: a lost probe row only costs freshness on one dashboard cell.
func RecordChannelProbe(result ChannelProbeResult) error {
	if TelemetryDB() == nil {
		return nil
	}
	result.Lane = NormalizeChannelLane(result.Lane)
	result.ModelName = strings.TrimSpace(result.ModelName)
	if result.ModelName == "" || result.ChannelId <= 0 {
		return nil
	}
	if result.ProbedAt.IsZero() {
		result.ProbedAt = time.Now().UTC()
	}
	if len(result.ErrorMessage) > 512 {
		result.ErrorMessage = result.ErrorMessage[:512]
		for len(result.ErrorMessage) > 0 && !utf8.ValidString(result.ErrorMessage) {
			result.ErrorMessage = result.ErrorMessage[:len(result.ErrorMessage)-1]
		}
	}
	result.RequestTS = result.ProbedAt.UnixMilli()
	return TelemetryDB().Create(&result).Error
}

// ChannelProbeSpendSince totals probe cost from the given instant. The
// scheduler passes the start of the current UTC day to enforce a daily cap.
func ChannelProbeSpendSince(since time.Time) (float64, error) {
	if TelemetryDB() == nil || !TelemetryDB().Migrator().HasTable(&ChannelProbeResult{}) {
		return 0, nil
	}
	var total *float64
	if err := TelemetryDB().Model(&ChannelProbeResult{}).
		Where("probed_at >= ?", since).
		Select("COALESCE(SUM(cost_usd), 0)").
		Scan(&total).Error; err != nil {
		return 0, err
	}
	if total == nil {
		return 0, nil
	}
	return *total, nil
}

// ReserveChannelProbeBudget atomically claims estimated upstream spend before
// the provider request starts. A crash can leave conservative unused headroom,
// but concurrent replicas can never spend past the configured daily ceiling.
func ReserveChannelProbeBudget(dayStart time.Time, budget float64, amount float64) (bool, error) {
	if budget < 0 || CoreDB() == nil {
		return true, nil
	}
	if amount < 0 {
		amount = 0
	}
	dayStart = dayStart.UTC()
	day := dayStart.Format("2006-01-02")
	legacySpent, err := ChannelProbeSpendSince(dayStart)
	if err != nil {
		return false, err
	}
	seed := ChannelProbeBudget{Day: day, ReservedUSD: legacySpent, UpdatedAt: time.Now().UTC()}
	if err := CoreDB().Clauses(clause.OnConflict{DoNothing: true}).Create(&seed).Error; err != nil {
		return false, err
	}
	result := CoreDB().Model(&ChannelProbeBudget{}).
		Where("day = ? AND reserved_usd + ? <= ?", day, amount, budget).
		Updates(map[string]any{
			"reserved_usd": gorm.Expr("reserved_usd + ?", amount),
			"updated_at":   time.Now().UTC(),
		})
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected == 1, nil
}

// ReleaseChannelProbeBudget returns a reservation when request construction or
// another local guard exits before contacting the provider.
func ReleaseChannelProbeBudget(dayStart time.Time, amount float64) error {
	if CoreDB() == nil || amount <= 0 {
		return nil
	}
	day := dayStart.UTC().Format("2006-01-02")
	return CoreDB().Model(&ChannelProbeBudget{}).
		Where("day = ?", day).
		Updates(map[string]any{
			"reserved_usd": gorm.Expr(
				"CASE WHEN reserved_usd >= ? THEN reserved_usd - ? ELSE 0 END",
				amount, amount,
			),
			"updated_at": time.Now().UTC(),
		}).Error
}

// ChannelProbeLastAttempt is the newest probe timestamp per cell, used by the
// scheduler to decide what is due without loading probe history.
type ChannelProbeLastAttempt struct {
	Lane      string
	ModelName string
	ChannelId int
	// ProbedAt is an aggregate, which SQLite hands back as text.
	ProbedAt ChannelHealthTime
}

// LoadChannelProbeLastAttempts returns the most recent probe per cell within
// the lookback window, keyed by lane/model/channel.
func LoadChannelProbeLastAttempts(since time.Time) (map[string]time.Time, error) {
	return LoadChannelProbeLastAttemptsContext(context.Background(), since)
}

func LoadChannelProbeLastAttemptsContext(ctx context.Context, since time.Time) (map[string]time.Time, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	db := TelemetryDB()
	if db != nil {
		db = db.WithContext(ctx)
	}
	result := map[string]time.Time{}
	if db == nil || !db.Migrator().HasTable(&ChannelProbeResult{}) {
		return result, ctx.Err()
	}
	var rows []ChannelProbeLastAttempt
	if err := db.Model(&ChannelProbeResult{}).
		Select("lane, model_name, channel_id, MAX(probed_at) AS probed_at").
		Where("probed_at >= ?", since).
		Group("lane, model_name, channel_id").
		Scan(&rows).Error; err != nil {
		return result, err
	}
	for _, row := range rows {
		if !row.ProbedAt.Valid {
			continue
		}
		result[ChannelHealthCellKey(row.ModelName, row.Lane, row.ChannelId)] = row.ProbedAt.Time
	}
	return result, ctx.Err()
}

// LoadChannelProbeLastAttempt returns the newest attempt on one route since
// the given time, or false when there is none.
//
// The scheduler re-reads it after winning a cell's claim. Candidate lists are
// computed before any probe runs, so a second replica sweeping from the same
// snapshot would otherwise claim the cell the moment the first released it and
// probe the route again seconds later — which is what doubled a third of all
// probes when both the API and the worker process ran the sweep.
func LoadChannelProbeLastAttempt(modelName string, lane string, channelID int, since time.Time) (time.Time, bool, error) {
	if TelemetryDB() == nil || !TelemetryDB().Migrator().HasTable(&ChannelProbeResult{}) {
		return time.Time{}, false, nil
	}
	var row ChannelProbeLastAttempt
	if err := TelemetryDB().Model(&ChannelProbeResult{}).
		Select("lane, model_name, channel_id, MAX(probed_at) AS probed_at").
		Where("model_name = ? AND lane = ? AND channel_id = ? AND probed_at >= ?",
			modelName, NormalizeChannelLane(lane), channelID, since).
		Group("lane, model_name, channel_id").
		Scan(&row).Error; err != nil {
		return time.Time{}, false, err
	}
	if !row.ProbedAt.Valid {
		return time.Time{}, false, nil
	}
	return row.ProbedAt.Time, true, nil
}

// PurgeOldChannelProbeResults trims probe history. Probes only ever back a
// short rolling window on the dashboard, so they are kept far shorter than
// routing outcomes.
func PurgeOldChannelProbeResults(before time.Time) error {
	if TelemetryDB() == nil || !TelemetryDB().Migrator().HasTable(&ChannelProbeResult{}) {
		return nil
	}
	if err := TelemetryDB().Where("probed_at < ?", before).Delete(&ChannelProbeResult{}).Error; err != nil {
		return err
	}
	if CoreDB().Migrator().HasTable(&ChannelProbeBudget{}) {
		if err := CoreDB().Where("day < ?", before.UTC().Format("2006-01-02")).
			Delete(&ChannelProbeBudget{}).Error; err != nil {
			return err
		}
	}
	if CoreDB().Migrator().HasTable(&ChannelProbeClaim{}) {
		return CoreDB().Where("claimed_until < ?", before.UTC()).Delete(&ChannelProbeClaim{}).Error
	}
	return nil
}

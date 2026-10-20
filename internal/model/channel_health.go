package model

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"gorm.io/gorm"
)

// Channel health aggregates every (model, lane, channel) cell out of two
// sources of the same kind of fact: relayed requests (routing_outcome) and the
// streamed probes the scheduler sends to routes nobody called
// (channel_probe_result). Both are treated as samples of the route; see
// channelHealthEventsCTE for the few places where they differ.

// ChannelHealthCellKey identifies one (model, lane, channel) cell.
func ChannelHealthCellKey(modelName string, lane string, channelID int) string {
	return fmt.Sprintf("%s\x1f%s\x1f%d", modelName, NormalizeChannelLane(lane), channelID)
}

// ChannelHealthLaneKey identifies one (model, lane) pair.
func ChannelHealthLaneKey(modelName string, lane string) string {
	return modelName + "\x1f" + NormalizeChannelLane(lane)
}

// ChannelHealthEvidenceRow is one cell's window, split by kind (real traffic
// or probe) so the caller can report how much of each it saw.
type ChannelHealthEvidenceRow struct {
	ModelName   string
	Lane        string
	ChannelId   int
	Kind        string
	ChannelName string
	// RequestCount counts every sample, errors included; ErrorCount is the
	// subset that failed. Availability is derived, never stored.
	RequestCount int64
	ErrorCount   int64
	// DurationSumMs over DurationCount is the mean total duration of samples
	// that recorded one.
	DurationSumMs int64
	DurationCount int64
	// OutputTokens and DecodeMs together give throughput. Summing both and
	// dividing once weights the result by volume, which is far steadier than
	// averaging a per-request ratio over a handful of tiny responses.
	OutputTokens int64
	DecodeMs     int64
	// InterruptedCount counts streamed replies that broke after output had
	// started; StreamedCount is its denominator, every streamed reply that
	// produced output at all. A reply that never started is an availability
	// failure, not an interruption, so it stays out of both.
	InterruptedCount int64
	StreamedCount    int64
	LastEvidenceAt   ChannelHealthTime
}

// ChannelHealthPercentileRow carries the percentiles for one (model, lane).
// They live in their own query because a percentile cannot be re-derived from
// per-channel percentiles: ranking lane-wide and then grouping by channel makes
// each channel report its own first row past the threshold, and neither the
// max nor the mean of those is the lane's value.
type ChannelHealthPercentileRow struct {
	ModelName string
	Lane      string
	// P95LatencyMs ranks real traffic only; ProbeP95LatencyMs ranks probes
	// only. Total duration grows with reply length and a probe's reply is
	// short by design, so the two are kept apart and the caller uses the
	// probe figure when no successful traffic duration was measured.
	P95LatencyMs      float64
	ProbeP95LatencyMs float64
	TTFTP50Ms         float64
	// TTFTP95Ms is the slow tail of the first token. Unlike total duration it
	// does not grow with the length of the reply, so it is the tail figure
	// that describes the lane rather than what its callers asked for.
	TTFTP95Ms float64
	// P50LatencyMs and ProbeP50LatencyMs are the median total duration, kept
	// apart by kind for the same reason the p95 is: a probe sends one short
	// string or one small image, a customer may send a batch or ask for
	// high quality, so the two populations are not interchangeable.
	P50LatencyMs      float64
	ProbeP50LatencyMs float64
	// StallP50Ms is the median longest gap between two streamed chunks. Like
	// TTFT it is ranked over observed values only: a non-streaming response has
	// no stall at all, and letting those rows in as zero would pull the median
	// to zero on any lane that mixes the two.
	StallP50Ms float64
}

// ChannelHealthChannelPercentileRow carries the p95 latency of one upstream
// channel across every lane it serves. It is deliberately a second query
// rather than a re-fold of the lane percentiles: ranking lane-wide and then
// regrouping by channel makes each channel report the lane's first row past
// the threshold, which is not that channel's own p95. The provider view needs
// the channel's own tail latency, so it is ranked at channel grain here.
type ChannelHealthChannelPercentileRow struct {
	ChannelId    int
	P95LatencyMs float64
}

// ChannelHealthLastProbeRow is the newest synthetic probe of one channel,
// whichever cell it landed on. The provider surface reports it verbatim —
// when, against what, and how it failed — because "we checked ten minutes ago
// and got this" is the only evidence an idle upstream has.
type ChannelHealthLastProbeRow struct {
	ChannelId    int
	ModelName    string
	Lane         string
	ProbedAt     ChannelHealthTime
	Success      bool
	LatencyMs    int
	ErrorMessage string
}

// ChannelHealthBucketRow is one time slice of one (model, lane, channel) cell.
// Traffic and probe slices come back as separate rows and are summed by the
// caller; Kind only says which is which.
type ChannelHealthBucketRow struct {
	ModelName      string
	Lane           string
	ChannelId      int
	BucketIndex    int
	Kind           string
	RequestCount   int64
	ErrorCount     int64
	LastEvidenceAt ChannelHealthTime
}

// ChannelHealthLastOutcomeRow is the scheduler's lightweight view of real
// traffic. Probe selection only needs the newest observation per route; it
// must not run the full success/latency/token aggregation over seven days on
// every worker tick.
type ChannelHealthLastOutcomeRow struct {
	ModelName     string
	Lane          string
	ChannelId     int
	LastOutcomeAt ChannelHealthTime
}

// ChannelHealthLaneCell is one live (model, lane, channel) route taken from
// the ability table. It exists so cells with zero traffic and zero probes still
// render as "no data" instead of vanishing from the grid.
//
// "Live" means the same thing it means on the model plaza: the ability is
// enabled and its channel is enabled. The plaza is built from exactly that
// set, so a model the plaza does not sell cannot appear here either. Stale
// ability rows (a model removed from a channel's list) and routes on paused
// channels stay out of the grid — the health page answers "how is what we
// serve doing", not "what did we ever configure".
type ChannelHealthLaneCell struct {
	ModelName      string
	Lane           string
	ChannelId      int
	ChannelName    string
	ChannelStatus  int
	AbilityEnabled *bool
	Weight         uint
	Priority       int64
	// SourceType and ChannelType describe the upstream itself rather than the
	// route. The provider view groups by channel, so it needs to say what kind
	// of supply this is (官方 / 聚合 / 云厂商 / 非官方 / 未分类) next to its numbers.
	SourceType  string
	ChannelType int
	// Role is what this upstream is for on this route: carrying traffic now,
	// standing by, or parked inventory. The health page separates them because
	// "the standby is down" and "the source we are serving from is down" are
	// not the same incident.
	Role string
}

const (
	// ChannelHealthKindTraffic marks aggregates derived from real requests.
	ChannelHealthKindTraffic = "traffic"
	// ChannelHealthKindProbe marks aggregates derived from synthetic probes.
	ChannelHealthKindProbe = "probe"
)

// ChannelHealthTime scans a nullable timestamp across the drivers this project
// supports. Postgres hands back time.Time while SQLite hands back a string.
type ChannelHealthTime struct {
	Time  time.Time
	Valid bool
}

func (t *ChannelHealthTime) Scan(value any) error {
	switch typed := value.(type) {
	case nil:
		t.Valid = false
		return nil
	case time.Time:
		t.Time = typed.UTC()
		t.Valid = true
		return nil
	case []byte:
		return t.scanString(string(typed))
	case string:
		return t.scanString(typed)
	default:
		return fmt.Errorf("unsupported channel health time value %T", value)
	}
}

func (t *ChannelHealthTime) scanString(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		t.Valid = false
		return nil
	}
	layouts := []string{
		time.RFC3339Nano,
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05.999999999Z07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
	}
	for _, layout := range layouts {
		if parsed, err := time.Parse(layout, value); err == nil {
			t.Time = parsed.UTC()
			t.Valid = true
			return nil
		}
	}
	return fmt.Errorf("parse channel health time %q", value)
}

// Value completes the sql.Scanner/driver.Valuer pair. GORM refuses to scan into
// a struct containing a custom type that implements only one half, so without
// this every query selecting LastOutcomeAt fails at runtime.
func (t ChannelHealthTime) Value() (driver.Value, error) {
	if !t.Valid {
		return nil, nil
	}
	return t.Time, nil
}

// Ptr returns a copy suitable for a JSON pointer field.
func (t ChannelHealthTime) Ptr() *time.Time {
	if !t.Valid {
		return nil
	}
	value := t.Time
	return &value
}

func abilityGroupColumn() string {
	return "abilities." + bareGroupColumn()
}

func bareGroupColumn() string {
	if common.UsingPostgreSQL {
		return `"group"`
	}
	return "`group`"
}

func channelHealthEpochExpr(column string) string {
	if common.UsingPostgreSQL {
		return fmt.Sprintf("EXTRACT(EPOCH FROM %s)", column)
	}
	if common.UsingSQLite {
		return fmt.Sprintf("CAST(strftime('%%s', %s) AS INTEGER)", column)
	}
	return fmt.Sprintf("UNIX_TIMESTAMP(%s)", column)
}

// RoutingOutcomeBucketIndexExpr is the dialect-aware slice index of a
// timestamp column: floor((epoch(column) - ?) / ?), taking the window start in
// Unix seconds and the slice width in seconds as its two arguments.
func RoutingOutcomeBucketIndexExpr(column string) string {
	return channelHealthBucketIndexExpr(column)
}

func channelHealthBucketIndexExpr(column string) string {
	quotient := fmt.Sprintf("(%s - ?) / ?", channelHealthEpochExpr(column))
	// PostgreSQL rounds fractional numeric values when casting them to BIGINT.
	// Floor the quotient explicitly so the latter half of a bucket cannot move
	// into the next index. MySQL uses the same explicit rule; SQLite's integer
	// epoch arithmetic already truncates and avoids depending on math extensions.
	if !common.UsingSQLite {
		quotient = fmt.Sprintf("FLOOR(%s)", quotient)
	}
	return fmt.Sprintf("CAST(%s AS %s)", quotient, channelHealthIntegerCast())
}

// SQLite renders booleans as integers; Postgres and MySQL accept TRUE.
func channelHealthTrueLiteral() string {
	if common.UsingSQLite {
		return "1"
	}
	return "TRUE"
}

func channelHealthIntegerCast() string {
	if common.UsingPostgreSQL {
		return "BIGINT"
	}
	return "INTEGER"
}

// channelHealthLaneExpr resolves the immutable lane stamped when the request
// was routed. Legacy rows without final_group are excluded from lane health:
// deriving them from a channel's current group would rewrite history whenever
// an operator moves that channel and can misclassify eco abilities as stable.
func channelHealthLaneExpr() string {
	return fmt.Sprintf("COALESCE(NULLIF(o.final_group, ''), '%s')", ChannelLaneDirect)
}

func channelHealthHasTable(db *gorm.DB, dst any) bool {
	if db == nil {
		return false
	}
	if channelHealthUsesCore(db) {
		switch dst.(type) {
		case *TelemetryChannel:
			dst = &Channel{}
		case *TelemetryAbility:
			dst = &Ability{}
		}
	}
	if db.Migrator().HasTable(dst) {
		return true
	}
	// Outside a validated catalogue (during a migration, or on a connection
	// opened without validation) GORM's fallback only counts base tables, and
	// routing_outcome and routing_decision are views. Ask the catalogue for
	// any relation instead, or the health reads silently lose all traffic.
	if db.Dialector.Name() != "postgres" {
		return false
	}
	stmt := &gorm.Statement{DB: db}
	if err := stmt.Parse(dst); err != nil || stmt.Table == "" {
		return false
	}
	var found bool
	if err := db.Raw("SELECT to_regclass(?) IS NOT NULL", stmt.Table).Scan(&found).Error; err != nil {
		return false
	}
	return found
}

// channelHealthDB hands back the connection these queries must use for their
// whole run. Reading the package-level handle twice — once to decide the table
// exists, once to run the query — is a race: a background refresh can find it
// replaced in between and dereference nil. Every query below takes it once.
func channelHealthDB() *gorm.DB {
	return TelemetryDB()
}

// ChannelHealthEvidenceExpr excludes caller faults from the reliability
// population. Counting ordinary 4xx responses as successes would inflate a
// broken route's availability; counting them as failures would punish the
// route for malformed requests. Either way they are not channel evidence.
const ChannelHealthEvidenceExpr = `COALESCE(o.health_eligible, (
			COALESCE(o.status_code, 0) < 400
			OR COALESCE(o.status_code, 0) = 429
			OR COALESCE(o.status_code, 0) >= 500
		))`

// channelHealthPopulationExpr additionally requires request-time lane
// provenance. Admin reliability can still use ChannelHealthEvidenceExpr for
// legacy outcomes because it does not split by lane; lane health cannot.
const channelHealthPopulationExpr = `(` + ChannelHealthEvidenceExpr + `
			AND COALESCE(o.final_group, '') <> '')`

// ChannelHealthFailureExpr classifies an eligible request as a CHANNEL failure.
//
// It deliberately mirrors categorizeForMetrics in the distributor, which is the
// router's own definition of channel reliability: only upstream faults and rate
// limiting count. A malformed request, a content-policy refusal, an
// over-long context or any other ordinary 4xx is the caller's, and charging it
// to the channel would make a lane look broken because its users sent bad
// requests. 5xx, 429 and transport failures with no status count; other 4xx do
// not.
//
// It departs from the router's verdict in exactly one place: a 200 that
// produced no output tokens is a failure here even when the Go side called it
// a success. That is not a hypothetical. An upstream that forwards an error
// frame inside an already-committed 200 stream ends the request with a clean
// status, no error_code, zero tokens and a $0 billing row, and every one of
// those requests used to read as a healthy success while the user watched the
// reply die mid-sentence. The only honest evidence of that class of failure is
// the absence of output, so that is what gets counted.
//
// Only the reporting SQL learns this. The live breaker keeps using the Go-side
// verdict, on purpose: it decides during the request, before the token count
// is final, and a breaker that trips on a zero-token 200 would also trip on
// every legitimately empty completion.
//
// The zero-output rule applies to streamed replies only. A stream that
// committed a 200 and never produced a token is unambiguous. A non-streamed
// reply is not: an embedding, a moderation or a rerank records its prompt and
// no output figure at all, and an empty chat completion records exactly the
// same (a zero is stored as NULL), so reading that shape as a failure filed
// every successful embedding as broken.
const ChannelHealthFailureExpr = `CASE
			WHEN COALESCE(o.health_failed, FALSE) THEN 1
			WHEN COALESCE(o.status_code, 0) >= 200
				AND COALESCE(o.status_code, 0) < 300
				AND COALESCE(o.actual_output_tokens, 0) = 0
				AND COALESCE(o.was_streaming, FALSE)
			THEN 1
			WHEN o.health_failed IS NOT NULL
			THEN 0
			WHEN COALESCE(o.status_code, 0) >= 200
				AND COALESCE(o.status_code, 0) < 300
				AND COALESCE(o.error_code, '') = ''
				AND COALESCE(o.error_type, '') = ''
			THEN 0 ELSE 1
		END`

// ChannelHealthOutcomeSourceSQL adds only upstream failures that were superseded
// by a retry. The request-level RoutingOutcome still owns the final attempt and
// all usage/streaming fields, so the union represents each provider attempt
// once without duplicating tokens or throughput.
func ChannelHealthOutcomeSourceSQL() string {
	return channelHealthOutcomeSourceSQL(channelHealthDB())
}

func channelHealthOutcomeSourceSQL(db *gorm.DB) string {
	if db == nil {
		return "routing_outcome AS o"
	}
	if !channelHealthHasTable(db, &RoutingAttemptOutcome{}) {
		return "routing_outcome AS o"
	}
	// Every column any health expression reads has to appear on BOTH halves of
	// the union, in the same order. A superseded attempt row carries no usage,
	// streaming or completion detail at all, so those arrive as typed NULLs and
	// the expressions above COALESCE them away. fact says which half a row came
	// from: a superseded attempt is counted on its upstream, not on its lane.
	return `(
		SELECT request_id, outcome_at, decision_id, channel_id, final_group,
			status_code, error_code, error_type, provider_attempted,
			health_eligible, health_failed, total_duration_ms,
			ttft_ms, actual_output_tokens, was_streaming,
			actual_input_tokens, stream_aborted, max_stall_ms,
			answer_ttft_ms, reasoning_tokens, 'outcome' AS fact
		FROM routing_outcome
		UNION ALL
		SELECT request_id, outcome_at, decision_id, channel_id, final_group,
			status_code, error_code, error_type, provider_attempted,
			health_eligible, health_failed, total_duration_ms,
			NULL AS ttft_ms, NULL AS actual_output_tokens,
			NULL AS was_streaming, NULL AS actual_input_tokens,
			NULL AS stream_aborted, NULL AS max_stall_ms,
			NULL AS answer_ttft_ms, NULL AS reasoning_tokens, 'attempt' AS fact
		FROM routing_attempt_outcome
	) AS o`
}

// channelHealthEventsCTE is the single population every health aggregate
// reads: one row per real provider attempt and one row per synthetic probe,
// in the same shape.
//
// A probe is a request of fixed size, so its first-token time, its longest
// pause and its decode speed are samples of the lane exactly as a customer's
// request is, and they are counted as such. Total duration is kept apart by
// kind for text lanes, because a probe's reply is short by design; for
// embeddings, images and rerank the request is the same shape either way, so
// a probe's duration is a plain sample of the lane.
//
// Decode speed only counts a reply whose output arrived over a measurable
// span. An upstream that buffers the whole answer and flushes it as one frame
// reports a first token a millisecond before the last, and 60 tokens over one
// millisecond is sixty thousand tokens a second — a number that swamps every
// honest sample it is summed with. A span under 50ms, or a rate above three
// tokens per millisecond, is not a decode measurement.
//
// Decode speed counts only tokens that streamed during the span it divides
// by, from the first output token to the end. Reasoning is billed as output
// whether or not it is streamed. When it streams (reasoning_content, thinking
// deltas, a reply that only reasoned), it is generated inside that span and
// counts. When it is hidden, all of it happens before the first token — the
// first token is then the first answer token — so the reported reasoning is
// taken out; counting it read Claude lanes with omitted thinking at 700–1100
// tok/s. The answer time tells the two apart: equal to the first token means
// nothing streamed before the answer. Rows written before the answer time was
// recorded (NULL) carry no sample, nor does a reply too short to time.
//
// Rows cover [from, to). A provider attempt a retry superseded comes back as
// kind 'attempt', a customer request's answer as 'traffic', a probe as 'probe'.
//
// The result is a CTE body plus its arguments, which always come first in the
// enclosing statement. ok is false when neither source table exists.
func channelHealthEventsCTE(db *gorm.DB, from, to time.Time) (string, []any, bool) {
	branches := make([]string, 0, 2)
	args := make([]any, 0, 3)
	if channelHealthHasTable(db, &RoutingOutcome{}) && channelHealthHasTable(db, &RoutingDecision{}) {
		streamedTokens := channelHealthStreamedTokensExpr("o.actual_output_tokens", "o.reasoning_tokens", "o.ttft_ms", "o.answer_ttft_ms")
		decoded := `COALESCE(o.actual_output_tokens, 0) > 0
				AND COALESCE(o.ttft_ms, 0) > 0
				AND o.answer_ttft_ms IS NOT NULL
				AND ` + streamedTokens + ` >= ` + channelHealthMinDecodeTokens + `
				AND COALESCE(o.total_duration_ms, 0) - o.ttft_ms >= ` + channelHealthMinDecodeMs + `
				AND ` + streamedTokens + ` <= (o.total_duration_ms - o.ttft_ms) * ` + channelHealthMaxTokensPerMs
		hasAttempts := channelHealthHasTable(db, &RoutingAttemptOutcome{})
		source := channelHealthOutcomeSourceSQL(db)
		decisionJoin := "LEFT JOIN routing_decision AS d ON d.id = o.decision_id"
		modelExpr := "COALESCE(NULLIF(d.resolved_model, ''), NULLIF(d.requested_model, ''), 'unknown')"
		kindExpr := "'" + ChannelHealthKindTraffic + "'"
		if hasAttempts {
			kindExpr = "CASE WHEN o.fact = 'attempt' THEN '" + RouteHealthKindAttempt + "' ELSE '" + ChannelHealthKindTraffic + "' END"
		}
		if routingFactsAreViews(db) {
			windowed, sourceArgs := channelHealthWindowSourcePostgres(from, to, hasAttempts)
			source = "(" + windowed + ") AS o"
			decisionJoin = ""
			modelExpr = "COALESCE(NULLIF(o.decision_resolved_model, ''), NULLIF(o.decision_requested_model, ''), 'unknown')"
			args = append(args, sourceArgs...)
		}
		branches = append(branches, fmt.Sprintf(`
	SELECT
		%[7]s AS model_name,
		%[1]s AS lane,
		COALESCE(o.channel_id, 0) AS channel_id,
		o.outcome_at AS outcome_at,
		%[2]s AS kind,
		%[3]s AS is_error,
		CASE WHEN %[3]s = 0 AND COALESCE(o.total_duration_ms, 0) > 0 THEN o.total_duration_ms ELSE 0 END AS duration_ms,
		CASE WHEN %[3]s = 0 AND COALESCE(o.ttft_ms, 0) > 0 THEN o.ttft_ms ELSE 0 END AS ttft_ms,
		CASE WHEN %[3]s = 0 AND COALESCE(o.max_stall_ms, 0) > 0 THEN o.max_stall_ms ELSE 0 END AS max_stall_ms,
		CASE WHEN %[6]s THEN `+streamedTokens+` ELSE 0 END AS output_tokens,
		CASE WHEN %[6]s THEN o.total_duration_ms - o.ttft_ms ELSE 0 END AS decode_ms,
		CASE WHEN COALESCE(o.was_streaming, FALSE) AND COALESCE(o.stream_aborted, FALSE)
			AND COALESCE(o.actual_output_tokens, 0) > 0 THEN 1 ELSE 0 END AS interrupted,
		CASE WHEN COALESCE(o.was_streaming, FALSE)
			AND COALESCE(o.actual_output_tokens, 0) > 0 THEN 1 ELSE 0 END AS streamed
	FROM %[4]s
	%[8]s
	WHERE o.outcome_at >= ? AND o.outcome_at < ? AND %[5]s`,
			channelHealthLaneExpr(), kindExpr, ChannelHealthFailureExpr,
			source, channelHealthPopulationExpr, decoded, modelExpr, decisionJoin))
		args = append(args, from, to)
	}
	if channelHealthHasTable(db, &ChannelProbeResult{}) {
		// A failed probe's timings describe how fast the upstream said no, which
		// is not a latency sample of the lane, so only successes carry them.
		// Pre-upgrade 10-token non-streaming text probes also have latency_ms,
		// but neither output tokens nor a modality; their duration is not
		// comparable to streamed probes and stays out.
		streamedTokens := channelHealthStreamedTokensExpr("p.output_tokens", "p.reasoning_tokens", "p.ttft_ms", "p.answer_ttft_ms")
		decoded := `p.success
				AND COALESCE(p.output_tokens, 0) > 0
				AND COALESCE(p.ttft_ms, 0) > 0
				AND p.answer_ttft_ms IS NOT NULL
				AND ` + streamedTokens + ` >= ` + channelHealthMinDecodeTokens + `
				AND COALESCE(p.latency_ms, 0) - p.ttft_ms >= ` + channelHealthMinDecodeMs + `
				AND ` + streamedTokens + ` <= (p.latency_ms - p.ttft_ms) * ` + channelHealthMaxTokensPerMs
		branches = append(branches, fmt.Sprintf(`
	SELECT
		p.model_name AS model_name,
		p.lane AS lane,
		p.channel_id AS channel_id,
		p.probed_at AS outcome_at,
		'%[1]s' AS kind,
		CASE WHEN p.success THEN 0 ELSE 1 END AS is_error,
		CASE WHEN p.success AND COALESCE(p.latency_ms, 0) > 0
			AND (COALESCE(p.output_tokens, 0) > 0 OR COALESCE(p.modality, '') IN (%[3]s))
			THEN p.latency_ms ELSE 0 END AS duration_ms,
		CASE WHEN p.success AND COALESCE(p.ttft_ms, 0) > 0 THEN p.ttft_ms ELSE 0 END AS ttft_ms,
		CASE WHEN p.success AND COALESCE(p.max_stall_ms, 0) > 0 THEN p.max_stall_ms ELSE 0 END AS max_stall_ms,
		CASE WHEN %[2]s THEN `+streamedTokens+` ELSE 0 END AS output_tokens,
		CASE WHEN %[2]s THEN p.latency_ms - p.ttft_ms ELSE 0 END AS decode_ms,
		CASE WHEN NOT p.success AND COALESCE(p.ttft_ms, 0) > 0 THEN 1 ELSE 0 END AS interrupted,
		CASE WHEN COALESCE(p.ttft_ms, 0) > 0 THEN 1 ELSE 0 END AS streamed
	FROM channel_probe_result AS p
	WHERE p.probed_at >= ? AND p.probed_at < ? AND p.skipped = ?
		AND COALESCE(p.modality, '') <> ?`, ChannelHealthKindProbe, decoded, channelHealthSampledDurationModalities))
		args = append(args, from, to, false, ChannelProbeModalityVideoLiveness)
	}
	if len(branches) == 0 {
		return "", nil, false
	}
	return strings.Join(branches, "\n\tUNION ALL\n"), args, true
}

// channelHealthStreamedTokensExpr is the output that streamed between the
// first token and the end: all of it when anything arrived before the first
// answer token or no answer came at all (the reasoning was streamed), and the
// output minus the reported reasoning when the first token was the answer
// (the reasoning was hidden and happened before the span).
func channelHealthStreamedTokensExpr(output, reasoning, ttft, answer string) string {
	return `(` + output + ` - CASE WHEN ` + answer + ` = ` + ttft + `
				THEN COALESCE(` + reasoning + `, 0) ELSE 0 END)`
}

const (
	// channelHealthMinDecodeMs and channelHealthMaxTokensPerMs bound what
	// counts as a decode-speed sample; see channelHealthEventsCTE.
	channelHealthMinDecodeMs     = "50"
	channelHealthMaxTokensPerMs  = "3"
	channelHealthMinDecodeTokens = "16"
	// channelHealthSampledDurationModalities are the probe modalities whose
	// total duration is a plain latency sample of the lane.
	channelHealthSampledDurationModalities = "'embedding', 'image', 'rerank'"
)

// The aggregate queries below read the hourly route health rows
// (route_health.go). Their names and row shapes are what the health page,
// the probe scheduler and their tests have always used.

// QueryChannelHealthEvidence sums the window per (model, lane, channel, kind).
func QueryChannelHealthEvidence(since time.Time) ([]ChannelHealthEvidenceRow, error) {
	return QueryChannelHealthEvidenceContext(context.Background(), since)
}

func QueryChannelHealthEvidenceContext(ctx context.Context, since time.Time) ([]ChannelHealthEvidenceRow, error) {
	window, err := LoadRouteHealthWindow(ctx, since, time.Now())
	if err != nil {
		return nil, err
	}
	return window.Evidence(), nil
}

// QueryChannelHealthPercentiles ranks each lane's latency figures over the
// routes serving it now, leaving out excluded routes.
func QueryChannelHealthPercentiles(since time.Time, excluded ...ChannelHealthRouteRef) ([]ChannelHealthPercentileRow, error) {
	return QueryChannelHealthPercentilesWithAliases(since, nil, excluded...)
}

// QueryChannelHealthPercentilesWithAliases ranks routed variants together
// under their published catalog name.
func QueryChannelHealthPercentilesWithAliases(since time.Time, aliases map[string]string, excluded ...ChannelHealthRouteRef) ([]ChannelHealthPercentileRow, error) {
	return QueryChannelHealthPercentilesWithAliasesContext(context.Background(), since, aliases, excluded...)
}

func QueryChannelHealthPercentilesWithAliasesContext(ctx context.Context, since time.Time, aliases map[string]string, excluded ...ChannelHealthRouteRef) ([]ChannelHealthPercentileRow, error) {
	return queryRouteHealthPercentiles(ctx, since, RouteHealthPopulation{Aliases: aliases, Excluded: excluded})
}

// QueryChannelHealthPublicPercentilesWithAliases pools measurements across
// routes that share a public lane, for the named catalog models.
func QueryChannelHealthPublicPercentilesWithAliases(since time.Time, aliases map[string]string, models []string, excluded ...ChannelHealthRouteRef) ([]ChannelHealthPercentileRow, error) {
	return QueryChannelHealthPublicPercentilesWithAliasesContext(context.Background(), since, aliases, models, excluded...)
}

func QueryChannelHealthPublicPercentilesWithAliasesContext(ctx context.Context, since time.Time, aliases map[string]string, models []string, excluded ...ChannelHealthRouteRef) ([]ChannelHealthPercentileRow, error) {
	if len(models) == 0 {
		return []ChannelHealthPercentileRow{}, ctx.Err()
	}
	return queryRouteHealthPercentiles(ctx, since, RouteHealthPopulation{Aliases: aliases, PublicModels: models, Excluded: excluded})
}

func queryRouteHealthPercentiles(ctx context.Context, since time.Time, population RouteHealthPopulation) ([]ChannelHealthPercentileRow, error) {
	grid, err := LoadChannelHealthGridContext(ctx)
	if err != nil {
		return nil, err
	}
	window, err := LoadRouteHealthWindow(ctx, since, time.Now())
	if err != nil {
		return nil, err
	}
	population.Current = RouteHealthCurrentCells(grid)
	return window.Percentiles(population), nil
}

// ChannelHealthRouteRef names one (model, lane, channel) route by the name its
// ability is stored under.
type ChannelHealthRouteRef struct {
	ModelName string
	Lane      string
	ChannelId int
}

// QueryChannelHealthChannelPercentiles ranks each upstream's own total
// duration across every lane it serves, over customer requests.
func QueryChannelHealthChannelPercentiles(since time.Time) ([]ChannelHealthChannelPercentileRow, error) {
	return QueryChannelHealthChannelPercentilesContext(context.Background(), since)
}

func QueryChannelHealthChannelPercentilesContext(ctx context.Context, since time.Time) ([]ChannelHealthChannelPercentileRow, error) {
	grid, err := LoadChannelHealthGridContext(ctx)
	if err != nil {
		return nil, err
	}
	window, err := LoadRouteHealthWindow(ctx, since, time.Now())
	if err != nil {
		return nil, err
	}
	return window.ChannelPercentiles(RouteHealthEnabledChannels(grid)), nil
}

// RouteHealthEnabledChannels is the set of channels that serve some route now.
func RouteHealthEnabledChannels(grid []ChannelHealthLaneCell) map[int]bool {
	enabled := make(map[int]bool, len(grid))
	for _, cell := range grid {
		if cell.ChannelStatus == common.ChannelStatusEnabled {
			enabled[cell.ChannelId] = true
		}
	}
	return enabled
}

// QueryChannelHealthLastProbes returns the newest non-skipped probe per
// channel. Skipped attempts are excluded here exactly as they are from the
// health aggregates: a local pre-provider error says nothing about the
// upstream, so surfacing it as "last checked" would misreport what we know.
func QueryChannelHealthLastProbes(since time.Time) ([]ChannelHealthLastProbeRow, error) {
	return QueryChannelHealthLastProbesContext(context.Background(), since)
}

func QueryChannelHealthLastProbesContext(ctx context.Context, since time.Time) ([]ChannelHealthLastProbeRow, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	db := channelHealthDB()
	if db != nil {
		db = db.WithContext(ctx)
	}
	if db == nil {
		return []ChannelHealthLastProbeRow{}, ctx.Err()
	}
	if !channelHealthHasTable(db, &ChannelProbeResult{}) {
		return []ChannelHealthLastProbeRow{}, ctx.Err()
	}
	var rows []ChannelHealthLastProbeRow
	query := `
	WITH ranked AS (
		SELECT
			p.*,
			ROW_NUMBER() OVER (
				PARTITION BY p.channel_id
				ORDER BY p.probed_at DESC, p.id DESC
			) AS evidence_rank
		FROM channel_probe_result AS p
		WHERE p.probed_at >= ? AND p.skipped = ?
	)
	SELECT
		channel_id,
		model_name,
		lane,
		probed_at AS probed_at,
		success,
		COALESCE(latency_ms, 0) AS latency_ms,
		COALESCE(error_message, '') AS error_message
	FROM ranked
	WHERE evidence_rank = 1`
	if err := channelHealthScan(db, channelHealthQueryTables(db, query), &rows, since, false); err != nil {
		return nil, err
	}
	for i := range rows {
		rows[i].Lane = NormalizeChannelLane(rows[i].Lane)
	}
	return rows, nil
}

// ChannelHealthUnprobeableRow is the newest reason one route could not be
// checked at all. A skipped probe is not a verdict about the upstream, but it
// is very much a fact about us: a route we cannot price, cannot address, or
// have excluded from synthetic traffic will never accumulate evidence, and
// reporting that as "quiet" hides a permanent blind spot behind a colour that
// means "nothing happened here".
type ChannelHealthUnprobeableRow struct {
	ModelName string
	Lane      string
	ChannelId int
	SkippedAt ChannelHealthTime
	Reason    string
}

// QueryChannelHealthUnprobeable returns the most recent skip per route, and
// only where nothing has been checked since — a skip later superseded by a
// real probe is history, not a blind spot.
func QueryChannelHealthUnprobeable(since time.Time) ([]ChannelHealthUnprobeableRow, error) {
	return QueryChannelHealthUnprobeableContext(context.Background(), since)
}

func QueryChannelHealthUnprobeableContext(ctx context.Context, since time.Time) ([]ChannelHealthUnprobeableRow, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	db := channelHealthDB()
	if db != nil {
		db = db.WithContext(ctx)
	}
	if db == nil {
		return []ChannelHealthUnprobeableRow{}, ctx.Err()
	}
	if !channelHealthHasTable(db, &ChannelProbeResult{}) {
		return []ChannelHealthUnprobeableRow{}, ctx.Err()
	}
	var rows []ChannelHealthUnprobeableRow
	query := `
	WITH ranked AS (
		SELECT
			p.lane, p.model_name, p.channel_id, p.probed_at, p.skipped,
			COALESCE(p.error_message, '') AS error_message,
			ROW_NUMBER() OVER (
				PARTITION BY p.lane, p.model_name, p.channel_id
				ORDER BY p.probed_at DESC, p.id DESC
			) AS evidence_rank
		FROM channel_probe_result AS p
		WHERE p.probed_at >= ?
	)
	SELECT lane, model_name, channel_id,
		probed_at AS skipped_at,
		error_message AS reason
	FROM ranked
	WHERE evidence_rank = 1 AND skipped = ?`
	if err := channelHealthScan(db, channelHealthQueryTables(db, query), &rows, since, true); err != nil {
		return nil, err
	}
	return rows, nil
}

// QueryChannelHealthLastOutcomes returns only the newest customer evidence for
// each route, for the probe scheduler.
func QueryChannelHealthLastOutcomes(since time.Time) ([]ChannelHealthLastOutcomeRow, error) {
	return QueryChannelHealthLastOutcomesContext(context.Background(), since)
}

func QueryChannelHealthLastOutcomesContext(ctx context.Context, since time.Time) ([]ChannelHealthLastOutcomeRow, error) {
	window, err := LoadRouteHealthWindow(ctx, since, time.Now())
	if err != nil {
		return nil, err
	}
	return window.LastOutcomes(), nil
}

// QueryChannelHealthBuckets slices the window into bucketCount slices of
// bucketSeconds per (model, lane, channel, kind), customer answers and probes
// only. It powers the heat wall.
func QueryChannelHealthBuckets(since time.Time, bucketSeconds int64, bucketCount int) ([]ChannelHealthBucketRow, error) {
	return QueryChannelHealthBucketsContext(context.Background(), since, bucketSeconds, bucketCount)
}

func QueryChannelHealthBucketsContext(ctx context.Context, since time.Time, bucketSeconds int64, bucketCount int) ([]ChannelHealthBucketRow, error) {
	if bucketSeconds <= 0 || bucketCount <= 0 {
		return []ChannelHealthBucketRow{}, ctx.Err()
	}
	until := since.Add(time.Duration(bucketSeconds*int64(bucketCount)) * time.Second)
	window, err := LoadRouteHealthWindow(ctx, since, until)
	if err != nil {
		return nil, err
	}
	return window.Buckets(since, bucketSeconds, bucketCount, false), nil
}

// LoadChannelHealthGrid lists every current (model, lane, channel) assignment.
// Disabled assignments remain here as explicit historical/inactive evidence:
// omitting them lets retained traffic recreate a provider as active.
func LoadChannelHealthGrid() ([]ChannelHealthLaneCell, error) {
	return LoadChannelHealthGridContext(context.Background())
}

func LoadChannelHealthGridContext(ctx context.Context) ([]ChannelHealthLaneCell, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	db := CoreDB()
	if db != nil {
		db = db.WithContext(ctx)
	}
	if db == nil {
		return []ChannelHealthLaneCell{}, ctx.Err()
	}
	result := []ChannelHealthLaneCell{}
	if !channelHealthHasTable(db, &Ability{}) || !channelHealthHasTable(db, &Channel{}) {
		return result, ctx.Err()
	}
	var rows []ChannelHealthLaneCell
	if err := db.Table("abilities").
		Select(abilityGroupColumn()+" AS lane, abilities.model AS model_name, abilities.channel_id AS channel_id, abilities.enabled AS ability_enabled, "+
			"abilities.weight AS weight, COALESCE(abilities.priority, 0) AS priority, "+
			"COALESCE(NULLIF(channels.name, ''), 'unknown') AS channel_name, channels.status AS channel_status, "+
			"COALESCE(channels.source_type, '') AS source_type, COALESCE(channels.type, 0) AS channel_type, "+
			"COALESCE(abilities.role, '') AS role").
		Joins("JOIN channels ON channels.id = abilities.channel_id").
		Where("abilities.enabled = ? AND channels.status = ?", true, common.ChannelStatusEnabled).
		Scan(&rows).Error; err != nil {
		return result, err
	}
	for i := range rows {
		rows[i].Lane = NormalizeChannelLane(rows[i].Lane)
		rows[i].SourceType = NormalizeChannelSourceType(rows[i].SourceType)
		rows[i].Role = NormalizeAbilityRole(rows[i].Role)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].ModelName != rows[j].ModelName {
			return rows[i].ModelName < rows[j].ModelName
		}
		if rows[i].Lane != rows[j].Lane {
			return rows[i].Lane < rows[j].Lane
		}
		return rows[i].ChannelName < rows[j].ChannelName
	})
	return rows, nil
}

func channelHealthQueryTables(db *gorm.DB, query string) string {
	if channelHealthUsesCore(db) {
		query = strings.ReplaceAll(query, "telemetry_channels channels", "channels")
		query = strings.ReplaceAll(query, "telemetry_abilities abilities", "abilities")
	}
	return query
}

// ChannelHealthAnalysisContext marks background analytical reads, never relay or
// financial work. The captured store is not replaced or changed globally.
func ChannelHealthAnalysisContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, channelHealthAnalysisKey{}, true)
}

type channelHealthAnalysisKey struct{}

func channelHealthUsesCore(db *gorm.DB) bool {
	core := CoreDB()
	return db != nil && core != nil && db.Statement.ConnPool == core.Statement.ConnPool
}

func channelHealthScan(db *gorm.DB, query string, dest interface{}, args ...interface{}) error {
	ctx := db.Statement.Context
	if ctx == nil {
		ctx = context.Background()
	}
	if budgeted, _ := ctx.Value(channelHealthAnalysisKey{}).(bool); !budgeted || db.Dialector.Name() != "postgres" {
		err := db.Raw(query, args...).Scan(dest).Error
		if err != nil && ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	// Warn at half the budget: a health query creeping toward its timeout is
	// how the plaza went grey on 2026-10-10.
	ctx = WithSlowQueryThreshold(ctx, 2500*time.Millisecond)
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT set_config('statement_timeout','5000',true), set_config('lock_timeout','500',true), set_config('max_parallel_workers_per_gather','0',true), set_config('jit','off',true)").Error; err != nil {
			return err
		}
		return tx.Raw(query, args...).Scan(dest).Error
	}, &sql.TxOptions{ReadOnly: true})
	if err != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

// The window's outcomes, read from the typed log columns (see
// routing_columns.go) plus any pre-cutover history no log row superseded.
// The time bound sits inside each branch so it reaches the ro_outcome_at and
// legacy outcome_at indexes. A request's decision is stamped on the same log
// row as its outcome, so its model is read from that row; only outcomes whose
// decision lives elsewhere (pre-cutover history, attempts) are resolved by id.
// Resolving every outcome by id was a self-join of logs that cost 1.1 s of a
// 1.75 s seven-day read on 2026-10-14.
func channelHealthWindowSourcePostgres(from, to time.Time, hasAttempts bool) (string, []interface{}) {
	attempts := ""
	args := []interface{}{from, to, from, to}
	if hasAttempts {
		attempts = ` UNION ALL
			SELECT request_id,outcome_at,decision_id,channel_id,final_group,status_code,error_code,error_type,
				provider_attempted,health_eligible,health_failed,total_duration_ms,
				NULL::bigint,NULL::bigint,NULL::boolean,NULL::bigint,NULL::boolean,NULL::bigint,NULL::bigint,NULL::bigint,
				'attempt'::text,FALSE,NULL::text,NULL::text
			FROM routing_attempt_outcome WHERE outcome_at >= ? AND outcome_at < ? AND ` + strings.ReplaceAll(ChannelHealthEvidenceExpr, "o.", "routing_attempt_outcome.")
		args = append(args, from, to)
	}
	query := `WITH health_outcomes AS MATERIALIZED (
		SELECT snap.ro_request_id AS request_id, snap.ro_outcome_at AS outcome_at,
			snap.ro_decision_id AS decision_id, snap.ro_channel_id AS channel_id,
			snap.ro_final_group AS final_group, snap.ro_status_code AS status_code,
			snap.ro_error_code AS error_code, snap.ro_error_type AS error_type,
			snap.ro_provider_attempted AS provider_attempted, snap.ro_health_eligible AS health_eligible,
			snap.ro_health_failed AS health_failed, snap.ro_total_duration_ms AS total_duration_ms,
			snap.ro_ttft_ms AS ttft_ms, snap.ro_actual_output_tokens AS actual_output_tokens,
			snap.ro_was_streaming AS was_streaming, snap.ro_actual_input_tokens AS actual_input_tokens,
			snap.ro_stream_aborted AS stream_aborted, snap.ro_max_stall_ms AS max_stall_ms,
			snap.ro_answer_ttft_ms AS answer_ttft_ms, snap.ro_reasoning_tokens AS reasoning_tokens,
			'outcome'::text AS fact,
			snap.rd_id IS NOT DISTINCT FROM snap.ro_decision_id AS decision_on_row,
			snap.rd_resolved_model AS row_resolved_model, snap.rd_requested_model AS row_requested_model
		FROM logs snap
		WHERE snap.ro_request_id IS NOT NULL AND snap.ro_outcome_at >= ? AND snap.ro_outcome_at < ? AND ` + strings.ReplaceAll(ChannelHealthEvidenceExpr, "o.", "snap.ro_") + `
		UNION ALL
		SELECT old.request_id,old.outcome_at,old.decision_id,old.channel_id,old.final_group,old.status_code,
			old.error_code,old.error_type,old.provider_attempted,old.health_eligible,old.health_failed,
			old.total_duration_ms,old.ttft_ms,old.actual_output_tokens,old.was_streaming,old.actual_input_tokens,
			old.stream_aborted,old.max_stall_ms,old.answer_ttft_ms,old.reasoning_tokens,'outcome'::text,
			FALSE,NULL::text,NULL::text
		FROM routing_outcome_legacy old WHERE old.outcome_at >= ? AND old.outcome_at < ? AND ` + strings.ReplaceAll(ChannelHealthEvidenceExpr, "o.", "old.") + `
			AND NOT EXISTS (SELECT 1 FROM logs newer WHERE newer.request_id=old.request_id
				AND newer.ro_request_id IS NOT NULL OFFSET 0)` + attempts + `
	), health_decision_keys AS MATERIALIZED (
		SELECT DISTINCT decision_id FROM health_outcomes WHERE NOT decision_on_row
	), health_decisions AS (
		SELECT snap.rd_id AS id, snap.rd_resolved_model AS resolved_model, snap.rd_requested_model AS requested_model
		FROM logs snap
		WHERE snap.rd_id IN (SELECT decision_id FROM health_decision_keys)
		UNION ALL
		SELECT old.id,old.resolved_model,old.requested_model
		FROM health_decision_keys keys JOIN LATERAL (
			SELECT id,request_id,resolved_model,requested_model FROM routing_decision_legacy
			WHERE id=keys.decision_id OFFSET 0
		) old ON TRUE
		WHERE NOT EXISTS (SELECT 1 FROM logs newer WHERE newer.request_id=old.request_id
			AND newer.rd_id IS NOT NULL OFFSET 0)
	)
	SELECT o.request_id,o.outcome_at,o.decision_id,o.channel_id,o.final_group,o.status_code,o.error_code,o.error_type,
		o.provider_attempted,o.health_eligible,o.health_failed,o.total_duration_ms,o.ttft_ms,o.actual_output_tokens,
		o.was_streaming,o.actual_input_tokens,o.stream_aborted,o.max_stall_ms,o.answer_ttft_ms,o.reasoning_tokens,o.fact,
		CASE WHEN o.decision_on_row THEN o.row_resolved_model ELSE d.resolved_model END AS decision_resolved_model,
		CASE WHEN o.decision_on_row THEN o.row_requested_model ELSE d.requested_model END AS decision_requested_model
	FROM health_outcomes o LEFT JOIN health_decisions d ON d.id=o.decision_id AND NOT o.decision_on_row OFFSET 0`
	return query, args
}

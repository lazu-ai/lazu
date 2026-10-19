package model

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Route health is read from hourly rows, never from a week of raw requests.
//
// Each row is one hour of one (model, lane, channel, kind) cell, computed from
// the raw facts of that hour alone with exactly the per-request rules the
// health SQL has always applied (channelHealthEventsCTE). A page that shows
// seven days merges 168 such hours per route; the cost no longer grows with
// traffic or with retention.
//
// Hours are recomputed from raw facts rather than incremented as requests
// arrive, because a request's verdict can be rewritten after the fact: a media
// job accepted with a 202 fails minutes later, and a provisional outcome is
// replaced by the final one. Recomputing an hour is correct by construction;
// an increment would need a compensating write at every place a verdict
// changes. The rollup task refreshes the last three hours every minute and the
// last two days every thirty minutes, which covers every late write we make.
//
// Readers never depend on that task being alive: hours it has not reached are
// computed from raw facts at read time (LoadRouteHealthWindow). On 2026-10-10
// a stalled health worker left the model plaza grey for four days; with a
// live tail the page only gets slower, and RouteHealthRollupWatch says why.

const (
	// RouteHealthKindAttempt is a provider attempt that a retry superseded.
	// Operators see it on the upstream that failed; a customer-facing lane
	// counts each request once, by the answer the customer got.
	RouteHealthKindAttempt = "attempt"

	routeHealthRetention     = 30 * 24 * time.Hour
	routeHealthRecentHours   = 3
	routeHealthDeepHours     = 48
	routeHealthDeepEvery     = 30 * time.Minute
	routeHealthComputeChunk  = 6 * time.Hour
	routeHealthStaleAfter    = 5 * time.Minute
	routeHealthStateName     = "hourly"
	routeHealthHistogramSize = 64
	// Bucket 0 holds values under 10 ms; bucket i ≥ 1 holds
	// [10·1.2^(i-1), 10·1.2^i) ms, the last one everything above. A value
	// read back as its bucket's geometric midpoint is within 9.6% of the
	// truth, and 62 steps reach about 12 minutes.
	routeHealthHistogramBase  = 10.0
	routeHealthHistogramRatio = 1.2
)

// RouteHealthHourly is one hour of one route's health evidence. Hour and
// LastAtMs are integers so every supported driver scans them the same way.
type RouteHealthHourly struct {
	Hour          int64  `gorm:"primaryKey;autoIncrement:false"`
	ModelName     string `gorm:"primaryKey;size:191"`
	Lane          string `gorm:"primaryKey;size:64"`
	ChannelID     int    `gorm:"primaryKey;autoIncrement:false"`
	Kind          string `gorm:"primaryKey;size:16"`
	ChannelName   string `gorm:"size:191;not null;default:''"`
	Samples       int64  `gorm:"not null;default:0"`
	Failures      int64  `gorm:"not null;default:0"`
	DurationSumMs int64  `gorm:"not null;default:0"`
	DurationCount int64  `gorm:"not null;default:0"`
	OutputTokens  int64  `gorm:"not null;default:0"`
	DecodeMs      int64  `gorm:"not null;default:0"`
	Interrupted   int64  `gorm:"not null;default:0"`
	Streamed      int64  `gorm:"not null;default:0"`
	LastAtMs      int64  `gorm:"not null;default:0"`
	DurationHist  string `gorm:"type:text;not null;default:''"`
	TTFTHist      string `gorm:"column:ttft_hist;type:text;not null;default:''"`
	StallHist     string `gorm:"type:text;not null;default:''"`
}

func (RouteHealthHourly) TableName() string { return "route_health_hourly" }

// RouteHealthRollupState records how far the hourly rows are current.
// ThroughHour is the newest hour the rollup task computed, RefreshedAtMs when.
type RouteHealthRollupState struct {
	Name              string `gorm:"primaryKey;size:32"`
	ThroughHour       int64  `gorm:"not null;default:0"`
	RefreshedAtMs     int64  `gorm:"not null;default:0"`
	DeepRefreshedAtMs int64  `gorm:"not null;default:0"`
}

func (RouteHealthRollupState) TableName() string { return "route_health_rollup_state" }

func routeHealthHourOf(at time.Time) int64 {
	unix := at.UTC().Unix()
	return unix - ((unix%3600)+3600)%3600
}

// --- histograms -----------------------------------------------------------

type routeHealthHistogram [routeHealthHistogramSize]int64

func routeHealthBucketOf(ms int64) int {
	if float64(ms) < routeHealthHistogramBase {
		return 0
	}
	index := 1 + int(math.Floor(math.Log(float64(ms)/routeHealthHistogramBase)/math.Log(routeHealthHistogramRatio)))
	if index >= routeHealthHistogramSize {
		return routeHealthHistogramSize - 1
	}
	return index
}

func routeHealthBucketMidpoint(index int) float64 {
	if index == 0 {
		return routeHealthHistogramBase / 2
	}
	lower := routeHealthHistogramBase * math.Pow(routeHealthHistogramRatio, float64(index-1))
	if index == routeHealthHistogramSize-1 {
		return lower
	}
	return lower * math.Sqrt(routeHealthHistogramRatio)
}

func (h *routeHealthHistogram) add(ms int64) {
	if ms > 0 {
		h[routeHealthBucketOf(ms)]++
	}
}

func (h *routeHealthHistogram) merge(other *routeHealthHistogram) {
	for i := range h {
		h[i] += other[i]
	}
}

func (h *routeHealthHistogram) total() int64 {
	var sum int64
	for _, count := range h {
		sum += count
	}
	return sum
}

// quantile returns the value at or past fraction of the observations, as the
// health SQL ranked them (the first rank r with r ≥ n·fraction), read back as
// that observation's bucket midpoint. Zero when nothing was observed.
func (h *routeHealthHistogram) quantile(fraction float64) float64 {
	total := h.total()
	if total == 0 {
		return 0
	}
	rank := int64(math.Ceil(float64(total) * fraction))
	if rank < 1 {
		rank = 1
	}
	var seen int64
	for index, count := range h {
		seen += count
		if seen >= rank {
			return routeHealthBucketMidpoint(index)
		}
	}
	return routeHealthBucketMidpoint(routeHealthHistogramSize - 1)
}

func (h *routeHealthHistogram) encode() string {
	var out strings.Builder
	for index, count := range h {
		if count == 0 {
			continue
		}
		if out.Len() > 0 {
			out.WriteByte(',')
		}
		out.WriteString(strconv.Itoa(index))
		out.WriteByte(':')
		out.WriteString(strconv.FormatInt(count, 10))
	}
	return out.String()
}

func decodeRouteHealthHistogram(encoded string) routeHealthHistogram {
	var h routeHealthHistogram
	for _, part := range strings.Split(encoded, ",") {
		index, count, ok := strings.Cut(part, ":")
		if !ok {
			continue
		}
		i, err1 := strconv.Atoi(index)
		c, err2 := strconv.ParseInt(count, 10, 64)
		if err1 != nil || err2 != nil || i < 0 || i >= routeHealthHistogramSize {
			continue
		}
		h[i] += c
	}
	return h
}

// --- computing hours from raw facts ---------------------------------------

type routeHealthEvent struct {
	ModelName    string
	Lane         string
	ChannelId    int
	Kind         string
	ChannelName  string
	OutcomeAt    ChannelHealthTime
	IsError      int64
	DurationMs   int64
	TtftMs       int64
	MaxStallMs   int64
	OutputTokens int64
	DecodeMs     int64
	Interrupted  int64
	Streamed     int64
}

type routeHealthKey struct {
	hour    int64
	model   string
	lane    string
	channel int
	kind    string
}

type routeHealthAccumulator struct {
	row                   RouteHealthHourly
	duration, ttft, stall routeHealthHistogram
	lastNamedAt           int64
}

// ComputeRouteHealthHours derives the hourly rows for [from, to) from raw
// facts. from and to are rounded to whole hours.
func ComputeRouteHealthHours(ctx context.Context, from, to time.Time) ([]RouteHealthHourly, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	db := channelHealthDB()
	if db == nil {
		return []RouteHealthHourly{}, nil
	}
	db = db.WithContext(ctx)
	start := time.Unix(routeHealthHourOf(from), 0).UTC()
	end := time.Unix(routeHealthHourOf(to.Add(time.Hour-time.Nanosecond)), 0).UTC()
	if !end.After(start) {
		return []RouteHealthHourly{}, nil
	}
	events, args, ok := channelHealthEventsCTE(db, start, end)
	if !ok {
		return []RouteHealthHourly{}, nil
	}
	channelName, channelJoin := "'' AS channel_name", ""
	if channelHealthHasTable(db, &TelemetryChannel{}) {
		channelName = "COALESCE(channels.name, '') AS channel_name"
		channelJoin = "LEFT JOIN telemetry_channels channels ON channels.id = e.channel_id"
	}
	query := fmt.Sprintf(`
WITH events AS (%s)
SELECT e.model_name, e.lane, e.channel_id, e.kind, %s, e.outcome_at, e.is_error,
	e.duration_ms, e.ttft_ms, e.max_stall_ms, e.output_tokens, e.decode_ms, e.interrupted, e.streamed
FROM events AS e
%s`, events, channelName, channelJoin)
	var rows []routeHealthEvent
	if err := channelHealthScan(db, channelHealthQueryTables(db, query), &rows, args...); err != nil {
		return nil, err
	}
	return aggregateRouteHealthEvents(rows), nil
}

func aggregateRouteHealthEvents(events []routeHealthEvent) []RouteHealthHourly {
	accumulators := map[routeHealthKey]*routeHealthAccumulator{}
	for _, event := range events {
		if !event.OutcomeAt.Valid {
			continue
		}
		key := routeHealthKey{
			hour:    routeHealthHourOf(event.OutcomeAt.Time),
			model:   event.ModelName,
			lane:    NormalizeChannelLane(event.Lane),
			channel: event.ChannelId,
			kind:    event.Kind,
		}
		acc := accumulators[key]
		if acc == nil {
			acc = &routeHealthAccumulator{row: RouteHealthHourly{
				Hour: key.hour, ModelName: key.model, Lane: key.lane, ChannelID: key.channel, Kind: key.kind,
			}}
			accumulators[key] = acc
		}
		row := &acc.row
		row.Samples++
		row.Failures += event.IsError
		if event.DurationMs > 0 {
			row.DurationSumMs += event.DurationMs
			row.DurationCount++
			acc.duration.add(event.DurationMs)
		}
		acc.ttft.add(event.TtftMs)
		acc.stall.add(event.MaxStallMs)
		row.OutputTokens += event.OutputTokens
		row.DecodeMs += event.DecodeMs
		row.Interrupted += event.Interrupted
		row.Streamed += event.Streamed
		at := event.OutcomeAt.Time.UnixMilli()
		if at > row.LastAtMs {
			row.LastAtMs = at
		}
		if name := strings.TrimSpace(event.ChannelName); name != "" && at >= acc.lastNamedAt {
			row.ChannelName = name
			acc.lastNamedAt = at
		}
	}
	result := make([]RouteHealthHourly, 0, len(accumulators))
	for _, acc := range accumulators {
		acc.row.DurationHist = acc.duration.encode()
		acc.row.TTFTHist = acc.ttft.encode()
		acc.row.StallHist = acc.stall.encode()
		result = append(result, acc.row)
	}
	sortRouteHealthRows(result)
	return result
}

func sortRouteHealthRows(rows []RouteHealthHourly) {
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.Hour != b.Hour {
			return a.Hour < b.Hour
		}
		if a.ModelName != b.ModelName {
			return a.ModelName < b.ModelName
		}
		if a.Lane != b.Lane {
			return a.Lane < b.Lane
		}
		if a.ChannelID != b.ChannelID {
			return a.ChannelID < b.ChannelID
		}
		return a.Kind < b.Kind
	})
}

// storeRouteHealthHours replaces the stored rows of [fromHour, toHour) with
// rows. Concurrent runs converge: each replaces the range and upserts.
func storeRouteHealthHours(ctx context.Context, db *gorm.DB, fromHour, toHour int64, rows []RouteHealthHourly) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("hour >= ? AND hour < ?", fromHour, toHour).Delete(&RouteHealthHourly{}).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		return tx.Clauses(clause.OnConflict{UpdateAll: true}).CreateInBatches(&rows, 500).Error
	})
}

// RefreshRouteHealthRollup brings the hourly rows up to now: the last three
// hours every call, the last two days when the previous deep pass is older
// than thirty minutes, and everything since the last run after an outage
// (bounded by retention). It is the rollup task's whole job and the schema
// migration's backfill.
func RefreshRouteHealthRollup(ctx context.Context, now time.Time) error {
	db := channelHealthDB()
	if db == nil || !db.Migrator().HasTable(&RouteHealthHourly{}) {
		return nil
	}
	state, found, err := loadRouteHealthRollupState(ctx, db)
	if err != nil {
		return err
	}
	nowHour := routeHealthHourOf(now)
	oldest := nowHour - int64(routeHealthRetention/time.Second)
	from := nowHour - (routeHealthRecentHours-1)*3600
	deep := !found || now.UnixMilli()-state.DeepRefreshedAtMs >= routeHealthDeepEvery.Milliseconds()
	if deep {
		from = nowHour - (routeHealthDeepHours-1)*3600
	}
	switch {
	case !found:
		from = oldest
	case state.ThroughHour < from:
		from = state.ThroughHour
	}
	if from < oldest {
		from = oldest
	}
	end := nowHour + 3600
	chunk := int64(routeHealthComputeChunk / time.Second)
	for chunkStart := from; chunkStart < end; chunkStart += chunk {
		chunkEnd := chunkStart + chunk
		if chunkEnd > end {
			chunkEnd = end
		}
		rows, err := ComputeRouteHealthHours(ctx, time.Unix(chunkStart, 0), time.Unix(chunkEnd, 0))
		if err != nil {
			return fmt.Errorf("route health: compute %s: %w", time.Unix(chunkStart, 0).UTC().Format(time.RFC3339), err)
		}
		if err := storeRouteHealthHours(ctx, db, chunkStart, chunkEnd, rows); err != nil {
			return fmt.Errorf("route health: store %s: %w", time.Unix(chunkStart, 0).UTC().Format(time.RFC3339), err)
		}
	}
	if deep {
		if err := db.WithContext(ctx).Where("hour < ?", oldest).Delete(&RouteHealthHourly{}).Error; err != nil {
			return err
		}
	}
	next := RouteHealthRollupState{
		Name: routeHealthStateName, ThroughHour: nowHour, RefreshedAtMs: now.UnixMilli(),
		DeepRefreshedAtMs: state.DeepRefreshedAtMs,
	}
	if deep {
		next.DeepRefreshedAtMs = now.UnixMilli()
	}
	return db.WithContext(ctx).Clauses(clause.OnConflict{UpdateAll: true}).Create(&next).Error
}

func loadRouteHealthRollupState(ctx context.Context, db *gorm.DB) (RouteHealthRollupState, bool, error) {
	var state RouteHealthRollupState
	err := db.WithContext(ctx).Where("name = ?", routeHealthStateName).Take(&state).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return RouteHealthRollupState{}, false, nil
	}
	return state, err == nil, err
}

// RouteHealthRollupStatus reports the rollup's progress for the lag alert.
func RouteHealthRollupStatus(ctx context.Context) (RouteHealthRollupState, bool, error) {
	db := channelHealthDB()
	if db == nil || !db.Migrator().HasTable(&RouteHealthRollupState{}) {
		return RouteHealthRollupState{}, false, nil
	}
	return loadRouteHealthRollupState(ctx, db)
}

// --- reading a window -----------------------------------------------------

// RouteHealthWindow is the hourly evidence of a period, stored or computed.
type RouteHealthWindow struct {
	Rows []RouteHealthHourly
}

// LoadRouteHealthWindow reads the hours whose start lies in [since, until).
// Hours the rollup task has not reached — or a current hour it last computed
// more than five minutes ago — are computed from raw facts instead, so a
// stalled task never blanks a page.
func LoadRouteHealthWindow(ctx context.Context, since, until time.Time) (RouteHealthWindow, error) {
	if err := ctx.Err(); err != nil {
		return RouteHealthWindow{}, err
	}
	db := channelHealthDB()
	if db == nil {
		return RouteHealthWindow{}, nil
	}
	db = db.WithContext(ctx)
	sinceHour := routeHealthHourOf(since)
	untilUnix := until.UTC().Unix()
	if untilUnix <= sinceHour {
		return RouteHealthWindow{}, nil
	}
	liveFrom := sinceHour
	if db.Migrator().HasTable(&RouteHealthHourly{}) {
		state, found, err := loadRouteHealthRollupState(ctx, db)
		if err != nil {
			return RouteHealthWindow{}, err
		}
		if found {
			liveFrom = state.ThroughHour + 3600
			nowHour := routeHealthHourOf(time.Now())
			if state.ThroughHour >= nowHour && time.Since(time.UnixMilli(state.RefreshedAtMs)) > routeHealthStaleAfter {
				liveFrom = state.ThroughHour
			}
			if liveFrom < sinceHour {
				liveFrom = sinceHour
			}
		}
	}
	window := RouteHealthWindow{}
	if storedUntil := min(liveFrom, untilUnix); storedUntil > sinceHour {
		if err := db.Where("hour >= ? AND hour < ?", sinceHour, storedUntil).Find(&window.Rows).Error; err != nil {
			return RouteHealthWindow{}, err
		}
	}
	if liveFrom < untilUnix {
		live, err := ComputeRouteHealthHours(ctx, time.Unix(liveFrom, 0), until)
		if err != nil {
			return RouteHealthWindow{}, err
		}
		for _, row := range live {
			if row.Hour >= sinceHour && row.Hour < untilUnix {
				window.Rows = append(window.Rows, row)
			}
		}
	}
	return window, nil
}

// Evidence sums the window per (model, lane, channel, kind). Attempts keep
// their own kind: the lane fold leaves them out, the upstream view counts them.
func (w RouteHealthWindow) Evidence() []ChannelHealthEvidenceRow {
	type key struct {
		model, lane, kind string
		channel           int
	}
	sums := map[key]*ChannelHealthEvidenceRow{}
	named := map[key]int64{}
	for _, row := range w.Rows {
		k := key{row.ModelName, row.Lane, row.Kind, row.ChannelID}
		sum := sums[k]
		if sum == nil {
			sum = &ChannelHealthEvidenceRow{ModelName: row.ModelName, Lane: row.Lane, ChannelId: row.ChannelID, Kind: row.Kind, ChannelName: "unknown"}
			sums[k] = sum
		}
		sum.RequestCount += row.Samples
		sum.ErrorCount += row.Failures
		sum.DurationSumMs += row.DurationSumMs
		sum.DurationCount += row.DurationCount
		sum.OutputTokens += row.OutputTokens
		sum.DecodeMs += row.DecodeMs
		sum.InterruptedCount += row.Interrupted
		sum.StreamedCount += row.Streamed
		if row.LastAtMs > 0 && (!sum.LastEvidenceAt.Valid || row.LastAtMs > sum.LastEvidenceAt.Time.UnixMilli()) {
			sum.LastEvidenceAt = ChannelHealthTime{Time: time.UnixMilli(row.LastAtMs).UTC(), Valid: true}
		}
		if row.ChannelName != "" && row.LastAtMs >= named[k] {
			sum.ChannelName = row.ChannelName
			named[k] = row.LastAtMs
		}
	}
	result := make([]ChannelHealthEvidenceRow, 0, len(sums))
	for _, sum := range sums {
		result = append(result, *sum)
	}
	sort.Slice(result, func(i, j int) bool {
		a, b := result[i], result[j]
		if a.ModelName != b.ModelName {
			return a.ModelName < b.ModelName
		}
		if a.Lane != b.Lane {
			return a.Lane < b.Lane
		}
		if a.RequestCount != b.RequestCount {
			return a.RequestCount > b.RequestCount
		}
		if a.ChannelId != b.ChannelId {
			return a.ChannelId < b.ChannelId
		}
		return a.Kind < b.Kind
	})
	return result
}

// Buckets slices the window into bucketCount slices of bucketSeconds (whole
// hours) from since. includeAttempts folds superseded attempts in as traffic,
// which only the upstream view wants.
func (w RouteHealthWindow) Buckets(since time.Time, bucketSeconds int64, bucketCount int, includeAttempts bool) []ChannelHealthBucketRow {
	if bucketSeconds <= 0 || bucketCount <= 0 {
		return []ChannelHealthBucketRow{}
	}
	type key struct {
		model, lane, kind string
		channel, index    int
	}
	sums := map[key]*ChannelHealthBucketRow{}
	start := since.UTC().Unix()
	for _, row := range w.Rows {
		kind := row.Kind
		if kind == RouteHealthKindAttempt {
			if !includeAttempts {
				continue
			}
			kind = ChannelHealthKindTraffic
		}
		offset := row.Hour - start
		if offset < 0 {
			continue
		}
		index := int(offset / bucketSeconds)
		if index >= bucketCount {
			continue
		}
		k := key{row.ModelName, row.Lane, kind, row.ChannelID, index}
		sum := sums[k]
		if sum == nil {
			sum = &ChannelHealthBucketRow{ModelName: row.ModelName, Lane: row.Lane, ChannelId: row.ChannelID, BucketIndex: index, Kind: kind}
			sums[k] = sum
		}
		sum.RequestCount += row.Samples
		sum.ErrorCount += row.Failures
		if row.LastAtMs > 0 && (!sum.LastEvidenceAt.Valid || row.LastAtMs > sum.LastEvidenceAt.Time.UnixMilli()) {
			sum.LastEvidenceAt = ChannelHealthTime{Time: time.UnixMilli(row.LastAtMs).UTC(), Valid: true}
		}
	}
	result := make([]ChannelHealthBucketRow, 0, len(sums))
	for _, sum := range sums {
		result = append(result, *sum)
	}
	sort.Slice(result, func(i, j int) bool {
		a, b := result[i], result[j]
		if a.ModelName != b.ModelName {
			return a.ModelName < b.ModelName
		}
		if a.Lane != b.Lane {
			return a.Lane < b.Lane
		}
		if a.ChannelId != b.ChannelId {
			return a.ChannelId < b.ChannelId
		}
		if a.BucketIndex != b.BucketIndex {
			return a.BucketIndex < b.BucketIndex
		}
		return a.Kind < b.Kind
	})
	return result
}

// RouteHealthPopulation names which routes a lane's percentiles rank over.
type RouteHealthPopulation struct {
	// Current holds the cell keys (ChannelHealthCellKey) of routes serving
	// now: enabled, on an enabled channel, and not parked inventory.
	Current map[string]bool
	// Aliases maps a routed model name to the catalog name it is ranked under.
	Aliases map[string]string
	// PublicModels, when non-nil, ranks only these catalog names, with every
	// lane but cheap pooled as direct — the customer-facing lanes.
	PublicModels []string
	// Excluded are further routes a lane does not count, such as an idle
	// standby.
	Excluded []ChannelHealthRouteRef
}

// RouteHealthCurrentCells is the route set percentiles rank over.
func RouteHealthCurrentCells(grid []ChannelHealthLaneCell) map[string]bool {
	cells := make(map[string]bool, len(grid))
	for _, cell := range grid {
		if NormalizeAbilityRole(cell.Role) == AbilityRoleInventory {
			continue
		}
		cells[ChannelHealthCellKey(cell.ModelName, cell.Lane, cell.ChannelId)] = true
	}
	return cells
}

// Percentiles ranks each lane's latency figures over its population. Total
// duration is kept apart by kind (a probe's reply is short by design); first
// token and the longest pause pool traffic and probes. Superseded attempts
// stay out: the lane describes the answers customers got.
func (w RouteHealthWindow) Percentiles(population RouteHealthPopulation) []ChannelHealthPercentileRow {
	type laneHists struct {
		trafficDuration, probeDuration, ttft, stall routeHealthHistogram
	}
	excluded := make(map[string]bool, len(population.Excluded))
	for _, route := range population.Excluded {
		excluded[ChannelHealthCellKey(route.ModelName, route.Lane, route.ChannelId)] = true
	}
	var public map[string]bool
	if population.PublicModels != nil {
		public = make(map[string]bool, len(population.PublicModels))
		for _, name := range population.PublicModels {
			public[name] = true
		}
	}
	lanes := map[string]*laneHists{}
	order := []string{}
	names := map[string][2]string{}
	for _, row := range w.Rows {
		if row.Kind == RouteHealthKindAttempt {
			continue
		}
		cell := ChannelHealthCellKey(row.ModelName, row.Lane, row.ChannelID)
		if population.Current != nil && !population.Current[cell] {
			continue
		}
		if excluded[cell] {
			continue
		}
		name := row.ModelName
		if canonical, ok := population.Aliases[name]; ok && canonical != "" {
			name = canonical
		}
		lane := row.Lane
		if public != nil {
			if !public[name] {
				continue
			}
			if lane != ChannelLaneAgent {
				lane = ChannelLaneDirect
			}
		}
		key := ChannelHealthLaneKey(name, lane)
		hists := lanes[key]
		if hists == nil {
			hists = &laneHists{}
			lanes[key] = hists
			order = append(order, key)
			names[key] = [2]string{name, lane}
		}
		duration := decodeRouteHealthHistogram(row.DurationHist)
		if row.Kind == ChannelHealthKindProbe {
			hists.probeDuration.merge(&duration)
		} else {
			hists.trafficDuration.merge(&duration)
		}
		ttft := decodeRouteHealthHistogram(row.TTFTHist)
		hists.ttft.merge(&ttft)
		stall := decodeRouteHealthHistogram(row.StallHist)
		hists.stall.merge(&stall)
	}
	sort.Strings(order)
	result := make([]ChannelHealthPercentileRow, 0, len(order))
	for _, key := range order {
		hists := lanes[key]
		result = append(result, ChannelHealthPercentileRow{
			ModelName:         names[key][0],
			Lane:              names[key][1],
			P95LatencyMs:      hists.trafficDuration.quantile(0.95),
			ProbeP95LatencyMs: hists.probeDuration.quantile(0.95),
			TTFTP50Ms:         hists.ttft.quantile(0.50),
			TTFTP95Ms:         hists.ttft.quantile(0.95),
			P50LatencyMs:      hists.trafficDuration.quantile(0.50),
			ProbeP50LatencyMs: hists.probeDuration.quantile(0.50),
			StallP50Ms:        hists.stall.quantile(0.50),
		})
	}
	return result
}

// ChannelPercentiles ranks each upstream's own customer request durations,
// superseded attempts included, across every lane it serves. enabled limits
// the result to channels serving now; nil keeps every channel.
func (w RouteHealthWindow) ChannelPercentiles(enabled map[int]bool) []ChannelHealthChannelPercentileRow {
	hists := map[int]*routeHealthHistogram{}
	for _, row := range w.Rows {
		if row.Kind == ChannelHealthKindProbe || row.ChannelID <= 0 {
			continue
		}
		if enabled != nil && !enabled[row.ChannelID] {
			continue
		}
		h := hists[row.ChannelID]
		if h == nil {
			h = &routeHealthHistogram{}
			hists[row.ChannelID] = h
		}
		duration := decodeRouteHealthHistogram(row.DurationHist)
		h.merge(&duration)
	}
	result := make([]ChannelHealthChannelPercentileRow, 0, len(hists))
	for id, h := range hists {
		if h.total() == 0 {
			continue
		}
		result = append(result, ChannelHealthChannelPercentileRow{ChannelId: id, P95LatencyMs: h.quantile(0.95)})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ChannelId < result[j].ChannelId })
	return result
}

// LastOutcomes is the newest customer evidence per route, superseded
// attempts included, for the probe scheduler.
func (w RouteHealthWindow) LastOutcomes() []ChannelHealthLastOutcomeRow {
	latest := map[string]*ChannelHealthLastOutcomeRow{}
	for _, row := range w.Rows {
		if row.Kind == ChannelHealthKindProbe || row.LastAtMs <= 0 {
			continue
		}
		key := ChannelHealthCellKey(row.ModelName, row.Lane, row.ChannelID)
		current := latest[key]
		if current == nil {
			current = &ChannelHealthLastOutcomeRow{ModelName: row.ModelName, Lane: row.Lane, ChannelId: row.ChannelID}
			latest[key] = current
		}
		if !current.LastOutcomeAt.Valid || row.LastAtMs > current.LastOutcomeAt.Time.UnixMilli() {
			current.LastOutcomeAt = ChannelHealthTime{Time: time.UnixMilli(row.LastAtMs).UTC(), Valid: true}
		}
	}
	result := make([]ChannelHealthLastOutcomeRow, 0, len(latest))
	for _, row := range latest {
		result = append(result, *row)
	}
	sort.Slice(result, func(i, j int) bool {
		return ChannelHealthCellKey(result[i].ModelName, result[i].Lane, result[i].ChannelId) <
			ChannelHealthCellKey(result[j].ModelName, result[j].Lane, result[j].ChannelId)
	})
	return result
}

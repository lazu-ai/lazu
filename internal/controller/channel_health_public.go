package controller

import (
	"sort"
	"time"

	"github.com/lazu-ai/lazu/internal/model"
)

type channelHealthAggregateTotals struct {
	DurationSumMs        int64 `json:"duration_sum_ms"`
	DurationCount        int64 `json:"duration_count"`
	TrafficDurationCount int64 `json:"traffic_duration_count"`
	Interrupted          int64 `json:"interrupted"`
	Streamed             int64 `json:"streamed"`
	// Already ranked over the complete public-lane sample population while
	// building the snapshot. It is not a percentile of route percentiles.
	PublicPercentiles *model.ChannelHealthPercentileRow `json:"public_percentiles,omitempty"`
}

// attachPublicChannelHealthPercentiles ranks lanes that share a public name
// over their pooled population, never a percentile of route percentiles.
func attachPublicChannelHealthPercentiles(rows []channelHealthLaneView, health model.RouteHealthWindow, population model.RouteHealthPopulation) {
	type publicLane struct{ model, lane string }
	counts := map[publicLane]int{}
	models := map[string]bool{}
	for _, row := range rows {
		if row.Kind == channelHealthKindSearch {
			continue
		}
		key := publicLane{row.ModelName, row.Lane}
		counts[key]++
		if counts[key] > 1 {
			models[row.ModelName] = true
		}
	}
	if len(models) == 0 {
		return
	}
	names := make([]string, 0, len(models))
	for name := range models {
		names = append(names, name)
	}
	sort.Strings(names)
	population.PublicModels = names
	percentiles := health.Percentiles(population)
	byLane := make(map[publicLane]*model.ChannelHealthPercentileRow, len(percentiles))
	for i := range percentiles {
		row := &percentiles[i]
		byLane[publicLane{row.ModelName, catalogLaneKey(row.Lane)}] = row
	}
	for i := range rows {
		row := &rows[i]
		key := publicLane{row.ModelName, row.Lane}
		if counts[key] > 1 && row.AggregateTotals != nil {
			row.AggregateTotals.PublicPercentiles = byLane[key]
		}
	}
}

// mergePublicChannelHealthRows runs after access filtering but before source
// redaction. Raw route names are deliberately absent from the grouping key.
func mergePublicChannelHealthRows(rows []channelHealthLaneView) []channelHealthLaneView {
	type publicLane struct{ kind, model, lane, provider string }
	groups := map[publicLane][]channelHealthLaneView{}
	order := make([]publicLane, 0, len(rows))
	for _, row := range rows {
		kind := row.Kind
		if kind == "" {
			kind = channelHealthKindLLM
		}
		key := publicLane{kind, row.ModelName, row.Lane, row.Provider}
		if _, ok := groups[key]; !ok {
			order = append(order, key)
		}
		groups[key] = append(groups[key], row)
	}
	result := make([]channelHealthLaneView, 0, len(order))
	for _, key := range order {
		group := groups[key]
		if len(group) == 1 {
			result = append(result, group[0])
			continue
		}
		result = append(result, mergePublicChannelHealthLane(group))
	}
	return result
}

func mergePublicChannelHealthLane(rows []channelHealthLaneView) channelHealthLaneView {
	// Start with identity only: no arbitrary route's price, percentile or
	// operator metadata can become the apparent public lane's figure.
	merged := channelHealthLaneView{
		ModelName: rows[0].ModelName, Kind: rows[0].Kind,
		Lane: rows[0].Lane, Provider: rows[0].Provider,
		Source: channelHealthSourceNone,
	}
	statuses := make([]channelHealthSourceView, 0, len(rows))
	type sliceKey struct {
		index int
		start time.Time
	}
	buckets := map[sliceKey]*channelHealthBucketView{}
	totals := channelHealthAggregateTotals{}
	completeTotals := true
	var pooled *model.ChannelHealthPercentileRow
	var sampled []channelHealthLaneView
	for _, row := range rows {
		statuses = append(statuses, channelHealthSourceView{Status: row.Status})
		if channelHealthRowHasSamples(row) {
			sampled = append(sampled, row)
			if row.AggregateTotals == nil {
				// Older materialized snapshots lack the denominators. Wait for
				// their normal refresh rather than inventing a weighted ratio.
				completeTotals = false
			} else {
				totals.DurationSumMs += row.AggregateTotals.DurationSumMs
				totals.DurationCount += row.AggregateTotals.DurationCount
				totals.TrafficDurationCount += row.AggregateTotals.TrafficDurationCount
				totals.Interrupted += row.AggregateTotals.Interrupted
				totals.Streamed += row.AggregateTotals.Streamed
				if row.AggregateTotals.PublicPercentiles != nil {
					pooled = row.AggregateTotals.PublicPercentiles
				}
			}
		}
		if channelHealthEvidenceStrength(row.Source) > channelHealthEvidenceStrength(merged.Source) {
			merged.Source = row.Source
		}
		merged.RequestCount += row.RequestCount
		merged.ErrorCount += row.ErrorCount
		merged.ProbeCount += row.ProbeCount
		merged.ProbeErrorCount += row.ProbeErrorCount
		merged.OutputTokens += row.OutputTokens
		merged.DecodeMs += row.DecodeMs
		merged.LastOutcomeAt = laterChannelHealthTime(merged.LastOutcomeAt, row.LastOutcomeAt)
		merged.LastProbedAt = laterChannelHealthTime(merged.LastProbedAt, row.LastProbedAt)
		for _, bucket := range row.Buckets {
			key := sliceKey{bucket.Index, bucket.StartAt.UTC()}
			view := buckets[key]
			if view == nil {
				view = &channelHealthBucketView{Index: bucket.Index, StartAt: bucket.StartAt, Source: channelHealthSourceNone, Tone: "none"}
				buckets[key] = view
			}
			// A lane's history is its worst measured live source, not a sum
			// of the winning sources' counts or an averaged color. Reuse the
			// same verdict ordering as the original lane fold.
			mergeChannelHealthBucketVerdict(view, channelHealthCellVerdict{
				source: bucket.Source, requestCount: bucket.RequestCount,
				errorCount: bucket.ErrorCount, rate: bucket.SuccessRate, tone: bucket.Tone,
			}, 0)
		}
	}
	merged.Status = channelHealthLaneStatus(statuses)
	merged.SuccessRate = successRate(merged.RequestCount+merged.ProbeCount, merged.ErrorCount+merged.ProbeErrorCount)
	if merged.DecodeMs > 0 && merged.OutputTokens > 0 {
		rate := float64(merged.OutputTokens) * 1000 / float64(merged.DecodeMs)
		merged.OutputTPS = &rate
	}
	if completeTotals {
		if totals.DurationCount > 0 {
			mean := float64(totals.DurationSumMs) / float64(totals.DurationCount)
			merged.AvgLatencyMs = &mean
		}
		if totals.Streamed > 0 {
			rate := float64(totals.Interrupted) / float64(totals.Streamed)
			merged.InterruptionRate = &rate
		}
	}
	// A single contributing route needs no pooling. Multi-route distributions
	// were ranked together from original events at snapshot generation time.
	if len(sampled) == 1 {
		row := sampled[0]
		merged.AvgLatencyMs, merged.P95LatencyMs = row.AvgLatencyMs, row.P95LatencyMs
		merged.TTFTP50Ms, merged.TTFTP95Ms = row.TTFTP50Ms, row.TTFTP95Ms
		merged.LatencyP50Ms, merged.LatencyP95Ms = row.LatencyP50Ms, row.LatencyP95Ms
		merged.StallP50Ms, merged.InterruptionRate = row.StallP50Ms, row.InterruptionRate
	}
	if pooled != nil && completeTotals {
		merged.TTFTP50Ms = positiveChannelHealthFigure(pooled.TTFTP50Ms)
		merged.TTFTP95Ms = positiveChannelHealthFigure(pooled.TTFTP95Ms)
		merged.StallP50Ms = positiveChannelHealthFigure(pooled.StallP50Ms)
		p95 := pooled.P95LatencyMs
		if totals.TrafficDurationCount == 0 {
			p95 = pooled.ProbeP95LatencyMs
		}
		merged.P95LatencyMs = positiveChannelHealthFigure(p95)
		p50, p95 := channelHealthLatencyPair(totals.TrafficDurationCount,
			pooled.P50LatencyMs, pooled.P95LatencyMs, pooled.ProbeP50LatencyMs, pooled.ProbeP95LatencyMs)
		merged.LatencyP50Ms = positiveChannelHealthFigure(p50)
		merged.LatencyP95Ms = positiveChannelHealthFigure(p95)
	}
	for _, bucket := range buckets {
		merged.Buckets = append(merged.Buckets, *bucket)
	}
	sort.Slice(merged.Buckets, func(i, j int) bool {
		left, right := merged.Buckets[i], merged.Buckets[j]
		if !left.StartAt.Equal(right.StartAt) {
			return left.StartAt.Before(right.StartAt)
		}
		return left.Index < right.Index
	})
	return merged
}

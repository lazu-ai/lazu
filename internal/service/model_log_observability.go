package service

import (
	"fmt"
	"math"
	"sort"
)

type RelayObservabilityStats struct {
	RelayFormat             string  `json:"relay_format"`
	ProviderFamily          string  `json:"provider_family"`
	ProviderAPIType         int     `json:"provider_api_type"`
	TotalRequests           int     `json:"total_requests"`
	SuccessRequests         int     `json:"success_requests"`
	SuccessRate             float64 `json:"success_rate"`
	P95DurationMs           int64   `json:"p95_duration_ms"`
	StreamInterruptedRate   float64 `json:"stream_interrupted_rate"`
	FallbackHitRate         float64 `json:"fallback_hit_rate"`
	AverageDurationMs       float64 `json:"average_duration_ms"`
	AverageFirstResponseMs  float64 `json:"average_first_response_latency_ms"`
	ObservedStreamRequests  int     `json:"observed_stream_requests"`
	ObservedFallbackRequest int     `json:"observed_fallback_requests"`
}

type relayObservabilityAccumulator struct {
	total                    int
	success                  int
	durations                []int64
	streamRequests           int
	streamInterrupted        int
	fallbackRequests         int
	firstResponseLatencySum  int64
	firstResponseLatencySeen int
}

func SummarizeRelayObservability(entries []ModelLogEntry) []RelayObservabilityStats {
	accumulators := make(map[string]*relayObservabilityAccumulator)
	keys := make(map[string]RelayObservabilityStats)

	for _, entry := range entries {
		key := fmt.Sprintf("%s|%s|%d", entry.RelayFormat, entry.ProviderFamily, entry.ProviderAPIType)
		if _, ok := accumulators[key]; !ok {
			accumulators[key] = &relayObservabilityAccumulator{
				durations: make([]int64, 0, 8),
			}
			keys[key] = RelayObservabilityStats{
				RelayFormat:     entry.RelayFormat,
				ProviderFamily:  entry.ProviderFamily,
				ProviderAPIType: entry.ProviderAPIType,
			}
		}

		acc := accumulators[key]
		acc.total++
		if entry.DurationMs > 0 {
			acc.durations = append(acc.durations, entry.DurationMs)
		}
		if entry.FirstResponseLatency > 0 {
			acc.firstResponseLatencySum += entry.FirstResponseLatency
			acc.firstResponseLatencySeen++
		}

		success := entry.StatusCode >= 200 && entry.StatusCode < 300 && entry.Error == ""
		if success {
			acc.success++
		}

		if entry.Stream {
			acc.streamRequests++
			interrupted := entry.StreamInterrupted || !success
			if interrupted {
				acc.streamInterrupted++
			}
		}

		if entry.FallbackHit {
			acc.fallbackRequests++
		}
	}

	result := make([]RelayObservabilityStats, 0, len(accumulators))
	for key, acc := range accumulators {
		stat := keys[key]
		stat.TotalRequests = acc.total
		stat.SuccessRequests = acc.success
		if acc.total > 0 {
			stat.SuccessRate = float64(acc.success) / float64(acc.total)
			stat.FallbackHitRate = float64(acc.fallbackRequests) / float64(acc.total)
		}
		if acc.streamRequests > 0 {
			stat.StreamInterruptedRate = float64(acc.streamInterrupted) / float64(acc.streamRequests)
		}
		if len(acc.durations) > 0 {
			stat.P95DurationMs = percentile95(acc.durations)
			stat.AverageDurationMs = averageInt64(acc.durations)
		}
		if acc.firstResponseLatencySeen > 0 {
			stat.AverageFirstResponseMs = float64(acc.firstResponseLatencySum) / float64(acc.firstResponseLatencySeen)
		}
		stat.ObservedStreamRequests = acc.streamRequests
		stat.ObservedFallbackRequest = acc.fallbackRequests
		result = append(result, stat)
	}

	sort.Slice(result, func(i, j int) bool {
		if result[i].RelayFormat != result[j].RelayFormat {
			return result[i].RelayFormat < result[j].RelayFormat
		}
		if result[i].ProviderFamily != result[j].ProviderFamily {
			return result[i].ProviderFamily < result[j].ProviderFamily
		}
		return result[i].ProviderAPIType < result[j].ProviderAPIType
	})
	return result
}

func percentile95(values []int64) int64 {
	if len(values) == 0 {
		return 0
	}
	data := make([]int64, len(values))
	copy(data, values)
	sort.Slice(data, func(i, j int) bool { return data[i] < data[j] })
	index := int(math.Ceil(float64(len(data))*0.95)) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(data) {
		index = len(data) - 1
	}
	return data[index]
}

func averageInt64(values []int64) float64 {
	if len(values) == 0 {
		return 0
	}
	var sum int64
	for _, value := range values {
		sum += value
	}
	return float64(sum) / float64(len(values))
}

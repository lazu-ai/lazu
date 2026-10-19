package service

import "testing"

func TestSummarizeRelayObservability(t *testing.T) {
	entries := []ModelLogEntry{
		{
			RelayFormat:          "openai",
			ProviderFamily:       "openai",
			ProviderAPIType:      0,
			StatusCode:           200,
			DurationMs:           100,
			FirstResponseLatency: 30,
			Stream:               true,
			StreamInterrupted:    false,
			FallbackHit:          false,
		},
		{
			RelayFormat:          "openai",
			ProviderFamily:       "openai",
			ProviderAPIType:      0,
			StatusCode:           500,
			DurationMs:           300,
			FirstResponseLatency: 60,
			Stream:               true,
			StreamInterrupted:    true,
			FallbackHit:          true,
			Error:                "upstream failed",
		},
		{
			RelayFormat:          "openai",
			ProviderFamily:       "openai",
			ProviderAPIType:      0,
			StatusCode:           200,
			DurationMs:           200,
			FirstResponseLatency: 40,
			Stream:               false,
			FallbackHit:          false,
		},
		{
			RelayFormat:          "claude",
			ProviderFamily:       "claude",
			ProviderAPIType:      2,
			StatusCode:           200,
			DurationMs:           50,
			FirstResponseLatency: 15,
			Stream:               true,
		},
	}

	stats := SummarizeRelayObservability(entries)
	if len(stats) != 2 {
		t.Fatalf("expected 2 grouped stats, got %d", len(stats))
	}

	openai := stats[1]
	if openai.RelayFormat != "openai" || openai.ProviderFamily != "openai" {
		t.Fatalf("unexpected openai key: %+v", openai)
	}
	if openai.TotalRequests != 3 {
		t.Fatalf("openai total requests mismatch: %d", openai.TotalRequests)
	}
	if openai.SuccessRequests != 2 {
		t.Fatalf("openai success requests mismatch: %d", openai.SuccessRequests)
	}
	if openai.SuccessRate != 2.0/3.0 {
		t.Fatalf("openai success rate mismatch: %f", openai.SuccessRate)
	}
	if openai.P95DurationMs != 300 {
		t.Fatalf("openai p95 mismatch: %d", openai.P95DurationMs)
	}
	if openai.ObservedStreamRequests != 2 {
		t.Fatalf("openai stream count mismatch: %d", openai.ObservedStreamRequests)
	}
	if openai.StreamInterruptedRate != 0.5 {
		t.Fatalf("openai stream interrupted rate mismatch: %f", openai.StreamInterruptedRate)
	}
	if openai.ObservedFallbackRequest != 1 {
		t.Fatalf("openai fallback count mismatch: %d", openai.ObservedFallbackRequest)
	}
	if openai.FallbackHitRate != 1.0/3.0 {
		t.Fatalf("openai fallback rate mismatch: %f", openai.FallbackHitRate)
	}
	if openai.AverageFirstResponseMs <= 0 {
		t.Fatalf("openai first response latency should be >0, got %f", openai.AverageFirstResponseMs)
	}
}

func TestPercentile95(t *testing.T) {
	values := []int64{10, 20, 30, 40, 50, 60, 70, 80, 90, 100}
	if got := percentile95(values); got != 100 {
		t.Fatalf("unexpected p95 value: %d", got)
	}
}

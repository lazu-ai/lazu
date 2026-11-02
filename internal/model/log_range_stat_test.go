package model

import "testing"

func TestAdminLogsRangeStatCoversTheRange(t *testing.T) {
	_, logDB := newLedgerTestDBs(t)
	streamed, buffered := true, false
	ttft := func(ms int) *int { return &ms }
	cached := func(n int) *int { return &n }
	rows := []Log{
		// The window before [7200, 14399].
		{UserId: 5, Type: LogTypeConsume, CreatedAt: 3600, AmountMicrousd: 40},
		{UserId: 5, Type: LogTypeConsume, CreatedAt: 7200, AmountMicrousd: 100, PromptTokens: 1000,
			RoutingOutcome: LogRoutingOutcome{WasStreaming: &streamed, TTFTMs: ttft(800), CachedInputTokens: cached(250)}},
		{UserId: 5, Type: LogTypeConsume, CreatedAt: 7300, AmountMicrousd: 50, PromptTokens: 1000,
			RoutingOutcome: LogRoutingOutcome{WasStreaming: &streamed, TTFTMs: ttft(1200), CachedInputTokens: cached(250)}},
		{UserId: 5, Type: LogTypeConsume, CreatedAt: 10900, AmountMicrousd: 30,
			RoutingOutcome: LogRoutingOutcome{WasStreaming: &buffered, TTFTMs: ttft(9000)}},
		{UserId: 5, Type: LogTypeError, CreatedAt: 11000},
		{UserId: 6, Type: LogTypeConsume, CreatedAt: 7200, AmountMicrousd: 999},
	}
	if err := logDB.Create(&rows).Error; err != nil {
		t.Fatalf("seed logs: %v", err)
	}

	stat, err := AdminLogsRangeStat(AdminLogFilter{UserID: 5, Start: 7200, End: 14399})
	if err != nil {
		t.Fatalf("AdminLogsRangeStat: %v", err)
	}
	if stat.AmountMicrousd != 180 || stat.RequestCount != 3 || stat.ErrorCount != 1 {
		t.Fatalf("totals = %d / %d requests / %d errors, want 180 / 3 / 1", stat.AmountMicrousd, stat.RequestCount, stat.ErrorCount)
	}
	if stat.PreviousAmountMicrousd == nil || *stat.PreviousAmountMicrousd != 40 {
		t.Fatalf("previous = %v, want 40", stat.PreviousAmountMicrousd)
	}
	if stat.CacheHitRatio != 0.25 {
		t.Fatalf("cache hit = %v, want 0.25", stat.CacheHitRatio)
	}
	if stat.MedianTTFTMs == nil || *stat.MedianTTFTMs != 1000 {
		t.Fatalf("median ttft = %v, want 1000 (buffered requests left out)", stat.MedianTTFTMs)
	}
	if len(stat.Series) != 2 || stat.Series[0].Bucket != 7200 || stat.Series[0].AmountMicrousd != 150 || stat.Series[1].Bucket != 10800 {
		t.Fatalf("series = %#v, want hour buckets 7200 (150) and 10800", stat.Series)
	}
}

package model

import (
	"errors"
	"math"
	"testing"

	"github.com/lazu-ai/lazu/pkg/billing"
)

func TestUsageDataCacheOverflowIsAtomic(t *testing.T) {
	CacheUsageDataLock.Lock()
	defer CacheUsageDataLock.Unlock()
	previous := CacheUsageData
	defer func() { CacheUsageData = previous }()
	for _, test := range []struct {
		name string
		row  UsageData
	}{
		{"count", UsageData{Count: int(^uint(0) >> 1), TokenUsed: 2, AmountMicrousd: 3}},
		{"tokens", UsageData{Count: 2, TokenUsed: int(^uint(0) >> 1), AmountMicrousd: 3}},
		{"money", UsageData{Count: 2, TokenUsed: 3, AmountMicrousd: billing.MicroUSD(math.MaxInt64)}},
	} {
		t.Run(test.name, func(t *testing.T) {
			original := test.row
			CacheUsageData = map[string]*UsageData{"1-test-model-0": &original}
			if err := logUsageDataCache(1, "test", "model", 1, 0, 1); !errors.Is(err, errUsageDataCacheRange) {
				t.Fatalf("expected range error, got %v", err)
			}
			if got := *CacheUsageData["1-test-model-0"]; got != test.row {
				t.Fatalf("partial cache mutation after rejection: got %+v, want %+v", got, test.row)
			}
		})
	}
}

func TestUsageDataCacheSignedMoneyAndTokenBoundary(t *testing.T) {
	CacheUsageDataLock.Lock()
	defer CacheUsageDataLock.Unlock()
	previous := CacheUsageData
	defer func() { CacheUsageData = previous }()
	CacheUsageData = make(map[string]*UsageData)
	if err := logUsageDataCache(1, "test", "model", 10, 0, int(^uint(0)>>1)); err != nil {
		t.Fatal(err)
	}
	if err := logUsageDataCache(1, "test", "model", -4, 0, 0); err != nil {
		t.Fatal(err)
	}
	got := CacheUsageData["1-test-model-0"]
	if got.Count != 2 || got.TokenUsed != int(^uint(0)>>1) || got.AmountMicrousd != 6 {
		t.Fatalf("valid signed/boundary values changed: %+v", got)
	}
	if err := logUsageDataCache(1, "test", "model", 0, 0, -1); !errors.Is(err, errUsageDataCacheRange) {
		t.Fatalf("negative tokens accepted: %v", err)
	}
}

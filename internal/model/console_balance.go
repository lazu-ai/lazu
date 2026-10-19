package model

import (
	"context"
	"time"

	"github.com/lazu-ai/lazu/pkg/billing"
)

// ReadConsoleWalletBalances overlays already loaded committed values with a
// bounded, read-only Hot snapshot. This projection is exclusively for display:
// financial baselines, mutations and authentication still read Core normally.
func ReadConsoleWalletBalances(ctx context.Context, committed map[int]billing.MicroUSD) map[int]billing.MicroUSD {
	result := make(map[int]billing.MicroUSD, len(committed))
	ids := make([]int, 0, len(committed))
	for id, amount := range committed {
		result[id] = amount
		ids = append(ids, id)
	}
	engine := GatewayLedger()
	if engine == nil || len(ids) == 0 {
		return result
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
	defer cancel()
	balances, err := engine.ReadBalances(ctx, ids)
	if err != nil {
		return result
	}
	for id, amount := range balances {
		result[id] = billing.MicroUSD(amount)
	}
	return result
}

func ReadConsoleWalletBalance(ctx context.Context, payerID int, committed billing.MicroUSD) billing.MicroUSD {
	return ReadConsoleWalletBalances(ctx, map[int]billing.MicroUSD{payerID: committed})[payerID]
}

// ReadConsoleTokenRemaining overlays only limited-Key display counters. The
// authorized Core Token objects and their historical grant/used fields remain
// unchanged; unlimited Keys retain their established Core display semantics.
func ReadConsoleTokenRemaining(ctx context.Context, tokens []*Token) map[int]billing.MicroUSD {
	result := make(map[int]billing.MicroUSD, len(tokens))
	ids := make([]int, 0, len(tokens))
	for _, token := range tokens {
		if token == nil {
			continue
		}
		result[token.Id] = token.RemainQuota
		if !token.UnlimitedQuota && token.Id > 0 {
			ids = append(ids, token.Id)
		}
	}
	engine := GatewayLedger()
	if engine == nil || len(ids) == 0 {
		return result
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
	defer cancel()
	remaining, err := engine.ReadTokenRemaining(ctx, ids)
	if err != nil {
		return result
	}
	for id, amount := range remaining {
		result[id] = billing.MicroUSD(amount)
	}
	return result
}

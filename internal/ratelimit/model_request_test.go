package ratelimit

import (
	"context"
	"testing"

	"github.com/lazu-ai/lazu/internal/common"
)

func withMemoryLimiter(t *testing.T) *Service {
	t.Helper()
	previousRedis := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() {
		common.RedisEnabled = previousRedis
	})
	return NewService()
}

func TestMemoryRequestPreflightDoesNotDrainRPMWhenSuccessRejects(t *testing.T) {
	service := withMemoryLimiter(t)
	ctx := context.Background()
	input := RequestPreflightInput{
		UserID:                 10,
		TokenID:                20,
		TokenRateLimitsEnabled: true,
		TokenRPMLimit:          1,
		AccountRPMLimit:        1,
		SuccessRequestLimit:    1,
		DurationSeconds:        60,
		WindowMinutes:          1,
	}

	if err := service.RecordSuccess(ctx, input); err != nil {
		t.Fatalf("record success failed: %v", err)
	}
	rejected, err := service.PreflightRequest(ctx, input)
	if err != nil {
		t.Fatalf("preflight failed: %v", err)
	}
	if rejected.Decision != DecisionSuccessRPM {
		t.Fatalf("expected success limiter rejection, got %#v", rejected)
	}

	input.SuccessRequestLimit = 0
	allowed, err := service.PreflightRequest(ctx, input)
	if err != nil {
		t.Fatalf("second preflight failed: %v", err)
	}
	if !allowed.Allowed() {
		t.Fatalf("success rejection must not consume token/account RPM, got %#v", allowed)
	}
}

func TestMemoryRequestPreflightRecordsOnlyAllowedRPM(t *testing.T) {
	service := withMemoryLimiter(t)
	ctx := context.Background()
	input := RequestPreflightInput{
		UserID:                 11,
		TokenID:                21,
		TokenRateLimitsEnabled: true,
		TokenRPMLimit:          1,
		AccountRPMLimit:        100,
		DurationSeconds:        60,
		WindowMinutes:          1,
	}

	first, err := service.PreflightRequest(ctx, input)
	if err != nil {
		t.Fatalf("first preflight failed: %v", err)
	}
	if !first.Allowed() {
		t.Fatalf("first request should pass, got %#v", first)
	}
	second, err := service.PreflightRequest(ctx, input)
	if err != nil {
		t.Fatalf("second preflight failed: %v", err)
	}
	if second.Decision != DecisionTokenRPM {
		t.Fatalf("expected token RPM rejection, got %#v", second)
	}
}

func TestMemoryTPMDoesNotDrainTokenWhenAccountRejects(t *testing.T) {
	service := withMemoryLimiter(t)
	ctx := context.Background()
	input := TokenPreflightInput{
		UserID:                 12,
		TokenID:                22,
		TokenRateLimitsEnabled: true,
		TokenTPMLimit:          10_000,
		AccountTPMLimit:        5_000,
		Tokens:                 6_000,
		DurationSeconds:        60,
		WindowMinutes:          1,
	}

	rejected, err := service.CheckAndRecordTPM(ctx, input)
	if err != nil {
		t.Fatalf("tpm check failed: %v", err)
	}
	if rejected.Decision != DecisionAccountTPM {
		t.Fatalf("expected account TPM rejection, got %#v", rejected)
	}

	input.AccountTPMLimit = 100_000
	input.Tokens = 9_000
	allowed, err := service.CheckAndRecordTPM(ctx, input)
	if err != nil {
		t.Fatalf("second tpm check failed: %v", err)
	}
	if !allowed.Allowed() {
		t.Fatalf("account rejection must not consume token TPM, got %#v", allowed)
	}
}

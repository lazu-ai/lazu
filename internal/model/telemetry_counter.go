package model

import (
	"encoding/json"
	"errors"
	"math"

	usagepkg "github.com/lazu-ai/lazu/internal/usage"
)

var ErrTelemetryCounterRange = errors.New("telemetry counter is negative or exceeds signed 64-bit range")

type telemetryDelta struct {
	target *int64
	value  int64
}

// Validate every delta before changing the accumulator. Monetary deltas may
// be signed, including refunds and negative margins; counters may not be.
func addTelemetryCounters(deltas ...telemetryDelta) error {
	for _, delta := range deltas {
		left, right := *delta.target, delta.value
		if right > 0 && left > math.MaxInt64-right || right < 0 && left < math.MinInt64-right {
			return ErrTelemetryCounterRange
		}
	}
	for _, delta := range deltas {
		*delta.target += delta.value
	}
	return nil
}

func telemetryNonnegative(values ...int64) error {
	for _, value := range values {
		if value < 0 {
			return ErrTelemetryCounterRange
		}
	}
	return nil
}

func checkedTelemetryTokenTotal(prompt, completion int) (int64, error) {
	total, err := checkedTelemetryInt64Total(int64(prompt), int64(completion))
	if err != nil || total > int64(^uint(0)>>1) {
		return 0, ErrTelemetryCounterRange
	}
	return total, nil
}

func checkedTelemetryInt64Total(prompt, completion int64) (int64, error) {
	if err := telemetryNonnegative(prompt, completion); err != nil {
		return 0, err
	}
	if prompt > math.MaxInt64-completion {
		return 0, ErrTelemetryCounterRange
	}
	return prompt + completion, nil
}

func telemetryDimensionQuantity(fact usagepkg.Fact, name string) (int64, error) {
	for _, dimension := range fact.Dimensions {
		if dimension.Name == name {
			if dimension.Quantity < 0 {
				return 0, ErrTelemetryCounterRange
			}
			return dimension.Quantity, nil
		}
	}
	return 0, nil
}

func validateTelemetryLogNumbers(row *Log) error {
	if _, err := checkedTelemetryTokenTotal(row.PromptTokens, row.CompletionTokens); err != nil {
		return err
	}
	return validateTelemetryUsageFactNumbers(row.UsageFact)
}

func validateTelemetryUsageFactNumbers(raw string) error {
	// Inspect the raw counters before ParseFact.Normalize can add native ints.
	// Keep historical unparsable metadata's existing no-fact behavior.
	var fact usagepkg.Fact
	if err := json.Unmarshal([]byte(raw), &fact); err != nil {
		return nil
	}
	if _, err := checkedTelemetryTokenTotal(fact.PromptTokens, fact.CompletionTokens); err != nil {
		return err
	}
	if _, err := checkedTelemetryTokenTotal(fact.InputTokens, fact.OutputTokens); err != nil {
		return err
	}
	if err := telemetryNonnegative(int64(fact.TotalTokens)); err != nil {
		return err
	}
	for _, dimension := range fact.Dimensions {
		if dimension.Quantity < 0 {
			return ErrTelemetryCounterRange
		}
	}
	if fact.MediaUnits != nil {
		return telemetryNonnegative(fact.MediaUnits.ImageCount, fact.MediaUnits.VideoSeconds)
	}
	return nil
}

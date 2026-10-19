package config

import "testing"

type syncTrackingConfig struct {
	Threshold int `json:"threshold"`
	synced    bool
}

func (c *syncTrackingConfig) Sync() {
	c.synced = true
}

func TestUpdateConfigFromMapAcceptsFloatFormattedIntegers(t *testing.T) {
	cfg := &syncTrackingConfig{Threshold: 95}

	err := UpdateConfigFromMap(cfg, map[string]string{
		"threshold": "99.000000",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Threshold != 99 {
		t.Fatalf("expected threshold to be updated to 99, got %d", cfg.Threshold)
	}
	if !cfg.synced {
		t.Fatalf("expected Sync to be called after config update")
	}
}

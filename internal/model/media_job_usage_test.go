package model

import (
	"testing"

	"github.com/lazu-ai/lazu/internal/dto"
)

func TestVideoJobUsageFillsWhatTheProviderLeftOut(t *testing.T) {
	snapshot, err := SnapshotWithVideoFacts(JSONValue([]byte(`{"prompt":"p"}`)), MediaJobProviderFactsEstimateSnapshotKey, dto.VideoFacts{
		Seconds: 5, Resolution: "480p", Ratio: "16:9",
	})
	if err != nil {
		t.Fatalf("estimate: %v", err)
	}
	job := MediaJob{PublicID: "mjob_usage", Kind: MediaJobKindVideo, Status: MediaJobStatusRunning, RequestSnapshot: snapshot}
	if usage := job.ToDTO(false).Usage; usage != nil {
		t.Fatalf("a running job has no usage yet: %+v", usage)
	}

	cost := 0.3883
	snapshot, err = SnapshotWithVideoFacts(snapshot, MediaJobProviderFactsSnapshotKey, dto.VideoFacts{ProviderCostUSD: &cost, Reported: true})
	if err != nil {
		t.Fatalf("reported: %v", err)
	}
	job.RequestSnapshot = snapshot
	job.Status = MediaJobStatusSucceeded
	usage := job.ToDTO(false).Usage
	if usage == nil || usage.Seconds != 5 || usage.Resolution != "480p" || usage.Ratio != "16:9" {
		t.Fatalf("an aggregator that reports only its cost still shows the billed clip: %+v", usage)
	}
}

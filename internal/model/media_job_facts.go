package model

import (
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/dto"
)

// mediaJobSnapshotFacts reads one facts record off the request snapshot.
func mediaJobSnapshotFacts(job MediaJob, key string) (dto.VideoFacts, bool) {
	if len(job.RequestSnapshot) == 0 {
		return dto.VideoFacts{}, false
	}
	var fields map[string]any
	if err := common.Unmarshal(job.RequestSnapshot, &fields); err != nil {
		return dto.VideoFacts{}, false
	}
	raw, ok := fields[key]
	if !ok || raw == nil {
		return dto.VideoFacts{}, false
	}
	encoded, err := common.Marshal(raw)
	if err != nil {
		return dto.VideoFacts{}, false
	}
	var facts dto.VideoFacts
	if err := common.Unmarshal(encoded, &facts); err != nil || facts.IsZero() {
		return dto.VideoFacts{}, false
	}
	return facts, true
}

// MediaJobProviderFacts returns the dimensions to bill a video job on: what
// the provider reported at the terminal state, field by field, over what the
// submission estimated from the outbound request. A dimension the provider
// left out keeps the estimate, so a result without usage cannot price the
// clip at zero tokens.
func MediaJobProviderFacts(job MediaJob) (dto.VideoFacts, bool) {
	estimate, hasEstimate := mediaJobSnapshotFacts(job, MediaJobProviderFactsEstimateSnapshotKey)
	reported, hasReported := mediaJobSnapshotFacts(job, MediaJobProviderFactsSnapshotKey)
	if !hasReported {
		return estimate, hasEstimate
	}
	if !hasEstimate {
		return reported, true
	}
	merged := estimate
	if reported.Seconds > 0 {
		merged.Seconds = reported.Seconds
	}
	if reported.Resolution != "" {
		merged.Resolution = reported.Resolution
	}
	if reported.Ratio != "" {
		merged.Ratio = reported.Ratio
	}
	if reported.FPS > 0 {
		merged.FPS = reported.FPS
	}
	if reported.OutputTokens > 0 {
		merged.OutputTokens = reported.OutputTokens
	}
	if reported.ProviderCostUSD != nil {
		merged.ProviderCostUSD = reported.ProviderCostUSD
	}
	merged.Audio = reported.Audio || estimate.Audio
	merged.Reported = reported.Reported
	return merged, true
}

// MediaJobHasReportedFacts reports whether the provider told us a billing
// dimension of its own (a token count, a cost) rather than echoing ours.
// Settlement re-prices only then; otherwise the reservation is final.
func MediaJobHasReportedFacts(job MediaJob) bool {
	facts, ok := mediaJobSnapshotFacts(job, MediaJobProviderFactsSnapshotKey)
	return ok && facts.Reported
}

// MediaJobVideoFamily returns the upstream family the submission pinned, or "".
func MediaJobVideoFamily(job MediaJob) string {
	return firstStringField(mediaJobRequestFields(job), MediaJobVideoFamilySnapshotKey)
}

// SnapshotWithVideoFamily pins the upstream family on a request snapshot.
func SnapshotWithVideoFamily(snapshot JSONValue, family string) (JSONValue, error) {
	fields := map[string]any{}
	if len(snapshot) > 0 {
		if err := common.Unmarshal(snapshot, &fields); err != nil {
			return snapshot, err
		}
	}
	fields[MediaJobVideoFamilySnapshotKey] = family
	body, err := common.Marshal(fields)
	if err != nil {
		return snapshot, err
	}
	return JSONValue(body), nil
}

// SnapshotWithVideoFacts stores a facts record under key on a request
// snapshot, replacing any earlier record.
func SnapshotWithVideoFacts(snapshot JSONValue, key string, facts dto.VideoFacts) (JSONValue, error) {
	fields := map[string]any{}
	if len(snapshot) > 0 {
		if err := common.Unmarshal(snapshot, &fields); err != nil {
			return snapshot, err
		}
	}
	fields[key] = facts
	body, err := common.Marshal(fields)
	if err != nil {
		return snapshot, err
	}
	return JSONValue(body), nil
}

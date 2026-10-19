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
// the provider reported at the terminal state when it did, else what the
// submission estimated from the outbound request.
func MediaJobProviderFacts(job MediaJob) (dto.VideoFacts, bool) {
	if facts, ok := mediaJobSnapshotFacts(job, MediaJobProviderFactsSnapshotKey); ok {
		return facts, true
	}
	return mediaJobSnapshotFacts(job, MediaJobProviderFactsEstimateSnapshotKey)
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

package model

import (
	"sort"
	"sync"
	"time"
)

// A job's progress is not something the providers report, so it is
// estimated from how long the same model's recent jobs took: elapsed time
// against their median, capped below 100 until the job is really done.

const (
	mediaJobTypicalSample   = 50
	mediaJobTypicalTTL      = 10 * time.Minute
	mediaJobTypicalVideo    = 150 // seconds, before a model has history
	mediaJobTypicalImage    = 30
	mediaJobProgressCeiling = 95
)

type mediaJobTypical struct {
	seconds int
	at      time.Time
}

var (
	mediaJobTypicalMu    sync.Mutex
	mediaJobTypicalCache = map[string]mediaJobTypical{}
)

// MediaJobTypicalSeconds is the median duration, submission to finish, of the
// model's recent successful jobs; a default for its kind without history.
func MediaJobTypicalSeconds(modelName string, kind string, now time.Time) int {
	key := kind + "\x00" + modelName
	mediaJobTypicalMu.Lock()
	cached, ok := mediaJobTypicalCache[key]
	mediaJobTypicalMu.Unlock()
	if ok && now.Sub(cached.at) < mediaJobTypicalTTL {
		return cached.seconds
	}
	seconds := mediaJobTypicalImage
	if kind == MediaJobKindVideo {
		seconds = mediaJobTypicalVideo
	}
	if CoreDB() != nil {
		var rows []MediaJob
		err := CoreDB().Select("created_at", "completed_at").
			Where("model_name = ? AND kind = ? AND status = ? AND completed_at > 0", modelName, kind, MediaJobStatusSucceeded).
			Order("id DESC").Limit(mediaJobTypicalSample).Find(&rows).Error
		if err == nil && len(rows) > 0 {
			durations := make([]int, 0, len(rows))
			for _, row := range rows {
				if d := int(row.CompletedAt - row.CreatedAt.Unix()); d > 0 {
					durations = append(durations, d)
				}
			}
			if len(durations) > 0 {
				sort.Ints(durations)
				seconds = durations[len(durations)/2]
			}
		}
	}
	mediaJobTypicalMu.Lock()
	mediaJobTypicalCache[key] = mediaJobTypical{seconds: seconds, at: now}
	mediaJobTypicalMu.Unlock()
	return seconds
}

// MediaJobProgress estimates an active job's progress (0-95) and the seconds
// it likely has left (0 once it runs longer than usual). ok is false for a
// finished job.
func MediaJobProgress(job MediaJob, now time.Time) (progress int, etaSeconds int, ok bool) {
	if job.Status != MediaJobStatusPending && job.Status != MediaJobStatusRunning {
		return 0, 0, false
	}
	typical := MediaJobTypicalSeconds(job.ModelName, job.Kind, now)
	elapsed := int(now.Sub(job.CreatedAt).Seconds())
	if elapsed < 0 {
		elapsed = 0
	}
	progress = elapsed * 100 / typical
	if progress > mediaJobProgressCeiling {
		progress = mediaJobProgressCeiling
	}
	if progress < 1 {
		progress = 1
	}
	etaSeconds = typical - elapsed
	if etaSeconds < 0 {
		etaSeconds = 0
	}
	return progress, etaSeconds, true
}

func resetMediaJobTypicalCache() {
	mediaJobTypicalMu.Lock()
	mediaJobTypicalCache = map[string]mediaJobTypical{}
	mediaJobTypicalMu.Unlock()
}

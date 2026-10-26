package common

import (
	"sync"
	"time"
)

// Functions only this package's tests call: they reset state between tests
// or wait for background work to finish. Kept out of the production build.

// From hot_path_metrics.go.
func resetHotPathMetricsForTest() {
	hotPathMetricsStartedAt = time.Now().Unix()
	hotPathStageMetrics = sync.Map{}
	hotPathCounters = sync.Map{}
}

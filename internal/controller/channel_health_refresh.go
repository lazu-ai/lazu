package controller

import (
	"sync"

	"github.com/lazu-ai/lazu/internal/common"
)

// A routing mutation rebuilds the health snapshots, which is too slow to do on
// the request path, so it runs in the background. An untracked goroutine there
// outlives the request that started it — in tests, the test itself — and then
// reads package state the next test is already replacing. That is how a live
// query reached a connection that had gone away.
//
// These refreshes are therefore tracked. Production behaviour is unchanged:
// the handler still returns without waiting. A test waits with
// WaitForChannelHealthRefreshes so nothing it started is still running when it
// tears its fixtures down.
var channelHealthRefreshes sync.WaitGroup

// refreshChannelHealthInBackground runs one snapshot refresh off the request
// path. `reason` names the mutation, so a failure says which one it followed.
func refreshChannelHealthInBackground(reason string) {
	channelHealthRefreshes.Add(1)
	go func() {
		defer channelHealthRefreshes.Done()
		if err := RefreshChannelHealthAfterRoutingMutation(); err != nil {
			common.SysLog("channel health: failed to refresh snapshots after " + reason + ": " + err.Error())
		}
	}()
}

// WaitForChannelHealthRefreshes blocks until every refresh started so far has
// finished. It exists for tests; a request never calls it.
func WaitForChannelHealthRefreshes() {
	channelHealthRefreshes.Wait()
}

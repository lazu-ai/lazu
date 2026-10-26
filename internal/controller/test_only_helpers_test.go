package controller

import "context"

// Functions only this package's tests call: they reset state between tests
// or wait for background work to finish. Kept out of the production build.

// From auth_email.go.
// waitForAuthEmailDeliveries keeps test cleanup concise.
func waitForAuthEmailDeliveries() {
	_ = WaitForAuthEmailDeliveries(context.Background())
}

// From channel_health_refresh.go.
// WaitForChannelHealthRefreshes blocks until every refresh started so far has
// finished. It exists for tests; a request never calls it.
func WaitForChannelHealthRefreshes() {
	channelHealthRefreshes.Wait()
}

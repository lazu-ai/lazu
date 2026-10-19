package service

import (
	"context"
	"sync"
)

var tokenShadowWork sync.WaitGroup
var diagnosticWork sync.WaitGroup

// DrainRelayDiagnosticWork waits for queued tokenizer and timing work before
// closing or replacing an embedded telemetry store. Call only after stopping
// request producers, as with other shutdown drains; it never runs on a request
// path and does not stop the process-wide workers.
func DrainRelayDiagnosticWork(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		tokenShadowWork.Wait()
		diagnosticWork.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

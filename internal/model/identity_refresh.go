package model

import (
	"context"
	"errors"
	"sync"
)

var errIdentityRefreshCapacity = errors.New("identity refresh capacity unavailable")

// Flights contain only serialized projections. Authorization and mutable
// snapshot decoding remain local to each waiting request.
type identityRefreshFlight struct {
	done    chan struct{}
	cancel  context.CancelFunc
	waiters int
	raw     string
	err     error
}

type identityRefreshCoordinator struct {
	mu      sync.Mutex
	flights map[string]*identityRefreshFlight
	limit   int
}

func newIdentityRefreshCoordinator(limit int) *identityRefreshCoordinator {
	return &identityRefreshCoordinator{flights: make(map[string]*identityRefreshFlight), limit: limit}
}

var identitySnapshotRefreshes = newIdentityRefreshCoordinator(1024)

func (coordinator *identityRefreshCoordinator) do(ctx context.Context, digest string, load func(context.Context) (string, error)) (string, error) {
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		coordinator.mu.Lock()
		flight := coordinator.flights[digest]
		if flight != nil && flight.waiters == 0 {
			// The last waiter canceled this read. Wait for it to stop before
			// starting a new read, without inheriting the canceled result.
			coordinator.mu.Unlock()
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-flight.done:
				continue
			}
		}
		if flight == nil {
			if len(coordinator.flights) >= coordinator.limit {
				coordinator.mu.Unlock()
				return "", errIdentityRefreshCapacity
			}
			readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), identityTransactionTimeout)
			flight = &identityRefreshFlight{done: make(chan struct{}), cancel: cancel}
			coordinator.flights[digest] = flight
			go coordinator.load(readCtx, digest, flight, load)
		}
		flight.waiters++
		coordinator.mu.Unlock()
		select {
		case <-ctx.Done():
			coordinator.leave(flight)
			return "", ctx.Err()
		case <-flight.done:
			coordinator.leave(flight)
			if err := ctx.Err(); err != nil {
				return "", err
			}
			return flight.raw, flight.err
		}
	}
}

func (coordinator *identityRefreshCoordinator) leave(flight *identityRefreshFlight) {
	coordinator.mu.Lock()
	flight.waiters--
	last := flight.waiters == 0
	coordinator.mu.Unlock()
	if last {
		flight.cancel()
	}
}

func (coordinator *identityRefreshCoordinator) load(ctx context.Context, digest string, flight *identityRefreshFlight, load func(context.Context) (string, error)) {
	var raw string
	var err error
	defer func() {
		if recover() != nil {
			err = errors.New("identity refresh failed unexpectedly")
		}
		coordinator.mu.Lock()
		flight.raw, flight.err = raw, err
		delete(coordinator.flights, digest)
		close(flight.done)
		coordinator.mu.Unlock()
		flight.cancel()
	}()
	raw, err = load(ctx)
}

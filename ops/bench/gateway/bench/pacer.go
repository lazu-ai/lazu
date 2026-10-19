package main

import (
	"context"
	"sync/atomic"
	"time"
)

// Workers claim absolute arrival slots instead of receiving lossy ticker
// notifications. A busy pool may start slots late, but never after the load
// window; offered minus actual starts exposes those missed arrivals.
type arrivalPacer struct {
	start, deadline time.Time
	rate, offered   int64
	next            atomic.Int64
}

func newArrivalPacer(start time.Time, duration time.Duration, rate int) *arrivalPacer {
	r := int64(rate)
	// There is one arrival at start and every 1/rate second strictly before
	// deadline. Split the calculation to avoid duration*rate overflowing.
	n := int64(duration/time.Second)*r + (int64(duration%time.Second)*r+int64(time.Second)-1)/int64(time.Second)
	return &arrivalPacer{start: start, deadline: start.Add(duration), rate: r, offered: n}
}

func arrivalOffset(index, rate int64) time.Duration {
	return time.Duration(index/rate)*time.Second + time.Duration(index%rate)*time.Second/time.Duration(rate)
}

func (p *arrivalPacer) claim() (time.Time, bool) {
	index := p.next.Add(1) - 1
	if index >= p.offered {
		return time.Time{}, false
	}
	return p.start.Add(arrivalOffset(index, p.rate)), true
}

func waitUntil(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func awaitArrival(ctx context.Context, scheduled, deadline time.Time, now func() time.Time, wait func(context.Context, time.Duration) bool) bool {
	current := now()
	if ctx.Err() != nil || !current.Before(deadline) || !scheduled.Before(deadline) {
		return false
	}
	if delay := scheduled.Sub(current); delay > 0 && !wait(ctx, delay) {
		return false
	}
	return ctx.Err() == nil && now().Before(deadline)
}

func (p *arrivalPacer) wait(ctx context.Context) (time.Time, bool) {
	scheduled, ok := p.claim()
	if !ok || !awaitArrival(ctx, scheduled, p.deadline, time.Now, waitUntil) {
		return time.Time{}, false
	}
	return scheduled, true
}

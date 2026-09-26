// Package clock abstracts the service's clock so tests can control time.
package clock

import "time"

// Clock tells the service's time and waits on it. The Watch Start and
// first-seen times come from it; upload times come from Immich's clock.
type Clock interface {
	Now() time.Time
	// After waits for d to pass, like time.After.
	After(d time.Duration) <-chan time.Time
	// AfterFunc calls f in its own goroutine once d has passed, like
	// time.AfterFunc.
	AfterFunc(d time.Duration, f func()) Timer
}

// Timer is a pending call made by AfterFunc.
type Timer interface {
	// Reset makes the call happen d from now instead, even if it already
	// happened or was stopped.
	Reset(d time.Duration) bool
	// Stop cancels the call, if it has not happened yet.
	Stop() bool
}

// System is the real clock.
type System struct{}

func (System) Now() time.Time                         { return time.Now() }
func (System) After(d time.Duration) <-chan time.Time { return time.After(d) }
func (System) AfterFunc(d time.Duration, f func()) Timer {
	return time.AfterFunc(d, f)
}

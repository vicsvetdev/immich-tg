// Package clock abstracts the service's clock so tests can control time.
package clock

import "time"

// Clock tells the service's time. The Watch Start and first-seen times come
// from it; upload times come from Immich's clock.
type Clock interface {
	Now() time.Time
}

// System is the real clock.
type System struct{}

func (System) Now() time.Time { return time.Now() }

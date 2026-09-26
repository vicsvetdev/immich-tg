// Package watcher is the core loop: it owns the Watch Start and, on each
// poll, finds New Videos and hands them on for publishing.
package watcher

import (
	"context"
	"time"
)

// Watcher tracks New Videos since its Watch Start. It keeps everything in
// memory; nothing survives a restart.
type Watcher struct {
	watchStart time.Time
}

// New returns a Watcher whose Watch Start is watchStart.
func New(watchStart time.Time) *Watcher {
	return &Watcher{watchStart: watchStart}
}

// WatchStart is the moment this service instance started watching.
func (w *Watcher) WatchStart() time.Time { return w.watchStart }

// Poll runs one poll. Detection of New Videos is not implemented yet.
func (w *Watcher) Poll(ctx context.Context) {}

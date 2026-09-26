// Package watcher is the core loop: it owns the Watch Start, the search window
// and the in-memory Video list and, on each poll, finds New Videos and hands
// the Ready and timed-out ones to the Publisher in upload order.
package watcher

import (
	"context"
	"log/slog"
	"slices"
	"time"

	"immich-tg/internal/clock"
	"immich-tg/internal/immich"
	"immich-tg/internal/publisher"
)

// overlap is how far before the newest upload seen each search starts, to
// catch rows that appear in Immich late.
const overlap = 5 * time.Minute

// Watcher tracks New Videos since its Watch Start. It keeps everything in
// memory; nothing survives a restart.
type Watcher struct {
	watchStart   time.Time
	sourceUserID string
	waitTimeout  time.Duration
	clock        clock.Clock
	immich       *immich.Client
	publisher    *publisher.Publisher
	log          *slog.Logger

	// windowStart is where the next search starts. It only moves forward.
	windowStart time.Time
	// newestSeen is the newest upload time seen, or the Watch Start.
	newestSeen time.Time
	// videos holds the Waiting Videos and the handled ones still inside the
	// search window, by asset id.
	videos map[string]*video
}

// video is a New Video the Watcher knows about.
type video struct {
	asset immich.Asset
	// firstSeen is when the Watcher first saw it, by the service's clock.
	firstSeen time.Time
	// handled is set once the Video went to the Publisher or was dropped.
	// Handled Videos are kept, to deduplicate overlapping searches, until the
	// window passes them.
	handled bool
}

// New returns a Watcher whose Watch Start is now, watching the uploads of the
// Source User sourceUserID. A Video still Waiting waitTimeout after it was
// first seen times out.
func New(sourceUserID string, waitTimeout time.Duration, clk clock.Clock, immichClient *immich.Client, pub *publisher.Publisher, log *slog.Logger) *Watcher {
	watchStart := clk.Now()
	return &Watcher{
		watchStart:   watchStart,
		sourceUserID: sourceUserID,
		waitTimeout:  waitTimeout,
		clock:        clk,
		immich:       immichClient,
		publisher:    pub,
		log:          log,
		windowStart:  watchStart,
		newestSeen:   watchStart,
		videos:       map[string]*video{},
	}
}

// WatchStart is the moment this service instance started watching.
func (w *Watcher) WatchStart() time.Time { return w.watchStart }

// Poll runs one poll: it discovers New Videos, drops the Waiting ones the
// operator removed and hands the Ready and timed-out ones to the Publisher,
// one at a time, oldest upload first.
func (w *Watcher) Poll(ctx context.Context) {
	now := w.clock.Now()
	found, err := w.immich.FindVideos(ctx, w.windowStart)
	if err != nil {
		w.log.Error("could not search Immich for New Videos", "error", err)
		return
	}

	candidates := w.ownVideos(found.Candidates)
	listed := make(map[string]bool, len(candidates))
	for _, a := range candidates {
		listed[a.ID] = true
		if a.CreatedAt.After(w.newestSeen) {
			w.newestSeen = a.CreatedAt
		}
		if v, ok := w.videos[a.ID]; ok {
			v.asset = a
			continue
		}
		if a.CreatedAt.Before(w.watchStart) {
			continue
		}
		v := &video{asset: a, firstSeen: now}
		w.videos[a.ID] = v
		w.log.Info("New Video waiting",
			"asset_id", a.ID,
			"created_at", a.CreatedAt,
			"first_seen", v.firstSeen,
			"tracked", len(w.videos),
		)
	}

	w.dropMissing(listed)

	for _, a := range candidates {
		v := w.videos[a.ID]
		if v == nil || v.handled {
			continue
		}
		ready := found.Ready[a.ID]
		timedOut := now.Sub(v.firstSeen) > w.waitTimeout
		if !ready && !timedOut {
			continue
		}
		if ctx.Err() != nil {
			return
		}
		if ready {
			w.publisher.Publish(ctx, v.asset)
		} else {
			w.log.Info("Waiting Video timed out", "asset_id", a.ID, "first_seen", v.firstSeen)
			w.publisher.TimedOut(ctx, v.asset, w.waitTimeout)
		}
		v.handled = true
	}

	w.advanceWindow()
}

// dropMissing silently drops the Waiting Videos that are not listed among the
// candidates: the operator trashed, archived, locked or deleted them. The
// search window always includes the oldest Waiting Video, so a missing one
// really is gone. A dropped Video counts as handled: it is not posted during
// this run even if restored, and no longer holds the window back.
func (w *Watcher) dropMissing(listed map[string]bool) {
	for id, v := range w.videos {
		if v.handled || listed[id] {
			continue
		}
		v.handled = true
		w.log.Info("Waiting Video dropped", "asset_id", id, "created_at", v.asset.CreatedAt)
	}
}

// ownVideos returns the Source User's assets from as, oldest upload first.
// Search has no owner filter, so it also returns partners' assets.
func (w *Watcher) ownVideos(as []immich.Asset) []immich.Asset {
	var own []immich.Asset
	for _, a := range as {
		if a.OwnerID == w.sourceUserID {
			own = append(own, a)
		}
	}
	slices.SortStableFunc(own, func(a, b immich.Asset) int { return a.CreatedAt.Compare(b.CreatedAt) })
	return own
}

// advanceWindow moves the window start to
// max(Watch Start, min(newest seen − overlap, oldest Waiting)), never
// backwards, and forgets the handled Videos before it: they can never be
// returned by a search again. The window is kept at the millisecond precision
// of Immich's timestamps, which the search uses, so that nothing forgotten can
// fall between the two.
func (w *Watcher) advanceWindow() {
	start := w.newestSeen.Add(-overlap)
	for _, v := range w.videos {
		if !v.handled && v.asset.CreatedAt.Before(start) {
			start = v.asset.CreatedAt
		}
	}
	w.windowStart = latest(w.windowStart, w.watchStart, start).Truncate(time.Millisecond)

	for id, v := range w.videos {
		if v.handled && v.asset.CreatedAt.Before(w.windowStart) {
			delete(w.videos, id)
		}
	}
}

func latest(ts ...time.Time) time.Time {
	return slices.MaxFunc(ts, time.Time.Compare)
}

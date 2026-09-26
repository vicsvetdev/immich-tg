// Package app is the service's lifecycle: it loads the configuration, starts
// the Watcher, announces itself in the Log Channel and polls until stopped.
package app

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"os"
	"time"

	"immich-tg/internal/clock"
	"immich-tg/internal/config"
	"immich-tg/internal/immich"
	"immich-tg/internal/publisher"
	"immich-tg/internal/telegram"
	"immich-tg/internal/watcher"
)

// Exit codes returned by Main.
const (
	ExitOK      = 0
	ExitFailure = 1
)

// watchStartLayout formats the Watch Start in the started message.
const watchStartLayout = "02 Jan 2006, 15:04:05 MST"

// PollTrigger starts polls. The service receives one channel per poll to run
// and closes it once that poll has finished, so the sender can wait for it.
type PollTrigger <-chan chan<- struct{}

// Options are the process's injectable dependencies. Zero values mean the
// production defaults.
type Options struct {
	// Stdout receives the structured logs. Default: os.Stdout.
	Stdout io.Writer
	// Clock is the service's clock. Default: the system clock.
	Clock clock.Clock
	// Polls triggers polls. Default: every POLL_INTERVAL.
	Polls PollTrigger
	// HTTPClient makes every Immich and Telegram request. Default: a new
	// client without an overall timeout, since uploads can take long; the
	// clients bound their other calls themselves.
	HTTPClient *http.Client
}

// Main runs the service until ctx is cancelled and returns the process exit
// code. getenv supplies the configuration.
func Main(ctx context.Context, getenv func(string) string, opts Options) int {
	opts = opts.withDefaults()
	log := slog.New(slog.NewJSONHandler(opts.Stdout, nil))

	cfg, err := config.Load(getenv)
	if err != nil {
		log.Error("invalid configuration", "error", err)
		return ExitFailure
	}

	tg := telegram.New(cfg.TelegramAPIURL, cfg.TelegramBotToken, opts.HTTPClient)
	im := immich.New(cfg.ImmichURL, cfg.ImmichAPIKey, opts.HTTPClient)

	sourceUserID, err := im.SourceUser(ctx)
	if err != nil {
		log.Error("could not resolve the Source User", "error", err)
		return ExitFailure
	}

	pub := publisher.New(im, tg, cfg.ImmichPublicURL, cfg.VideoChannelID, log)
	w := watcher.New(sourceUserID, opts.Clock, im, pub, log)
	started := "🟢 immich-tg started, watching uploads from " + w.WatchStart().Format(watchStartLayout)
	if err := tg.SendMessage(ctx, telegram.Message{ChatID: cfg.LogChannelID, Text: started}); err != nil {
		log.Error("could not publish the started message to the Log Channel", "error", err)
		return ExitFailure
	}
	log.Info("started",
		"watch_start", w.WatchStart(),
		"poll_interval", cfg.PollInterval.String(),
		"wait_timeout", cfg.WaitTimeout.String(),
	)

	polls := opts.Polls
	if polls == nil {
		polls = every(ctx, cfg.PollInterval)
	}
	for {
		select {
		case <-ctx.Done():
			log.Info("stopping")
			return ExitOK
		case done := <-polls:
			w.Poll(ctx)
			close(done)
		}
	}
}

func (o Options) withDefaults() Options {
	if o.Stdout == nil {
		o.Stdout = os.Stdout
	}
	if o.Clock == nil {
		o.Clock = clock.System{}
	}
	if o.HTTPClient == nil {
		o.HTTPClient = &http.Client{}
	}
	return o
}

// every triggers a poll each interval until ctx is done. Like time.Ticker,
// it drops ticks that would pile up behind a slow poll.
func every(ctx context.Context, interval time.Duration) PollTrigger {
	polls := make(chan chan<- struct{})
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				select {
				case polls <- make(chan struct{}):
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return polls
}

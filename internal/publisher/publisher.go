// Package publisher turns one Ready Video into its Post in the Video Channel.
package publisher

import (
	"context"
	"fmt"
	"html"
	"log/slog"
	"time"

	"immich-tg/internal/immich"
	"immich-tg/internal/telegram"
)

// recordingDateLayout formats the Recording Date in captions.
const recordingDateLayout = "02 Jan 2006, 15:04"

// Publisher publishes Posts.
type Publisher struct {
	immich         *immich.Client
	telegram       *telegram.Client
	publicURL      string
	videoChannelID string
	log            *slog.Logger
}

// New returns a Publisher that posts to videoChannelID. Share Links point to
// publicURL, Immich's public address without a trailing slash.
func New(immichClient *immich.Client, tg *telegram.Client, publicURL, videoChannelID string, log *slog.Logger) *Publisher {
	return &Publisher{
		immich:         immichClient,
		telegram:       tg,
		publicURL:      publicURL,
		videoChannelID: videoChannelID,
		log:            log,
	}
}

// Publish creates the Video's Share Link and publishes its Post with the
// Transcode. It is never repeated: whatever the outcome, the Video has been
// handled.
func (p *Publisher) Publish(ctx context.Context, v immich.Asset) {
	log := p.log.With("asset_id", v.ID)
	key, err := p.immich.CreateShareLink(ctx, v.ID)
	if err != nil {
		log.Error("could not create the Share Link; no Post", "error", err)
		return
	}
	header, err := p.immich.TranscodeHeader(ctx, v.ID)
	if err != nil {
		log.Error("could not read the Transcode header; no Post", "error", err)
		return
	}
	width, height := header.DisplaySize()
	transcode, err := p.immich.OpenTranscode(ctx, v.ID)
	if err != nil {
		log.Error("could not download the Transcode; no Post", "error", err)
		return
	}
	defer transcode.Close()
	post := telegram.Video{
		ChatID:    p.videoChannelID,
		File:      transcode,
		FileName:  v.ID + ".mp4",
		Caption:   caption(v.LocalDateTime, p.publicURL+"/share/"+key),
		ParseMode: "HTML",
		Width:     width,
		Height:    height,
		Duration:  int(time.Duration(v.Duration).Round(time.Second) / time.Second),
	}
	if err := p.telegram.SendVideo(ctx, post); err != nil {
		log.Error("could not publish the Post", "error", err)
		return
	}
	log.Info("posted",
		"recording_date", formatRecordingDate(v.LocalDateTime),
		"width", post.Width,
		"height", post.Height,
		"rotation", header.Rotation,
		"duration", post.Duration,
	)
}

// caption is a Post's caption in Telegram's HTML parse mode.
func caption(recordingDate time.Time, shareLink string) string {
	return fmt.Sprintf("📅 %s\n▶️ <a href=\"%s\">Watch in original quality</a>",
		formatRecordingDate(recordingDate), html.EscapeString(shareLink))
}

// formatRecordingDate formats Immich's localDateTime. It holds the wall-clock
// time at the place of recording in its UTC fields, so it is formatted as-is,
// with no time zone conversion.
func formatRecordingDate(localDateTime time.Time) string {
	return localDateTime.UTC().Format(recordingDateLayout)
}

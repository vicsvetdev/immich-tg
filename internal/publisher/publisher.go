// Package publisher turns one Video into its outcome: a Post in the Video
// Channel, a Link-only Post and/or a Problem Report in the Log Channel.
package publisher

import (
	"context"
	"fmt"
	"html"
	"log/slog"
	"time"

	"immich-tg/internal/immich"
	"immich-tg/internal/telegram"
	"immich-tg/internal/thumbnail"
)

// recordingDateLayout formats the Recording Date in captions.
const recordingDateLayout = "02 Jan 2006, 15:04"

// Publisher publishes Posts.
type Publisher struct {
	immich         *immich.Client
	telegram       *telegram.Client
	publicURL      string
	videoChannelID string
	logChannelID   string
	log            *slog.Logger
}

// New returns a Publisher that posts to videoChannelID and reports problems to
// logChannelID. Share Links and the links to assets in Problem Reports point
// to publicURL, Immich's public address without a trailing slash.
func New(immichClient *immich.Client, tg *telegram.Client, publicURL, videoChannelID, logChannelID string, log *slog.Logger) *Publisher {
	return &Publisher{
		immich:         immichClient,
		telegram:       tg,
		publicURL:      publicURL,
		videoChannelID: videoChannelID,
		logChannelID:   logChannelID,
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
	thumb := p.thumbnail(ctx, v.ID, log)
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
		Thumbnail: thumb,
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

// thumbnail makes the Post's thumbnail from the Video's preview image. On
// failure it returns nil: the Post goes out without one, and it is only
// logged, never a Problem Report.
func (p *Publisher) thumbnail(ctx context.Context, assetID string, log *slog.Logger) []byte {
	preview, contentType, err := p.immich.Preview(ctx, assetID)
	if err != nil {
		log.Warn("could not fetch the preview; no thumbnail", "error", err)
		return nil
	}
	thumb, err := thumbnail.Make(preview, contentType)
	if err != nil {
		log.Warn("could not make the thumbnail; no thumbnail", "error", err)
		return nil
	}
	return thumb
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

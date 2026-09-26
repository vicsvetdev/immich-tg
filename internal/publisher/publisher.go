// Package publisher turns one Video into its outcome: a Post in the Video
// Channel, a Link-only Post and/or a Problem Report in the Log Channel.
package publisher

import (
	"context"
	"errors"
	"fmt"
	"html"
	"io"
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

// maxTranscodeSize is the largest Transcode uploaded, in bytes: a safe reading
// of the 2000 MB the Bot API server accepts in local mode.
const maxTranscodeSize = 2_000_000_000

// Publish creates the Video's Share Link and publishes its Post with the
// Transcode or, if the Transcode cannot be posted, a Link-only Post and a
// Problem Report. It is never repeated: whatever the outcome, the Video has
// been handled.
func (p *Publisher) Publish(ctx context.Context, v immich.Asset) {
	shareLink, err := p.shareLink(ctx, v)
	if err != nil {
		p.fail(ctx, v, "", problem{kind: kindShareLink, err: err})
		return
	}
	if prob := p.postVideo(ctx, v, shareLink); prob != nil {
		p.fail(ctx, v, shareLink, *prob)
	}
}

// shareLink creates the Video's Share Link and returns its URL.
func (p *Publisher) shareLink(ctx context.Context, v immich.Asset) (string, error) {
	key, err := p.immich.CreateShareLink(ctx, v.ID)
	if err != nil {
		return "", err
	}
	return p.publicURL + "/share/" + key, nil
}

// postVideo publishes the Video's Post with the Transcode, or returns why it
// could not.
func (p *Publisher) postVideo(ctx context.Context, v immich.Asset, shareLink string) *problem {
	header, err := p.immich.TranscodeHeader(ctx, v.ID)
	if errors.Is(err, immich.ErrUnreadableHeader) {
		return &problem{kindUnreadable, err}
	}
	if err != nil {
		return &problem{kindDownload, fmt.Errorf("could not read the Transcode header: %w", err)}
	}
	width, height := header.DisplaySize()
	thumb := p.thumbnail(ctx, v.ID, p.log.With("asset_id", v.ID))
	post := telegram.Video{
		ChatID:    p.videoChannelID,
		Open:      func(ctx context.Context) (io.ReadCloser, error) { return p.openTranscode(ctx, v.ID) },
		FileName:  v.ID + ".mp4",
		Caption:   caption(v.LocalDateTime, shareLink),
		ParseMode: "HTML",
		Width:     width,
		Height:    height,
		Duration:  int(time.Duration(v.Duration).Round(time.Second) / time.Second),
		Thumbnail: thumb,
	}
	err = p.telegram.SendVideo(ctx, post)
	if _, ok := errors.AsType[*oversizedError](err); ok {
		return &problem{kindOversized, err}
	}
	if _, ok := errors.AsType[*telegram.FileError](err); ok {
		return &problem{kindDownload, fmt.Errorf("could not download the Transcode: %w", err)}
	}
	if err != nil {
		return &problem{kindUpload, err}
	}
	p.log.Info("posted",
		"asset_id", v.ID,
		"recording_date", formatRecordingDate(v.LocalDateTime),
		"width", post.Width,
		"height", post.Height,
		"rotation", header.Rotation,
		"duration", post.Duration,
	)
	return nil
}

// openTranscode opens the Video's Transcode for an upload, unless its
// Content-Length shows it is an Oversized Video. Without a Content-Length the
// upload goes ahead, and Telegram decides.
func (p *Publisher) openTranscode(ctx context.Context, assetID string) (io.ReadCloser, error) {
	t, err := p.immich.OpenTranscode(ctx, assetID)
	if err != nil {
		return nil, err
	}
	if t.Size > maxTranscodeSize {
		t.Close()
		return nil, &oversizedError{t.Size}
	}
	return t, nil
}

// oversizedError is the Transcode of an Oversized Video.
type oversizedError struct {
	size int64
}

func (e *oversizedError) Error() string {
	return fmt.Sprintf("the Transcode is %d bytes, over the %d bytes Telegram accepts", e.size, int64(maxTranscodeSize))
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

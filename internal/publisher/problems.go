package publisher

import (
	"context"
	"fmt"
	"time"

	"immich-tg/internal/immich"
	"immich-tg/internal/telegram"
)

// Kinds of problem, named in Problem Reports.
const kindTimeout = "no Transcode in time"

// Notes that end a Link-only Post's caption, saying why it has no video.
const noteNotAvailable = "video not available in Telegram"

// TimedOut handles a Video that stayed Waiting longer than waitTimeout: it
// publishes a Link-only Post and a Problem Report. It is never repeated:
// whatever the outcome, the Video has been handled.
func (p *Publisher) TimedOut(ctx context.Context, v immich.Asset, waitTimeout time.Duration) {
	reason := fmt.Errorf("Immich produced no Transcode within WAIT_TIMEOUT (%s)", waitTimeout)
	if err := p.publishLinkOnly(ctx, v, noteNotAvailable); err != nil {
		reason = fmt.Errorf("%w; no Link-only Post: %w", reason, err)
	}
	p.report(ctx, kindTimeout, v, reason)
}

// publishLinkOnly creates the Video's Share Link and publishes a Link-only
// Post: the usual caption plus note. Link previews stay enabled, so that
// Telegram shows the Share Link page's preview card.
func (p *Publisher) publishLinkOnly(ctx context.Context, v immich.Asset, note string) error {
	key, err := p.immich.CreateShareLink(ctx, v.ID)
	if err != nil {
		return fmt.Errorf("could not create the Share Link: %w", err)
	}
	post := telegram.Message{
		ChatID:    p.videoChannelID,
		Text:      caption(v.LocalDateTime, p.publicURL+"/share/"+key) + "\nℹ️ " + note,
		ParseMode: "HTML",
	}
	if err := p.telegram.SendMessage(ctx, post); err != nil {
		return fmt.Errorf("could not publish the Link-only Post: %w", err)
	}
	p.log.Info("posted Link-only", "asset_id", v.ID, "recording_date", formatRecordingDate(v.LocalDateTime), "note", note)
	return nil
}

// report publishes a Problem Report about the Video to the Log Channel and
// logs the same information to stdout, so that it is not lost when Telegram
// cannot be reached.
func (p *Publisher) report(ctx context.Context, kind string, v immich.Asset, reason error) {
	recordingDate := formatRecordingDate(v.LocalDateTime)
	// The link to the asset in Immich, for the operator; not a Share Link.
	assetLink := p.publicURL + "/photos/" + v.ID
	p.log.Error("Problem Report",
		"kind", kind,
		"asset_id", v.ID,
		"recording_date", recordingDate,
		"original_file_name", v.OriginalFileName,
		"asset_link", assetLink,
		"error", reason,
	)
	// Plain text: the filename and error are shown as they are, and Telegram
	// still makes the asset link clickable.
	text := fmt.Sprintf("⚠️ Problem: %s\n📅 %s\n📄 %s\n🔗 %s\nReason: %s",
		kind, recordingDate, v.OriginalFileName, assetLink, reason)
	if err := p.telegram.SendMessage(ctx, telegram.Message{ChatID: p.logChannelID, Text: text}); err != nil {
		p.log.Error("could not publish the Problem Report to the Log Channel", "asset_id", v.ID, "error", err)
	}
}

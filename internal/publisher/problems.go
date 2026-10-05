package publisher

import (
	"context"
	"fmt"
	"time"

	"immich-tg/internal/immich"
	"immich-tg/internal/shutdown"
	"immich-tg/internal/telegram"
)

// problemKind names the kind of problem in a Problem Report.
type problemKind string

const (
	kindOversized  problemKind = "oversized"
	kindTimeout    problemKind = "no Transcode in time"
	kindDownload   problemKind = "Transcode download failed"
	kindUnreadable problemKind = "Transcode header unreadable"
	kindUpload     problemKind = "upload failed"
	kindShareLink  problemKind = "Share Link creation failed"
)

// linkOnlyNote ends a Link-only Post's caption, saying why it has no video.
type linkOnlyNote string

const (
	noteTooLarge     linkOnlyNote = "video too large for Telegram"
	noteNotAvailable linkOnlyNote = "video not available in Telegram"
)

// note is the note of the Link-only Post that a problem of kind k gets, or
// empty if it gets none.
func (k problemKind) note() linkOnlyNote {
	switch k {
	case kindOversized:
		return noteTooLarge
	case kindShareLink:
		return "" // a Post without its Share Link would be a dead end
	default:
		return noteNotAvailable
	}
}

// problem is why a Video could not be handled as intended.
type problem struct {
	kind problemKind
	err  error
}

// TimedOut handles a Video that stayed Waiting longer than waitTimeout: it
// publishes a Link-only Post and a Problem Report. It is never repeated:
// whatever the outcome, the Video has been handled. A timeout is a genuine
// failure, so its outcome is published even if shutdown begins meanwhile.
func (p *Publisher) TimedOut(ctx context.Context, v immich.Asset, waitTimeout time.Duration) {
	ctx, cancel := shutdown.Outlive(ctx)
	defer cancel()
	prob := problem{kindTimeout, fmt.Errorf("Immich produced no Transcode within WAIT_TIMEOUT (%s)", waitTimeout)}
	link, err := p.originalLink(ctx, v)
	if err != nil {
		prob.err = fmt.Errorf("%w; no Link-only Post, could not create the Share Link: %w", prob.err, err)
	}
	p.publishOutcome(ctx, v, link, prob)
}

// fail handles a Video that could not be posted as intended. A failure caused
// by the shutdown abandons the Video: it is only logged, and the stopped
// message tells the operator why. Any other failure gets its outcome, even
// once shutdown has begun, within the shutdown grace.
func (p *Publisher) fail(ctx context.Context, v immich.Asset, originalLink string, prob problem) {
	if shutdown.Caused(ctx, prob.err) {
		p.log.Info("Video abandoned on shutdown", "asset_id", v.ID, "error", prob.err)
		return
	}
	ctx, cancel := shutdown.Outlive(ctx)
	defer cancel()
	p.publishOutcome(ctx, v, originalLink, prob)
}

// publishOutcome publishes a failure's outcome: a Link-only Post, given a
// link to the original and a kind of problem that gets one, then a Problem Report, which
// notes a failed Link-only Post too.
func (p *Publisher) publishOutcome(ctx context.Context, v immich.Asset, originalLink string, prob problem) {
	if note := prob.kind.note(); originalLink != "" && note != "" {
		if err := p.publishLinkOnly(ctx, v, originalLink, note); err != nil {
			prob.err = fmt.Errorf("%w; the Link-only Post failed too: %w", prob.err, err)
		}
	}
	p.report(ctx, v, prob)
}

// publishLinkOnly publishes a Link-only Post: the usual caption plus note.
// Link previews stay enabled.
func (p *Publisher) publishLinkOnly(ctx context.Context, v immich.Asset, originalLink string, note linkOnlyNote) error {
	post := telegram.Message{
		ChatID:    p.videoChannelID,
		Text:      p.caption(v, originalLink) + "\nℹ️ " + string(note),
		ParseMode: "HTML",
	}
	if err := p.telegram.SendMessage(ctx, post); err != nil {
		return err
	}
	p.log.Info("posted Link-only", "asset_id", v.ID, "recording_date", formatRecordingDate(v.LocalDateTime), "note", note)
	return nil
}

// report publishes a Problem Report about the Video to the Log Channel and
// logs the same information to stdout, so that it is not lost when Telegram
// cannot be reached.
func (p *Publisher) report(ctx context.Context, v immich.Asset, prob problem) {
	recordingDate := formatRecordingDate(v.LocalDateTime)
	// The link to the asset in Immich, for the operator; not a Share Link.
	assetLink := p.publicURL + "/photos/" + v.ID
	p.log.Error("Problem Report",
		"kind", prob.kind,
		"asset_id", v.ID,
		"recording_date", recordingDate,
		"original_file_name", v.OriginalFileName,
		"asset_link", assetLink,
		"error", prob.err,
	)
	// Plain text: the filename and error are shown as they are, and Telegram
	// still makes the asset link clickable.
	text := fmt.Sprintf("⚠️ Problem: %s\n📅 %s\n📄 %s\n🔗 %s\nReason: %s",
		prob.kind, recordingDate, v.OriginalFileName, assetLink, prob.err)
	if err := p.telegram.SendMessage(ctx, telegram.Message{ChatID: p.logChannelID, Text: text}); err != nil {
		p.log.Error("could not publish the Problem Report to the Log Channel", "asset_id", v.ID, "error", err)
	}
}

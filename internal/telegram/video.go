package telegram

import (
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/textproto"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"immich-tg/internal/clock"
)

const (
	// stallTimeout is how long an upload may go without moving any data
	// before it is abandoned, whichever side stalled: the file being read or
	// the Bot API server taking it.
	stallTimeout = 2 * time.Minute
	// responseTimeout bounds the wait for the Bot API server's answer once the
	// whole file is sent, generous enough for it to hand 2 GB on to Telegram.
	responseTimeout = time.Hour
)

// Video is a sendVideo request.
type Video struct {
	ChatID string
	// Open opens the MP4 to upload, which is read to its end and closed by
	// SendVideo. It is called again for each repeat after a 429, since the
	// previous stream has been consumed. ctx ends with the attempt.
	Open func(ctx context.Context) (io.ReadCloser, error)
	// FileName is the name the file is uploaded under, e.g. "<id>.mp4".
	FileName string
	Caption  string
	// ParseMode is empty for a plain caption, or "HTML".
	ParseMode string
	// Width and Height are the display dimensions.
	Width, Height int
	// Duration is in whole seconds.
	Duration int
	// Thumbnail is a JPEG thumbnail, or nil for none.
	Thumbnail []byte
}

// FileError is a failure to open or read the file of a Video, as opposed to a
// failure of the upload itself.
type FileError struct {
	Err error
}

func (e *FileError) Error() string { return e.Err.Error() }
func (e *FileError) Unwrap() error { return e.Err }

// SendVideo sends a streamable video. The file is streamed straight into a
// chunked multipart request, never held in memory or on disk. There is no
// overall timeout, since an upload of up to 2 GB can take long; instead the
// upload fails once no data has moved for the stall timeout. A 429 is waited
// out and the upload repeated with a freshly opened file.
func (c *Client) SendVideo(ctx context.Context, v Video) error {
	return c.waitingOutRateLimits(ctx, func() error { return c.sendVideo(ctx, v) })
}

// sendVideo makes one attempt at uploading v.
func (c *Client) sendVideo(parent context.Context, v Video) error {
	ctx, cancel := context.WithCancelCause(parent)
	defer cancel(nil)
	f, err := v.Open(ctx)
	if err != nil {
		return &FileError{err}
	}
	defer f.Close()

	file := &guardedFile{r: f}
	file.timer = c.clock.AfterFunc(stallTimeout, func() { cancel(file.stalled()) })
	defer file.stop()

	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		err := writeVideo(mw, v, file)
		if err == nil {
			file.sent.Store(true)
			file.postpone(responseTimeout)
		}
		pw.CloseWithError(err)
	}()
	// The pipe has no length, so the request is sent chunked.
	err = c.post(ctx, "sendVideo", mw.FormDataContentType(), pr, nil)
	// If the request ended before reading the whole body, the writer stops at
	// its next write, or once the file is closed on return. It is not waited
	// for, since the file may be stalled.
	pr.Close()
	if err == nil {
		return nil
	}
	if ctx.Err() != nil && parent.Err() == nil {
		return context.Cause(ctx) // the stall guard
	}
	if ferr := file.err(); ferr != nil {
		return &FileError{ferr}
	}
	return err
}

// guardedFile is the file of an upload in progress. Each read that returns
// data postpones its stall timer.
type guardedFile struct {
	r     io.Reader
	timer clock.Timer
	// reading is set during a read, so that a stall can be blamed on the
	// file's side.
	reading atomic.Bool
	// sent is set once the whole file has gone into the request.
	sent atomic.Bool

	mu      sync.Mutex
	readErr error
	// done is set once the attempt is over, so the timer is never armed
	// again by a writer still finishing.
	done bool
}

func (f *guardedFile) Read(p []byte) (int, error) {
	f.reading.Store(true)
	n, err := f.r.Read(p)
	f.reading.Store(false)
	if n > 0 {
		f.postpone(stallTimeout)
	}
	if err != nil && err != io.EOF {
		f.mu.Lock()
		if f.readErr == nil {
			f.readErr = err
		}
		f.mu.Unlock()
	}
	return n, err
}

// postpone makes the stall timer fire d from now, unless the attempt is over.
func (f *guardedFile) postpone(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.done {
		f.timer.Reset(d)
	}
}

// stop stops the stall timer for good.
func (f *guardedFile) stop() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.done = true
	f.timer.Stop()
}

// err is the first error reading the file, other than its end.
func (f *guardedFile) err() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.readErr
}

// stalled is the error for an upload whose stall timer has fired.
func (f *guardedFile) stalled() error {
	switch {
	case f.sent.Load():
		return fmt.Errorf("telegram sendVideo: no response within %s of sending the whole file", responseTimeout)
	case f.reading.Load():
		return &FileError{fmt.Errorf("stalled: no data for %s", stallTimeout)}
	default:
		return fmt.Errorf("telegram sendVideo: stalled: the Bot API server took no data for %s", stallTimeout)
	}
}

// writeVideo writes v as a multipart form, the video file last.
func writeVideo(mw *multipart.Writer, v Video, file io.Reader) error {
	fields := [][2]string{
		{"chat_id", v.ChatID},
		{"caption", v.Caption},
		{"parse_mode", v.ParseMode},
		{"supports_streaming", "true"},
		{"width", strconv.Itoa(v.Width)},
		{"height", strconv.Itoa(v.Height)},
		{"duration", strconv.Itoa(v.Duration)},
	}
	if v.Thumbnail != nil {
		fields = append(fields, [2]string{"thumbnail", "attach://thumb"})
	}
	for _, f := range fields {
		if f[1] == "" {
			continue
		}
		if err := mw.WriteField(f[0], f[1]); err != nil {
			return err
		}
	}
	if v.Thumbnail != nil {
		part, err := mw.CreatePart(textproto.MIMEHeader{
			"Content-Disposition": {multipart.FileContentDisposition("thumb", "thumb.jpg")},
			"Content-Type":        {"image/jpeg"},
		})
		if err != nil {
			return err
		}
		if _, err := part.Write(v.Thumbnail); err != nil {
			return err
		}
	}
	part, err := mw.CreatePart(textproto.MIMEHeader{
		"Content-Disposition": {multipart.FileContentDisposition("video", v.FileName)},
		"Content-Type":        {"video/mp4"},
	})
	if err != nil {
		return err
	}
	if _, err := io.Copy(part, file); err != nil {
		return fmt.Errorf("stream video file: %w", err)
	}
	return mw.Close()
}

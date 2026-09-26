package telegram

import (
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/textproto"
	"strconv"
)

// Video is a sendVideo request whose file is streamed from File.
type Video struct {
	ChatID string
	// File is the MP4 to upload, read to its end. The caller closes it after
	// SendVideo returns, which also stops a read still in progress.
	File io.Reader
	// FileName is the name the file is uploaded under, e.g. "<id>.mp4".
	FileName string
	Caption  string
	// ParseMode is empty for a plain caption, or "HTML".
	ParseMode string
	// Width and Height are the display dimensions.
	Width, Height int
	// Duration is in whole seconds.
	Duration int
}

// SendVideo sends a streamable video. The file is streamed straight into a
// chunked multipart request, never held in memory or on disk. There is no
// timeout, since an upload of up to 2 GB can take long.
func (c *Client) SendVideo(ctx context.Context, v Video) error {
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() { pw.CloseWithError(writeVideo(mw, v)) }()
	// The pipe has no length, so the request is sent chunked.
	err := c.post(ctx, "sendVideo", mw.FormDataContentType(), pr, nil)
	// If the request ended before reading the whole body, the writer stops at
	// its next write, or when the caller closes File. It is not waited for,
	// since File may be stalled.
	pr.Close()
	return err
}

// writeVideo writes v as a multipart form, the file last.
func writeVideo(mw *multipart.Writer, v Video) error {
	fields := [][2]string{
		{"chat_id", v.ChatID},
		{"caption", v.Caption},
		{"parse_mode", v.ParseMode},
		{"supports_streaming", "true"},
		{"width", strconv.Itoa(v.Width)},
		{"height", strconv.Itoa(v.Height)},
		{"duration", strconv.Itoa(v.Duration)},
	}
	for _, f := range fields {
		if f[1] == "" {
			continue
		}
		if err := mw.WriteField(f[0], f[1]); err != nil {
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
	if _, err := io.Copy(part, v.File); err != nil {
		return fmt.Errorf("stream video file: %w", err)
	}
	return mw.Close()
}

// Package telegram is a thin client for the Telegram Bot API server.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"immich-tg/internal/clock"
)

const (
	// maxResponseSize bounds how much of a Bot API response is read.
	maxResponseSize = 1 << 20
	// callTimeout bounds a call without an upload, so that a hung Bot API
	// server fails the call instead of blocking the service.
	callTimeout = 30 * time.Second
	// maxRetryAfter is the longest rate-limit wait that is waited out. A 429
	// asking for longer fails the call, so that it cannot block the service.
	maxRetryAfter = 5 * time.Minute
)

// Client calls the Bot API for one bot.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
	// clock times the waits for rate limits and the upload stall guard.
	clock clock.Clock
}

// New returns a client for the Bot API server at baseURL, e.g.
// http://telegram-bot-api:8081, without a trailing slash.
func New(baseURL, token string, httpClient *http.Client, clk clock.Clock) *Client {
	return &Client{baseURL: baseURL, token: token, http: httpClient, clock: clk}
}

// Message is a text message.
type Message struct {
	ChatID string
	Text   string
	// ParseMode is empty for plain text, or "HTML".
	ParseMode string
	// PreviewURL is the link Telegram previews. If empty, it previews the
	// first link in Text.
	PreviewURL string
}

// linkPreviewOptions are the Bot API's LinkPreviewOptions.
type linkPreviewOptions struct {
	URL string `json:"url"`
}

// SendMessage sends a text message.
func (c *Client) SendMessage(ctx context.Context, m Message) error {
	params := struct {
		ChatID             string              `json:"chat_id"`
		Text               string              `json:"text"`
		ParseMode          string              `json:"parse_mode,omitempty"`
		LinkPreviewOptions *linkPreviewOptions `json:"link_preview_options,omitempty"`
	}{ChatID: m.ChatID, Text: m.Text, ParseMode: m.ParseMode}
	if m.PreviewURL != "" {
		params.LinkPreviewOptions = &linkPreviewOptions{URL: m.PreviewURL}
	}
	return c.call(ctx, "sendMessage", params, nil)
}

// Error is an error answered by the Bot API server. It comes either as JSON
// or, from the server's HTTP layer, as a bare status with an empty body, in
// which case Code and Description are empty.
type Error struct {
	Method      string
	StatusCode  int
	Code        int
	Description string
	// RetryAfter is how long to wait before repeating the call, set on a 429.
	RetryAfter time.Duration
}

func (e *Error) Error() string {
	if e.Description == "" {
		return fmt.Sprintf("telegram %s: HTTP %d %s", e.Method, e.StatusCode, http.StatusText(e.StatusCode))
	}
	return fmt.Sprintf("telegram %s: %d %s", e.Method, e.Code, e.Description)
}

type response struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result"`
	ErrorCode   int             `json:"error_code"`
	Description string          `json:"description"`
	Parameters  struct {
		RetryAfter int `json:"retry_after"`
	} `json:"parameters"`
}

// rateLimited reports whether err is a 429 that says how long to wait before
// repeating the call.
func rateLimited(err error) (retryAfter time.Duration, ok bool) {
	e, ok := errors.AsType[*Error](err)
	if !ok || (e.StatusCode != http.StatusTooManyRequests && e.Code != http.StatusTooManyRequests) || e.RetryAfter <= 0 {
		return 0, false
	}
	return e.RetryAfter, true
}

// waitingOutRateLimits runs attempt and, each time it is rate limited, waits
// as long as the 429 asks and runs it again. This is the only repeat in the
// service. A wait over maxRetryAfter is not waited out: the call fails. It
// stops waiting when ctx is done.
func (c *Client) waitingOutRateLimits(ctx context.Context, attempt func() error) error {
	for {
		err := attempt()
		retryAfter, ok := rateLimited(err)
		if !ok {
			return err
		}
		if retryAfter > maxRetryAfter {
			return fmt.Errorf("%w; not waited out: the rate-limit wait of %s is over the %s limit", err, retryAfter, maxRetryAfter)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%w; stopped waiting for the rate limit: %w", err, context.Cause(ctx))
		case <-c.clock.After(retryAfter):
		}
	}
}

// call sends params as JSON to method and decodes the result into result,
// unless it is nil. Each attempt is bounded by the call timeout.
func (c *Client) call(ctx context.Context, method string, params, result any) error {
	body, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("telegram %s: encode request: %w", method, err)
	}
	return c.waitingOutRateLimits(ctx, func() error {
		ctx, cancel := context.WithTimeout(ctx, callTimeout)
		defer cancel()
		return c.post(ctx, method, "application/json", bytes.NewReader(body), result)
	})
}

// post sends body to method and decodes the result into result, unless it is
// nil.
func (c *Client) post(ctx context.Context, method, contentType string, body io.Reader, result any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/bot"+c.token+"/"+method, body)
	if err != nil {
		return fmt.Errorf("telegram %s: %w", method, c.redact(err))
	}
	req.Header.Set("Content-Type", contentType)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("telegram %s: %w", method, c.redact(err))
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
	if err != nil {
		return fmt.Errorf("telegram %s: read response: %w", method, err)
	}

	var r response
	if err := json.Unmarshal(data, &r); err != nil {
		if resp.StatusCode != http.StatusOK {
			return &Error{Method: method, StatusCode: resp.StatusCode}
		}
		return fmt.Errorf("telegram %s: invalid response: %w", method, err)
	}
	if !r.OK || resp.StatusCode != http.StatusOK {
		return &Error{
			Method:      method,
			StatusCode:  resp.StatusCode,
			Code:        r.ErrorCode,
			Description: r.Description,
			RetryAfter:  time.Duration(r.Parameters.RetryAfter) * time.Second,
		}
	}
	if result != nil {
		if err := json.Unmarshal(r.Result, result); err != nil {
			return fmt.Errorf("telegram %s: decode result: %w", method, err)
		}
	}
	return nil
}

// redact removes the bot token from err, which may contain the request URL.
func (c *Client) redact(err error) error {
	var uerr *url.Error
	if errors.As(err, &uerr) {
		uerr.URL = strings.ReplaceAll(uerr.URL, c.token, "<token>")
	}
	return err
}

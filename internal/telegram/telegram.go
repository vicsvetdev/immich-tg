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
)

const (
	// maxResponseSize bounds how much of a Bot API response is read.
	maxResponseSize = 1 << 20
	// callTimeout bounds a call without an upload, so that a hung Bot API
	// server fails the call instead of blocking the service.
	callTimeout = 30 * time.Second
)

// Client calls the Bot API for one bot.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

// New returns a client for the Bot API server at baseURL, e.g.
// http://telegram-bot-api:8081, without a trailing slash.
func New(baseURL, token string, httpClient *http.Client) *Client {
	return &Client{baseURL: baseURL, token: token, http: httpClient}
}

// Message is a text message.
type Message struct {
	ChatID string
	Text   string
	// ParseMode is empty for plain text, or "HTML".
	ParseMode string
}

// SendMessage sends a text message.
func (c *Client) SendMessage(ctx context.Context, m Message) error {
	params := struct {
		ChatID    string `json:"chat_id"`
		Text      string `json:"text"`
		ParseMode string `json:"parse_mode,omitempty"`
	}{m.ChatID, m.Text, m.ParseMode}
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

// call sends params as JSON to method and decodes the result into result,
// unless it is nil. It is bounded by the call timeout.
func (c *Client) call(ctx context.Context, method string, params, result any) error {
	body, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("telegram %s: encode request: %w", method, err)
	}
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	return c.post(ctx, method, "application/json", bytes.NewReader(body), result)
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

// Package immich is a thin client for the few Immich API endpoints the service
// uses. It hides pagination and response shapes behind domain-level
// operations.
package immich

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

	"immich-tg/internal/mp4"
)

const (
	// maxResponseSize bounds how much of an API response is read. A search
	// page of 1000 assets is well below it.
	maxResponseSize = 32 << 20
	// maxErrorSize bounds how much of an error response to a download is read.
	maxErrorSize = 64 << 10
	// callTimeout bounds each API call, so that a hung Immich fails the call
	// instead of blocking the service.
	callTimeout = 30 * time.Second
	// searchPageSize is the number of assets asked for per search page.
	searchPageSize = 1000
	// headerSize is how much of the Transcode is read for its header.
	// Immich encodes with faststart, so the moov box is at the start.
	headerSize = 1 << 20
)

// Client calls the Immich API with one API key.
type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

// New returns a client for the Immich instance at baseURL, e.g.
// http://192.168.1.10:2283, without a trailing slash.
func New(baseURL, apiKey string, httpClient *http.Client) *Client {
	return &Client{baseURL: baseURL, apiKey: apiKey, http: httpClient}
}

// Asset is an Immich asset as returned by search.
type Asset struct {
	ID      string `json:"id"`
	OwnerID string `json:"ownerId"`
	// CreatedAt is when the asset was uploaded, by Immich's clock.
	CreatedAt time.Time `json:"createdAt"`
	// LocalDateTime is the Recording Date: the wall-clock time at the place of
	// recording, serialised as if it were UTC.
	LocalDateTime    time.Time `json:"localDateTime"`
	OriginalFileName string    `json:"originalFileName"`
	Duration         Duration  `json:"duration"`
}

// Duration is a Video's length. Immich 3.2 serialises it as an integer of
// milliseconds. Any other form decodes as zero rather than failing, so that
// it can never break a whole search.
type Duration time.Duration

func (d *Duration) UnmarshalJSON(data []byte) error {
	var ms float64
	if json.Unmarshal(data, &ms) == nil {
		*d = Duration(ms * float64(time.Millisecond))
	}
	return nil
}

// Version is an Immich server version.
type Version struct {
	Major int `json:"major"`
	Minor int `json:"minor"`
	Patch int `json:"patch"`
}

func (v Version) String() string { return fmt.Sprintf("v%d.%d.%d", v.Major, v.Minor, v.Patch) }

// AtLeast reports whether v is oldest or newer.
func (v Version) AtLeast(oldest Version) bool {
	if v.Major != oldest.Major {
		return v.Major > oldest.Major
	}
	if v.Minor != oldest.Minor {
		return v.Minor > oldest.Minor
	}
	return v.Patch >= oldest.Patch
}

// ServerVersion returns the version of the Immich server.
func (c *Client) ServerVersion(ctx context.Context) (Version, error) {
	var v Version
	err := c.call(ctx, http.MethodGet, "/api/server/version", nil, &v)
	return v, err
}

// KeyPermissions returns the permissions of the client's API key, such as
// "asset.read", or "all" for an unrestricted key.
func (c *Client) KeyPermissions(ctx context.Context) ([]string, error) {
	var key struct {
		Permissions []string `json:"permissions"`
	}
	err := c.call(ctx, http.MethodGet, "/api/api-keys/me", nil, &key)
	return key.Permissions, err
}

// User is an Immich user.
type User struct {
	ID string `json:"id"`
}

// SourceUser returns the Source User: the user the client's API key belongs
// to.
func (c *Client) SourceUser(ctx context.Context) (User, error) {
	var u User
	if err := c.call(ctx, http.MethodGet, "/api/users/me", nil, &u); err != nil {
		return User{}, err
	}
	if u.ID == "" {
		return User{}, fmt.Errorf("immich GET /api/users/me: response has no user id")
	}
	return u, nil
}

// SearchResult holds the videos uploaded since a moment, by any user.
type SearchResult struct {
	// Candidates are the videos on the timeline that are not trashed.
	Candidates []Asset
	// Ready holds the ids of the videos that have a Transcode.
	Ready map[string]bool
}

// FindVideos returns the candidate videos uploaded at or after since, and which
// of them are Ready. Results include partners' videos too, in no particular
// order.
func (c *Client) FindVideos(ctx context.Context, since time.Time) (SearchResult, error) {
	candidates, err := c.searchVideos(ctx, since, false)
	if err != nil {
		return SearchResult{}, err
	}
	ready, err := c.searchVideos(ctx, since, true)
	if err != nil {
		return SearchResult{}, err
	}
	result := SearchResult{Candidates: candidates, Ready: make(map[string]bool, len(ready))}
	for _, a := range ready {
		result.Ready[a.ID] = true
	}
	return result, nil
}

// searchBody is a metadata search in the filter format introduced in Immich
// 3.2. It must not be mixed with the deprecated flat fields, and the filter
// object is strict: any unknown key is rejected.
type searchBody struct {
	Filter searchFilter `json:"filter"`
	Size   int          `json:"size"`
	Cursor string       `json:"cursor,omitempty"`
}

type searchFilter struct {
	Type       eq[string] `json:"type"`
	Visibility eq[string] `json:"visibility"`
	// TrashedAt must be {"eq": null}: search does not exclude trashed assets
	// by default.
	TrashedAt eq[*string] `json:"trashedAt"`
	CreatedAt gte         `json:"createdAt"`
	IsEncoded *eq[bool]   `json:"isEncoded,omitempty"`
}

type eq[T any] struct {
	Eq T `json:"eq"`
}

type gte struct {
	Gte string `json:"gte"`
}

// searchVideos returns every page of videos on the timeline, not trashed and
// uploaded at or after since; only Ready ones if ready is set. The timeline
// visibility excludes motion-photo clips, which are hidden, and archived and
// locked assets.
func (c *Client) searchVideos(ctx context.Context, since time.Time, ready bool) ([]Asset, error) {
	body := searchBody{
		Filter: searchFilter{
			Type:       eq[string]{"VIDEO"},
			Visibility: eq[string]{"timeline"},
			CreatedAt:  gte{formatTime(since)},
		},
		Size: searchPageSize,
	}
	if ready {
		body.Filter.IsEncoded = &eq[bool]{true}
	}
	var assets []Asset
	for {
		var page struct {
			Assets struct {
				Items      []Asset `json:"items"`
				NextCursor *string `json:"nextCursor"`
			} `json:"assets"`
		}
		if err := c.call(ctx, http.MethodPost, "/api/search/metadata", body, &page); err != nil {
			return nil, err
		}
		assets = append(assets, page.Assets.Items...)
		if page.Assets.NextCursor == nil || *page.Assets.NextCursor == "" {
			return assets, nil
		}
		body.Cursor = *page.Assets.NextCursor
	}
}

// formatTime formats t as ISO 8601 in UTC with milliseconds, like JavaScript's
// toISOString. Truncating to milliseconds can only move t earlier, so a search
// from it never misses an asset.
func formatTime(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000Z")
}

// CreateShareLink creates a Share Link for one asset and returns its key.
// Viewers can download the original, which is what lets them play it in the
// browser. Immich only allows downloading when metadata is shown, so it is.
func (c *Client) CreateShareLink(ctx context.Context, assetID string) (string, error) {
	body := struct {
		Type          string   `json:"type"`
		AssetIDs      []string `json:"assetIds"`
		AllowDownload bool     `json:"allowDownload"`
		ShowMetadata  bool     `json:"showMetadata"`
		AllowUpload   bool     `json:"allowUpload"`
	}{"INDIVIDUAL", []string{assetID}, true, true, false}
	var link struct {
		Key           string `json:"key"`
		AllowDownload bool   `json:"allowDownload"`
	}
	if err := c.call(ctx, http.MethodPost, "/api/shared-links", body, &link); err != nil {
		return "", err
	}
	if link.Key == "" {
		return "", fmt.Errorf("immich POST /api/shared-links: response has no key")
	}
	// Without downloading, the link to the original would be dead.
	if !link.AllowDownload {
		return "", fmt.Errorf("immich POST /api/shared-links: Immich created the link without downloading allowed")
	}
	return link.Key, nil
}

// OriginalPath is the path of an asset's original file, which a Share Link's
// key opens when the link allows downloading.
func OriginalPath(assetID string) string {
	return "/api/assets/" + url.PathEscape(assetID) + "/original"
}

// TranscodeHeader reads the header of a Ready Video's Transcode from its first
// MiB.
func (c *Client) TranscodeHeader(ctx context.Context, assetID string) (mp4.Header, error) {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	resp, err := c.playback(ctx, assetID, fmt.Sprintf("bytes=0-%d", headerSize-1))
	if err != nil {
		return mp4.Header{}, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, headerSize))
	if err != nil {
		return mp4.Header{}, fmt.Errorf("immich GET %s: read response: %w", playbackPath(assetID), err)
	}
	h, err := mp4.ReadHeader(data)
	if err != nil {
		return mp4.Header{}, fmt.Errorf("immich GET %s: %w in the first %d bytes: %w", playbackPath(assetID), ErrUnreadableHeader, headerSize, err)
	}
	return h, nil
}

// ErrUnreadableHeader is returned by TranscodeHeader when the Transcode was
// downloaded but its header could not be read from it.
var ErrUnreadableHeader = errors.New("unreadable Transcode header")

// Transcode is an open stream of a Ready Video's whole Transcode.
type Transcode struct {
	io.ReadCloser
	// Size is the Transcode's Content-Length, or -1 if Immich sent none.
	Size int64
}

// OpenTranscode opens a stream of a Ready Video's whole Transcode, which the
// caller must close. Only waiting for the response is bounded by the call
// timeout: the stream is read as slowly as the upload goes.
func (c *Client) OpenTranscode(ctx context.Context, assetID string) (*Transcode, error) {
	ctx, cancel := context.WithCancelCause(ctx)
	errNoResponse := fmt.Errorf("immich GET %s: no response within %s", playbackPath(assetID), callTimeout)
	timer := time.AfterFunc(callTimeout, func() { cancel(errNoResponse) })
	resp, err := c.playback(ctx, assetID, "")
	if !timer.Stop() { // the timeout fired, during or just after the request
		if err == nil {
			resp.Body.Close()
		}
		err = errNoResponse
	}
	if err != nil {
		cancel(nil)
		return nil, err
	}
	body := &transcodeBody{resp.Body, playbackPath(assetID), func() { cancel(nil) }}
	return &Transcode{ReadCloser: body, Size: resp.ContentLength}, nil
}

// Preview returns a Video's preview image, not its Transcode, and the image's
// Content-Type, which is JPEG or WebP depending on Immich's config.
func (c *Client) Preview(ctx context.Context, assetID string) ([]byte, string, error) {
	path := "/api/assets/" + url.PathEscape(assetID) + "/thumbnail"
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path+"?size=preview", nil)
	if err != nil {
		return nil, "", fmt.Errorf("immich GET %s: %w", path, err)
	}
	req.Header.Set("x-api-key", c.apiKey)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("immich GET %s: %w", path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
	if err != nil {
		return nil, "", fmt.Errorf("immich GET %s: read response: %w", path, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, "", &Error{Method: http.MethodGet, Path: path, StatusCode: resp.StatusCode, Message: errorMessage(data)}
	}
	return data, resp.Header.Get("Content-Type"), nil
}

// playback requests a Video's Transcode, with a Range header unless
// byteRange is empty. For a Ready Video, Immich serves the Transcode rather
// than the original.
func (c *Client) playback(ctx context.Context, assetID, byteRange string) (*http.Response, error) {
	path := playbackPath(assetID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("immich GET %s: %w", path, err)
	}
	req.Header.Set("x-api-key", c.apiKey)
	if byteRange != "" {
		req.Header.Set("Range", byteRange)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("immich GET %s: %w", path, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		defer resp.Body.Close()
		data, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorSize))
		return nil, &Error{Method: http.MethodGet, Path: path, StatusCode: resp.StatusCode, Message: errorMessage(data)}
	}
	return resp, nil
}

func playbackPath(assetID string) string {
	return "/api/assets/" + url.PathEscape(assetID) + "/video/playback"
}

// transcodeBody is the body of a Transcode download. It names the request in
// read errors and releases the request's context once closed.
type transcodeBody struct {
	io.ReadCloser
	path   string
	cancel context.CancelFunc
}

func (b *transcodeBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil && err != io.EOF {
		err = fmt.Errorf("immich GET %s: read response: %w", b.path, err)
	}
	return n, err
}

func (b *transcodeBody) Close() error {
	err := b.ReadCloser.Close()
	b.cancel()
	return err
}

// Error is an error answered by Immich.
type Error struct {
	Method     string
	Path       string
	StatusCode int
	// Message is Immich's error message, if the response had one.
	Message string
}

func (e *Error) Error() string {
	msg := fmt.Sprintf("immich %s %s: HTTP %d %s", e.Method, e.Path, e.StatusCode, http.StatusText(e.StatusCode))
	if e.Message != "" {
		msg += ": " + e.Message
	}
	return msg
}

// call sends params, unless nil, as JSON to path and decodes the response into
// result.
func (c *Client) call(ctx context.Context, method, path string, params, result any) error {
	var reqBody io.Reader
	if params != nil {
		data, err := json.Marshal(params)
		if err != nil {
			return fmt.Errorf("immich %s %s: encode request: %w", method, path, err)
		}
		reqBody = bytes.NewReader(data)
	}
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reqBody)
	if err != nil {
		return fmt.Errorf("immich %s %s: %w", method, path, err)
	}
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("Accept", "application/json")
	if params != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("immich %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
	if err != nil {
		return fmt.Errorf("immich %s %s: read response: %w", method, path, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return &Error{Method: method, Path: path, StatusCode: resp.StatusCode, Message: errorMessage(data)}
	}
	if err := json.Unmarshal(data, result); err != nil {
		return fmt.Errorf("immich %s %s: decode response: %w", method, path, err)
	}
	return nil
}

// errorMessage extracts the message from an Immich error body, which is a
// string or, for validation errors, a list of strings.
func errorMessage(body []byte) string {
	var e struct {
		Message json.RawMessage `json:"message"`
	}
	if json.Unmarshal(body, &e) != nil || e.Message == nil {
		return ""
	}
	var msg string
	if json.Unmarshal(e.Message, &msg) == nil {
		return msg
	}
	var msgs []string
	if json.Unmarshal(e.Message, &msgs) == nil {
		return strings.Join(msgs, "; ")
	}
	return string(e.Message)
}

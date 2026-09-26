// Package immich is a thin client for the few Immich API endpoints the service
// uses. It hides pagination and response shapes behind domain-level
// operations.
package immich

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	// maxResponseSize bounds how much of an API response is read. A search
	// page of 1000 assets is well below it.
	maxResponseSize = 32 << 20
	// callTimeout bounds each API call, so that a hung Immich fails the call
	// instead of blocking the service.
	callTimeout = 30 * time.Second
	// searchPageSize is the number of assets asked for per search page.
	searchPageSize = 1000
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
}

// SourceUser returns the id of the Source User: the user the API key belongs
// to.
func (c *Client) SourceUser(ctx context.Context) (string, error) {
	var user struct {
		ID string `json:"id"`
	}
	if err := c.call(ctx, http.MethodGet, "/api/users/me", nil, &user); err != nil {
		return "", err
	}
	if user.ID == "" {
		return "", fmt.Errorf("immich GET /api/users/me: response has no user id")
	}
	return user.ID, nil
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
// Viewers can download the original but never see its metadata.
func (c *Client) CreateShareLink(ctx context.Context, assetID string) (string, error) {
	body := struct {
		Type          string   `json:"type"`
		AssetIDs      []string `json:"assetIds"`
		AllowDownload bool     `json:"allowDownload"`
		ShowMetadata  bool     `json:"showMetadata"`
		AllowUpload   bool     `json:"allowUpload"`
	}{"INDIVIDUAL", []string{assetID}, true, false, false}
	var link struct {
		Key string `json:"key"`
	}
	if err := c.call(ctx, http.MethodPost, "/api/shared-links", body, &link); err != nil {
		return "", err
	}
	if link.Key == "" {
		return "", fmt.Errorf("immich POST /api/shared-links: response has no key")
	}
	return link.Key, nil
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

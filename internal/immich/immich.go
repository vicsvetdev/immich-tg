// Package immich is a thin client for the few Immich API endpoints the
// service uses.
package immich

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	// maxResponseSize bounds how much of an API response is read.
	maxResponseSize = 1 << 20
	// callTimeout bounds an API call, so that a hung Immich fails the call
	// instead of blocking the service.
	callTimeout = 30 * time.Second
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
	err := c.get(ctx, "/api/server/version", &v)
	return v, err
}

// KeyPermissions returns the permissions of the client's API key, such as
// "asset.read", or "all" for an unrestricted key.
func (c *Client) KeyPermissions(ctx context.Context) ([]string, error) {
	var key struct {
		Permissions []string `json:"permissions"`
	}
	err := c.get(ctx, "/api/api-keys/me", &key)
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
	if err := c.get(ctx, "/api/users/me", &u); err != nil {
		return User{}, err
	}
	if u.ID == "" {
		return User{}, fmt.Errorf("immich GET /api/users/me: no user id in response")
	}
	return u, nil
}

// Error is an error answered by Immich.
type Error struct {
	Method     string
	Path       string
	StatusCode int
	// Message is Immich's explanation, empty if the body had none.
	Message string
}

func (e *Error) Error() string {
	msg := e.Message
	if msg == "" {
		msg = http.StatusText(e.StatusCode)
	}
	return fmt.Sprintf("immich %s %s: HTTP %d %s", e.Method, e.Path, e.StatusCode, msg)
}

// get fetches path and decodes the JSON response into result.
func (c *Client) get(ctx context.Context, path string, result any) error {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return fmt.Errorf("immich GET %s: %w", path, err)
	}
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("immich GET %s: %w", path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
	if err != nil {
		return fmt.Errorf("immich GET %s: read response: %w", path, err)
	}
	if resp.StatusCode != http.StatusOK {
		return &Error{Method: http.MethodGet, Path: path, StatusCode: resp.StatusCode, Message: errorMessage(data)}
	}
	if err := json.Unmarshal(data, result); err != nil {
		return fmt.Errorf("immich GET %s: decode response: %w", path, err)
	}
	return nil
}

// errorMessage extracts the message from an Immich error body, which is
// either a string or, for validation errors, a list of strings.
func errorMessage(body []byte) string {
	var e struct {
		Message json.RawMessage `json:"message"`
	}
	if json.Unmarshal(body, &e) != nil || e.Message == nil {
		return ""
	}
	var one string
	if json.Unmarshal(e.Message, &one) == nil {
		return one
	}
	var many []string
	if json.Unmarshal(e.Message, &many) == nil {
		return strings.Join(many, "; ")
	}
	return ""
}

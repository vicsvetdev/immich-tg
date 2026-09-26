// Package fakeimmich is an in-test Immich server. It serves the endpoints the
// service uses from state the test can change, and records every request.
package fakeimmich

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
)

// SourceUserID is the id of the Source User, whose API key the service uses.
const SourceUserID = "11111111-1111-1111-1111-111111111111"

// Request is one request as the server received it.
type Request struct {
	Method string
	Path   string
	Query  url.Values
	Header http.Header
	Body   []byte
}

// Version is an Immich server version.
type Version struct {
	Major, Minor, Patch int
}

// Server is a fake Immich instance for one API key.
type Server struct {
	apiKey string
	srv    *httptest.Server
	mux    *http.ServeMux

	mu          sync.Mutex
	requests    []Request
	version     Version
	permissions []string
	lib         library
	failures    map[string]failure

	// closed is closed when the test ends, releasing stalled downloads.
	closed chan struct{}
}

type failure struct {
	status  int
	message string
}

// New starts a fake Immich accepting apiKey. It reports a supported version
// and a key with every permission. It is closed when the test ends.
func New(t testing.TB, apiKey string) *Server {
	s := &Server{
		apiKey:      apiKey,
		version:     Version{3, 2, 0},
		permissions: []string{"all"},
		failures:    map[string]failure{},
		closed:      make(chan struct{}),
	}
	s.mux = http.NewServeMux()
	s.mux.HandleFunc("GET /api/server/version", s.serverVersion)
	s.mux.HandleFunc("GET /api/users/me", s.authed(s.usersMe))
	s.mux.HandleFunc("GET /api/api-keys/me", s.authed(s.apiKeysMe))
	s.serveLibrary()
	s.srv = httptest.NewServer(http.HandlerFunc(s.handle))
	t.Cleanup(s.srv.Close)
	t.Cleanup(func() { close(s.closed) }) // before Close, which waits for the handlers
	return s
}

// URL is the base URL to use as IMMICH_URL.
func (s *Server) URL() string { return s.srv.URL }

// Requests returns every request received so far, in order.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Request(nil), s.requests...)
}

// SetVersion changes the reported server version.
func (s *Server) SetVersion(v Version) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.version = v
}

// SetPermissions changes the API key's permissions.
func (s *Server) SetPermissions(permissions ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.permissions = permissions
}

// Fail makes every later request to route, e.g. "GET /api/users/me", answer
// with an Immich error of the given status and message, whatever its API key.
func (s *Server) Fail(route string, status int, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failures[route] = failure{status, message}
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	s.mu.Lock()
	s.requests = append(s.requests, Request{
		Method: r.Method,
		Path:   r.URL.Path,
		Query:  r.URL.Query(),
		Header: r.Header.Clone(),
		Body:   body,
	})
	f, fail := s.failures[r.Method+" "+r.URL.Path]
	s.mu.Unlock()
	if fail {
		writeJSON(w, f.status, map[string]any{
			"message": f.message, "error": http.StatusText(f.status), "statusCode": f.status,
		})
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	s.mux.ServeHTTP(w, r)
}

// authed rejects requests without the right x-api-key, like Immich does.
func (s *Server) authed(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != s.apiKey {
			writeJSON(w, http.StatusUnauthorized, map[string]any{
				"message": "Invalid API key", "error": "Unauthorized", "statusCode": http.StatusUnauthorized,
			})
			return
		}
		h(w, r)
	}
}

func (s *Server) serverVersion(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	v := s.version
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"major": v.Major, "minor": v.Minor, "patch": v.Patch})
}

func (s *Server) usersMe(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"id": SourceUserID, "email": "operator@example.com", "name": "Operator"})
}

func (s *Server) apiKeysMe(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	permissions := append([]string(nil), s.permissions...)
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"id": "api-key-1", "name": "immich-tg", "permissions": permissions})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

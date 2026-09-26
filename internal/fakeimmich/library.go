package fakeimmich

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Asset is an asset in the fake library. Zero values mean the Source User's
// video on the timeline.
type Asset struct {
	ID string
	// OwnerID is the owner's user id. Default: SourceUserID.
	OwnerID string
	// Type is IMAGE or VIDEO. Default: VIDEO.
	Type string
	// Visibility is timeline, archive, hidden (motion-photo clips) or locked.
	// Default: timeline.
	Visibility string
	// CreatedAt is the upload time.
	CreatedAt time.Time
	// LocalDateTime is the Recording Date, served with a Z suffix like Immich
	// does. Default: CreatedAt.
	LocalDateTime    time.Time
	OriginalFileName string
	Trashed          bool
	// Transcoded is set once the asset has a Transcode (Immich's isEncoded).
	Transcoded bool
	// Transcode is the file served as the Transcode. Default: LandscapeMP4.
	Transcode []byte
	// Duration is the video's duration, served in milliseconds.
	Duration time.Duration

	// The rest change how the whole Transcode is downloaded. Ranged requests,
	// which read the header, are always served normally.

	// DownloadFailure, if non-zero, is the status a download fails with.
	DownloadFailure int
	// ContentLength, if non-zero, is the Content-Length a download declares.
	// The download still carries only Transcode, so a larger ContentLength
	// cuts it short.
	ContentLength int64
	// NoContentLength makes a download come without a Content-Length.
	NoContentLength bool
	// Stall makes a download stop after half the Transcode and hang until the
	// client goes away.
	Stall bool
}

// library is the fake's asset state. Server.mu guards it.
type library struct {
	assets []Asset
	// pageSize caps the search page size, to exercise pagination.
	pageSize int
	// shareLinkFailure, if non-zero, is the status Share Link creation fails
	// with.
	shareLinkFailure int
	shareLinks       int
}

// maxPageSize is Immich's maximum search page size.
const maxPageSize = 1000

func (s *Server) serveLibrary() {
	s.lib.pageSize = maxPageSize
	s.mux.HandleFunc("POST /api/search/metadata", s.authed(s.searchMetadata))
	s.mux.HandleFunc("POST /api/shared-links", s.authed(s.createSharedLink))
	s.mux.HandleFunc("GET /api/assets/{id}/video/playback", s.authed(s.videoPlayback))
}

// AddAsset adds a to the library.
func (s *Server) AddAsset(a Asset) {
	if a.OwnerID == "" {
		a.OwnerID = SourceUserID
	}
	if a.Type == "" {
		a.Type = "VIDEO"
	}
	if a.Visibility == "" {
		a.Visibility = "timeline"
	}
	if a.LocalDateTime.IsZero() {
		a.LocalDateTime = a.CreatedAt
	}
	if a.OriginalFileName == "" {
		a.OriginalFileName = "PXL_" + a.ID + ".mp4"
	}
	if a.Transcode == nil {
		a.Transcode = LandscapeMP4
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lib.assets = append(s.lib.assets, a)
}

// SetTranscoded gives the asset with the given id a Transcode.
func (s *Server) SetTranscoded(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.lib.assets {
		if s.lib.assets[i].ID == id {
			s.lib.assets[i].Transcoded = true
			return
		}
	}
	panic("fakeimmich: no asset " + id)
}

// SetPageSize caps the number of assets per search page.
func (s *Server) SetPageSize(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lib.pageSize = n
}

// FailShareLinks makes Share Link creation fail with status.
func (s *Server) FailShareLinks(status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lib.shareLinkFailure = status
}

// ShareLinkKey is the key of the n-th Share Link created, counting from 1.
func ShareLinkKey(n int) string { return fmt.Sprintf("share-key-%d", n) }

// searchRequest is a metadata search in the filter format of Immich 3.2. Like
// Immich, the fake rejects unknown keys, including the deprecated flat fields.
type searchRequest struct {
	Filter *searchFilter `json:"filter"`
	Size   int           `json:"size"`
	Cursor string        `json:"cursor"`
}

type searchFilter struct {
	Type *struct {
		Eq string `json:"eq"`
	} `json:"type"`
	Visibility *struct {
		Eq string `json:"eq"`
	} `json:"visibility"`
	TrashedAt *struct {
		Eq *string `json:"eq"`
	} `json:"trashedAt"`
	CreatedAt *struct {
		Gte *time.Time `json:"gte"`
	} `json:"createdAt"`
	IsEncoded *struct {
		Eq bool `json:"eq"`
	} `json:"isEncoded"`
}

func (s *Server) searchMetadata(w http.ResponseWriter, r *http.Request) {
	var req searchRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		badRequest(w, err.Error())
		return
	}
	if req.Filter == nil {
		badRequest(w, "filter is required")
		return
	}
	if req.Size < 1 || req.Size > maxPageSize {
		badRequest(w, fmt.Sprintf("size must be between 1 and %d", maxPageSize))
		return
	}
	if f := req.Filter.TrashedAt; f != nil && f.Eq != nil {
		badRequest(w, "trashedAt.eq must be null")
		return
	}
	offset := 0
	if req.Cursor != "" {
		n, err := strconv.Atoi(strings.TrimPrefix(req.Cursor, "cursor-"))
		if err != nil || !strings.HasPrefix(req.Cursor, "cursor-") {
			badRequest(w, "invalid cursor")
			return
		}
		offset = n
	}

	s.mu.Lock()
	var matches []Asset
	for _, a := range s.lib.assets {
		if req.Filter.matches(a) {
			matches = append(matches, a)
		}
	}
	size := min(req.Size, s.lib.pageSize)
	s.mu.Unlock()

	// Immich orders search results by recording date, newest first, not by
	// upload time.
	slices.SortStableFunc(matches, func(a, b Asset) int { return b.LocalDateTime.Compare(a.LocalDateTime) })

	page := matches[min(offset, len(matches)):min(offset+size, len(matches))]
	var nextCursor any
	if offset+size < len(matches) {
		nextCursor = fmt.Sprintf("cursor-%d", offset+size)
	}
	items := make([]map[string]any, 0, len(page))
	for _, a := range page {
		items = append(items, assetJSON(a))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"albums": map[string]any{"total": 0, "count": 0, "items": []any{}, "facets": []any{}},
		"assets": map[string]any{
			"total": len(page), "count": len(page), "items": items, "facets": []any{},
			"nextPage": nil, "nextCursor": nextCursor,
		},
	})
}

// matches applies the filter like Immich does: a missing condition matches
// everything, so trashed, hidden and archived assets are included unless
// filtered out.
func (f *searchFilter) matches(a Asset) bool {
	switch {
	case f.Type != nil && f.Type.Eq != a.Type:
		return false
	case f.Visibility != nil && f.Visibility.Eq != a.Visibility:
		return false
	case f.TrashedAt != nil && a.Trashed:
		return false
	case f.CreatedAt != nil && f.CreatedAt.Gte != nil && a.CreatedAt.Before(*f.CreatedAt.Gte):
		return false
	case f.IsEncoded != nil && f.IsEncoded.Eq != a.Transcoded:
		return false
	}
	return true
}

func assetJSON(a Asset) map[string]any {
	return map[string]any{
		"id":               a.ID,
		"ownerId":          a.OwnerID,
		"type":             a.Type,
		"visibility":       a.Visibility,
		"isTrashed":        a.Trashed,
		"createdAt":        a.CreatedAt.UTC().Format(time.RFC3339Nano),
		"localDateTime":    a.LocalDateTime.UTC().Format(time.RFC3339Nano),
		"originalFileName": a.OriginalFileName,
		"duration":         a.Duration.Milliseconds(),
	}
}

func (s *Server) createSharedLink(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Type          string   `json:"type"`
		AssetIDs      []string `json:"assetIds"`
		AllowDownload *bool    `json:"allowDownload"`
		ShowMetadata  *bool    `json:"showMetadata"`
		AllowUpload   *bool    `json:"allowUpload"`
		ExpiresAt     *string  `json:"expiresAt"`
		Password      *string  `json:"password"`
		Slug          *string  `json:"slug"`
		Description   *string  `json:"description"`
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		badRequest(w, err.Error())
		return
	}
	if req.Type != "INDIVIDUAL" || len(req.AssetIDs) == 0 {
		badRequest(w, "an INDIVIDUAL link needs assetIds")
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lib.shareLinkFailure != 0 {
		writeJSON(w, s.lib.shareLinkFailure, map[string]any{
			"message": "Share Link creation failed", "error": http.StatusText(s.lib.shareLinkFailure),
			"statusCode": s.lib.shareLinkFailure,
		})
		return
	}
	s.lib.shareLinks++
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":   fmt.Sprintf("link-%d", s.lib.shareLinks),
		"key":  ShareLinkKey(s.lib.shareLinks),
		"type": req.Type,
	})
}

// videoPlayback serves an asset's Transcode. Like Immich, it supports Range
// requests and sets Content-Length.
func (s *Server) videoPlayback(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	var asset *Asset
	for i, a := range s.lib.assets {
		if a.ID == r.PathValue("id") {
			asset = &s.lib.assets[i]
		}
	}
	var a Asset
	if asset != nil {
		a = *asset
	}
	s.mu.Unlock()
	if asset == nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"message": "Not found or no asset.view access", "error": "Bad Request", "statusCode": http.StatusBadRequest,
		})
		return
	}
	w.Header().Set("Content-Type", "video/mp4")
	if r.Header.Get("Range") != "" {
		http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(a.Transcode))
		return
	}

	switch {
	case a.DownloadFailure != 0:
		writeJSON(w, a.DownloadFailure, map[string]any{
			"message": "Download failed", "error": http.StatusText(a.DownloadFailure), "statusCode": a.DownloadFailure,
		})
		return
	case a.Stall:
		w.Header().Set("Content-Length", strconv.Itoa(len(a.Transcode)))
		w.Write(a.Transcode[:len(a.Transcode)/2])
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
		case <-s.closed:
		}
		return
	}
	switch {
	case a.NoContentLength:
		// Flushing before the first write sends the body chunked.
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
	case a.ContentLength != 0:
		w.Header().Set("Content-Length", strconv.FormatInt(a.ContentLength, 10))
	default:
		w.Header().Set("Content-Length", strconv.Itoa(len(a.Transcode)))
	}
	w.Write(a.Transcode)
}

// PlaybackRequests returns the requests for Transcodes received so far, in
// order.
func (s *Server) PlaybackRequests() []Request {
	var out []Request
	for _, r := range s.Requests() {
		if r.Method == http.MethodGet && strings.HasSuffix(r.Path, "/video/playback") {
			out = append(out, r)
		}
	}
	return out
}

func badRequest(w http.ResponseWriter, msg string) {
	writeJSON(w, http.StatusBadRequest, map[string]any{
		"message": []string{msg}, "error": "Bad Request", "statusCode": http.StatusBadRequest,
	})
}

// SearchBodies returns the bodies of the metadata searches received so far, in
// order, decoded as generic JSON.
func (s *Server) SearchBodies() []map[string]any {
	return s.bodies(http.MethodPost, "/api/search/metadata")
}

// ShareLinkBodies returns the bodies of the Share Link creations received so
// far, in order, decoded as generic JSON.
func (s *Server) ShareLinkBodies() []map[string]any {
	return s.bodies(http.MethodPost, "/api/shared-links")
}

func (s *Server) bodies(method, path string) []map[string]any {
	var out []map[string]any
	for _, r := range s.Requests() {
		if r.Method != method || r.Path != path {
			continue
		}
		var body map[string]any
		if err := json.Unmarshal(r.Body, &body); err != nil {
			body = map[string]any{"<invalid JSON>": string(r.Body)}
		}
		out = append(out, body)
	}
	return out
}

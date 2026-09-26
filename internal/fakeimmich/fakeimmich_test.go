package fakeimmich_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"immich-tg/internal/fakeimmich"
)

func get(t *testing.T, url, apiKey string) (*http.Response, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	if apiKey != "" {
		req.Header.Set("x-api-key", apiKey)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body map[string]any
	json.NewDecoder(resp.Body).Decode(&body)
	return resp, body
}

func TestRequiresAPIKeyAndRecordsRequests(t *testing.T) {
	s := fakeimmich.New(t, "key")

	if resp, _ := get(t, s.URL()+"/api/users/me", "wrong"); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("wrong key: status = %d, want 401", resp.StatusCode)
	}
	resp, body := get(t, s.URL()+"/api/users/me", "key")
	if resp.StatusCode != http.StatusOK || body["id"] != fakeimmich.SourceUserID {
		t.Errorf("users/me = %d %v", resp.StatusCode, body)
	}

	reqs := s.Requests()
	if len(reqs) != 2 || reqs[1].Path != "/api/users/me" || reqs[1].Header.Get("x-api-key") != "key" {
		t.Errorf("requests = %+v", reqs)
	}
}

func TestReportsConfiguredVersionAndPermissions(t *testing.T) {
	s := fakeimmich.New(t, "key")
	s.SetVersion(fakeimmich.Version{Major: 3, Minor: 1, Patch: 4})
	s.SetPermissions("asset.read", "user.read")

	_, version := get(t, s.URL()+"/api/server/version", "")
	if version["major"] != 3.0 || version["minor"] != 1.0 || version["patch"] != 4.0 {
		t.Errorf("version = %v", version)
	}
	_, key := get(t, s.URL()+"/api/api-keys/me", "key")
	perms, _ := key["permissions"].([]any)
	if len(perms) != 2 || perms[0] != "asset.read" || perms[1] != "user.read" {
		t.Errorf("permissions = %v", key["permissions"])
	}
}

func TestFailAnswersRouteWithImmichError(t *testing.T) {
	s := fakeimmich.New(t, "key")
	s.Fail("GET /api/users/me", http.StatusForbidden, "Missing required permission: user.read")

	resp, body := get(t, s.URL()+"/api/users/me", "key")
	if resp.StatusCode != http.StatusForbidden || body["message"] != "Missing required permission: user.read" {
		t.Errorf("users/me = %d %v", resp.StatusCode, body)
	}
	if resp, _ := get(t, s.URL()+"/api/api-keys/me", "key"); resp.StatusCode != http.StatusOK {
		t.Errorf("other route: status = %d, want 200", resp.StatusCode)
	}
	if n := len(s.Requests()); n != 2 {
		t.Errorf("recorded %d requests, want 2", n)
	}
}

func TestServesTranscodeWithRanges(t *testing.T) {
	s := fakeimmich.New(t, "key")
	s.AddAsset(fakeimmich.Asset{ID: "video", Transcode: fakeimmich.PortraitMP4})
	url := s.URL() + "/api/assets/video/video/playback"

	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("x-api-key", "key")
	req.Header.Set("Range", "bytes=0-99")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent || !bytes.Equal(data, fakeimmich.PortraitMP4[:100]) {
		t.Errorf("ranged playback = %d with %d bytes, want 206 with the first 100", resp.StatusCode, len(data))
	}

	req, _ = http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("x-api-key", "key")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	data, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.ContentLength != int64(len(fakeimmich.PortraitMP4)) || !bytes.Equal(data, fakeimmich.PortraitMP4) {
		t.Errorf("playback = %d with %d bytes and Content-Length %d, want 200 with the whole Transcode",
			resp.StatusCode, len(data), resp.ContentLength)
	}
	if n := len(s.PlaybackRequests()); n != 2 {
		t.Errorf("recorded %d playback requests, want 2", n)
	}
}

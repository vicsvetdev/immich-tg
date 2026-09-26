package fakeimmich_test

import (
	"encoding/json"
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

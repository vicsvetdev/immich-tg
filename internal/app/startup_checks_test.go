package app_test

import (
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"

	"immich-tg/internal/fakeimmich"
	"immich-tg/internal/faketelegram"
)

const (
	pathVersion = "/api/server/version"
	pathKey     = "/api/api-keys/me"
	pathUser    = "/api/users/me"
)

func TestStartupChecksRunInOrderBeforeTheStartedMessage(t *testing.T) {
	h := newHarness(t)

	h.start()
	h.poll()

	reqs := h.immich.Requests()
	if got := immichPaths(reqs); len(got) < 3 || !slices.Equal(got[:3], []string{pathVersion, pathKey, pathUser}) {
		t.Errorf("first Immich requests = %v, want %v", got, []string{pathVersion, pathKey, pathUser})
	}
	for _, r := range reqs {
		if r.Header.Get("x-api-key") != apiKey {
			t.Errorf("%s %s: x-api-key = %q, want the API key", r.Method, r.Path, r.Header.Get("x-api-key"))
		}
	}

	calls := h.telegram.Calls()
	want := []string{"getMe", "getChatMember", "getChatMember", "sendMessage"}
	if got := telegramMethods(calls); len(got) < 4 || !slices.Equal(got[:4], want) {
		t.Fatalf("first Telegram calls = %v, want %v", got, want)
	}
	for i, chat := range []string{videoChannel, logChannel} {
		c := calls[1+i]
		if c.ChatID != chat || c.Fields["user_id"] != strconv.Itoa(faketelegram.BotID) {
			t.Errorf("getChatMember %d: chat_id = %s, user_id = %s, want %s and the bot's id %d",
				i+1, c.ChatID, c.Fields["user_id"], chat, faketelegram.BotID)
		}
	}
	if started := h.logLine("started"); started["source_user_id"] != fakeimmich.SourceUserID {
		t.Errorf("source_user_id = %v, want %s", started["source_user_id"], fakeimmich.SourceUserID)
	}
}

func TestSupportedImmichVersions(t *testing.T) {
	tests := []struct {
		version   fakeimmich.Version
		supported bool
	}{
		{fakeimmich.Version{Major: 2, Minor: 9, Patch: 9}, false},
		{fakeimmich.Version{Major: 3, Minor: 1, Patch: 9}, false},
		{fakeimmich.Version{Major: 3, Minor: 2, Patch: 0}, true},
		{fakeimmich.Version{Major: 3, Minor: 2, Patch: 1}, true},
		{fakeimmich.Version{Major: 3, Minor: 10, Patch: 0}, true},
		{fakeimmich.Version{Major: 4, Minor: 0, Patch: 0}, true},
	}
	for _, tt := range tests {
		v := tt.version
		name := "v" + strconv.Itoa(v.Major) + "." + strconv.Itoa(v.Minor) + "." + strconv.Itoa(v.Patch)
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.immich.SetVersion(v)

			if !tt.supported {
				h.wantStartupFailure("Immich " + name + " is not supported, immich-tg needs v3.2.0 or newer")
				return
			}
			h.start()
			h.poll()
			if code := h.stop(); code != 0 {
				t.Errorf("exit code = %d, want 0", code)
			}
		})
	}
}

func TestAPIKeyPermissions(t *testing.T) {
	tests := []struct {
		name        string
		permissions []string
		wantMissing string // empty if the key is enough
	}{
		{"all", []string{"all"}, ""},
		{"exactly the required scopes", []string{"user.read", "asset.read", "asset.view", "asset.share", "sharedLink.create"}, ""},
		{"required scopes and more", []string{"album.read", "sharedLink.create", "asset.share", "asset.view", "asset.read", "user.read"}, ""},
		{"missing two scopes", []string{"user.read", "asset.read", "asset.view"}, "asset.share, sharedLink.create"},
		{"missing one scope", []string{"user.read", "asset.read", "asset.view", "asset.share"}, "sharedLink.create"},
		{"no scopes", nil, "user.read, asset.read, asset.view, asset.share, sharedLink.create"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			h.immich.SetPermissions(tt.permissions...)

			if tt.wantMissing != "" {
				h.wantStartupFailure("IMMICH_API_KEY is missing the permissions " + tt.wantMissing)
				return
			}
			h.start()
			h.poll()
			if code := h.stop(); code != 0 {
				t.Errorf("exit code = %d, want 0", code)
			}
		})
	}
}

// TestFailedStartupChecks covers a failing case of each check. The check
// fails with its message, and none after it runs.
func TestFailedStartupChecks(t *testing.T) {
	tests := []struct {
		name  string
		setup func(h *harness)
		want  []string
		// wantImmich and wantTelegram are every Immich request path and
		// Telegram method called, in order.
		wantImmich   []string
		wantTelegram []string
	}{
		{
			name:  "Immich unreachable",
			setup: func(h *harness) { h.env["IMMICH_URL"] = unreachableURL(h.t) },
			want:  []string{"Immich is not reachable at IMMICH_URL", "connection refused"},
		},
		{
			name:       "Immich version unavailable",
			setup:      func(h *harness) { h.immich.Fail("GET "+pathVersion, http.StatusBadGateway, "") },
			want:       []string{"could not read the server version of Immich at IMMICH_URL", "HTTP 502 Bad Gateway"},
			wantImmich: []string{pathVersion},
		},
		{
			name:       "Immich too old",
			setup:      func(h *harness) { h.immich.SetVersion(fakeimmich.Version{Major: 3, Minor: 1, Patch: 4}) },
			want:       []string{"Immich v3.1.4 is not supported, immich-tg needs v3.2.0 or newer"},
			wantImmich: []string{pathVersion},
		},
		{
			name:       "API key rejected",
			setup:      func(h *harness) { h.env["IMMICH_API_KEY"] = "wrong-key" },
			want:       []string{"could not read the permissions of IMMICH_API_KEY", "HTTP 401 Invalid API key"},
			wantImmich: []string{pathVersion, pathKey},
		},
		{
			name:       "API key missing scopes",
			setup:      func(h *harness) { h.immich.SetPermissions("user.read", "asset.read", "asset.view") },
			want:       []string{"IMMICH_API_KEY is missing the permissions asset.share, sharedLink.create"},
			wantImmich: []string{pathVersion, pathKey},
		},
		{
			name: "Source User unavailable",
			setup: func(h *harness) {
				h.immich.Fail("GET "+pathUser, http.StatusInternalServerError, "Internal server error")
			},
			want:       []string{"could not resolve the Source User", "HTTP 500 Internal server error"},
			wantImmich: []string{pathVersion, pathKey, pathUser},
		},
		{
			name: "bot token rejected",
			setup: func(h *harness) {
				h.telegram.Enqueue("getMe", faketelegram.JSONError(http.StatusUnauthorized, "Unauthorized"))
			},
			want:         []string{"could not identify the bot, check TELEGRAM_BOT_TOKEN", "401 Unauthorized"},
			wantImmich:   []string{pathVersion, pathKey, pathUser},
			wantTelegram: []string{"getMe"},
		},
		{
			name: "Video Channel not found",
			setup: func(h *harness) {
				h.telegram.Enqueue("getChatMember", faketelegram.JSONError(http.StatusBadRequest, "Bad Request: chat not found"))
			},
			want:         []string{"could not check the bot in the Video Channel, TELEGRAM_VIDEO_CHANNEL_ID " + videoChannel, "chat not found"},
			wantImmich:   []string{pathVersion, pathKey, pathUser},
			wantTelegram: []string{"getMe", "getChatMember"},
		},
		{
			name:  "bot is a plain member of the Video Channel",
			setup: func(h *harness) { h.telegram.SetBotMember(videoChannel, "member", false) },
			want: []string{
				"the bot cannot post to the Video Channel, TELEGRAM_VIDEO_CHANNEL_ID " + videoChannel,
				"it must be an administrator allowed to post messages, but its status is member",
			},
			wantImmich:   []string{pathVersion, pathKey, pathUser},
			wantTelegram: []string{"getMe", "getChatMember"},
		},
		{
			name:  "bot is a Video Channel administrator without the right to post",
			setup: func(h *harness) { h.telegram.SetBotMember(videoChannel, "administrator", false) },
			want: []string{
				"the bot cannot post to the Video Channel, TELEGRAM_VIDEO_CHANNEL_ID " + videoChannel,
				"its status is administrator and can_post_messages is false",
			},
			wantImmich:   []string{pathVersion, pathKey, pathUser},
			wantTelegram: []string{"getMe", "getChatMember"},
		},
		{
			name:  "bot has left the Log Channel",
			setup: func(h *harness) { h.telegram.SetBotMember(logChannel, "left", false) },
			want: []string{
				"the bot cannot post to the Log Channel, TELEGRAM_LOG_CHANNEL_ID " + logChannel,
				"its status is left",
			},
			wantImmich:   []string{pathVersion, pathKey, pathUser},
			wantTelegram: []string{"getMe", "getChatMember", "getChatMember"},
		},
		{
			name:  "bot is a Log Channel administrator without the right to post",
			setup: func(h *harness) { h.telegram.SetBotMember(logChannel, "administrator", false) },
			want: []string{
				"the bot cannot post to the Log Channel, TELEGRAM_LOG_CHANNEL_ID " + logChannel,
				"can_post_messages is false",
			},
			wantImmich:   []string{pathVersion, pathKey, pathUser},
			wantTelegram: []string{"getMe", "getChatMember", "getChatMember"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			tt.setup(h)

			h.wantStartupFailure(tt.want...)

			if got := immichPaths(h.immich.Requests()); !slices.Equal(got, tt.wantImmich) {
				t.Errorf("Immich requests = %v, want %v", got, tt.wantImmich)
			}
			if got := telegramMethods(h.telegram.Calls()); !slices.Equal(got, tt.wantTelegram) {
				t.Errorf("Telegram calls = %v, want %v", got, tt.wantTelegram)
			}
		})
	}
}

// wantStartupFailure runs the service and checks that startup fails with a
// non-zero exit, logging every one of want, and without the started message.
func (h *harness) wantStartupFailure(want ...string) {
	h.t.Helper()
	if code := h.run(); code == 0 {
		h.t.Errorf("exit code = 0, want non-zero")
	}
	failure := h.logLine("startup check failed")
	if failure["level"] != "ERROR" {
		h.t.Errorf("level = %v, want ERROR", failure["level"])
	}
	msg, _ := failure["error"].(string)
	for _, w := range want {
		if !strings.Contains(msg, w) {
			h.t.Errorf("error %q does not contain %q", msg, w)
		}
	}
	if calls := h.telegram.CallsTo(logChannel); len(calls) != 0 {
		h.t.Errorf("got Log Channel calls %+v, want none", calls)
	}
}

func immichPaths(reqs []fakeimmich.Request) []string {
	var paths []string
	for _, r := range reqs {
		paths = append(paths, r.Path)
	}
	return paths
}

func telegramMethods(calls []faketelegram.Call) []string {
	var methods []string
	for _, c := range calls {
		methods = append(methods, c.Method)
	}
	return methods
}

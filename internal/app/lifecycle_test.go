package app_test

import (
	"strings"
	"testing"

	"immich-tg/internal/faketelegram"
)

func TestStartedMessageGoesToLogChannel(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	h.start()
	h.poll()

	if calls := h.telegram.CallsTo(videoChannel); len(calls) != 0 {
		t.Errorf("got Video Channel calls %+v, want none", calls)
	}
	calls := h.telegram.CallsTo(logChannel)
	if len(calls) != 1 {
		t.Fatalf("got %d Log Channel calls, want 1: %+v", len(calls), calls)
	}
	got := calls[0]
	if got.Method != "sendMessage" {
		t.Errorf("method = %s, want sendMessage", got.Method)
	}
	want := "🟢 immich-tg started, watching uploads from 26 Sep 2026, 17:04:05 UTC"
	if got.Fields["text"] != want {
		t.Errorf("text = %q, want %q", got.Fields["text"], want)
	}
	if code := h.stop(); code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
}

func TestBadConfigFailsFast(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		key   string
		value string
	}{
		{"missing IMMICH_URL", "IMMICH_URL", ""},
		{"missing IMMICH_PUBLIC_URL", "IMMICH_PUBLIC_URL", ""},
		{"missing IMMICH_API_KEY", "IMMICH_API_KEY", ""},
		{"missing TELEGRAM_API_URL", "TELEGRAM_API_URL", ""},
		{"missing TELEGRAM_BOT_TOKEN", "TELEGRAM_BOT_TOKEN", ""},
		{"missing TELEGRAM_VIDEO_CHANNEL_ID", "TELEGRAM_VIDEO_CHANNEL_ID", ""},
		{"missing TELEGRAM_LOG_CHANNEL_ID", "TELEGRAM_LOG_CHANNEL_ID", ""},
		{"blank IMMICH_API_KEY", "IMMICH_API_KEY", "   "},
		{"IMMICH_URL without scheme", "IMMICH_URL", "immich:2283"},
		{"IMMICH_PUBLIC_URL not http", "IMMICH_PUBLIC_URL", "ftp://photos.example.com"},
		{"TELEGRAM_API_URL without host", "TELEGRAM_API_URL", "http://"},
		{"non-numeric TELEGRAM_VIDEO_CHANNEL_ID", "TELEGRAM_VIDEO_CHANNEL_ID", "my-channel"},
		{"bare @ TELEGRAM_LOG_CHANNEL_ID", "TELEGRAM_LOG_CHANNEL_ID", "@"},
		{"unparseable POLL_INTERVAL", "POLL_INTERVAL", "30"},
		{"zero POLL_INTERVAL", "POLL_INTERVAL", "0s"},
		{"negative WAIT_TIMEOUT", "WAIT_TIMEOUT", "-1h"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			h.env[tt.key] = tt.value

			if code := h.run(); code == 0 {
				t.Errorf("exit code = 0, want non-zero")
			}
			if out := h.stdout.String(); !strings.Contains(out, tt.key) {
				t.Errorf("output does not name %s:\n%s", tt.key, out)
			}
			if calls := h.telegram.Calls(); len(calls) != 0 {
				t.Errorf("got Telegram calls %+v, want none", calls)
			}
		})
	}
}

func TestBadConfigNamesEveryInvalidVariable(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.env["IMMICH_API_KEY"] = ""
	h.env["WAIT_TIMEOUT"] = "soon"

	if code := h.run(); code == 0 {
		t.Errorf("exit code = 0, want non-zero")
	}
	out := h.stdout.String()
	for _, key := range []string{"IMMICH_API_KEY", "WAIT_TIMEOUT"} {
		if !strings.Contains(out, key) {
			t.Errorf("output does not name %s:\n%s", key, out)
		}
	}
}

func TestOptionalVariables(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name             string
		env              map[string]string
		wantPollInterval string
		wantWaitTimeout  string
	}{
		{"defaults", nil, "30s", "2h0m0s"},
		{"overridden", map[string]string{"POLL_INTERVAL": "1m", "WAIT_TIMEOUT": "45m"}, "1m0s", "45m0s"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			for k, v := range tt.env {
				h.env[k] = v
			}

			h.start()
			h.poll()

			started := h.logLine("started")
			if started["poll_interval"] != tt.wantPollInterval {
				t.Errorf("poll_interval = %v, want %s", started["poll_interval"], tt.wantPollInterval)
			}
			if started["wait_timeout"] != tt.wantWaitTimeout {
				t.Errorf("wait_timeout = %v, want %s", started["wait_timeout"], tt.wantWaitTimeout)
			}
		})
	}
}

func TestLogsAreStructured(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	h.start()
	h.poll()
	h.stop()

	started := h.logLine("started")
	if started["level"] != "INFO" {
		t.Errorf("level = %v, want INFO", started["level"])
	}
	if started["watch_start"] != "2026-09-26T17:04:05Z" {
		t.Errorf("watch_start = %v, want 2026-09-26T17:04:05Z", started["watch_start"])
	}
}

func TestStartupFailsWhenStartedMessageIsRejected(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		reply   faketelegram.Reply
		wantLog string
	}{
		{"JSON error", faketelegram.JSONError(403, "Forbidden: bot is not a member of the channel chat"), "Forbidden: bot is not a member of the channel chat"},
		{"bare status", faketelegram.BareStatus(502), "HTTP 502"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			h.telegram.Enqueue("sendMessage", tt.reply)

			if code := h.run(); code == 0 {
				t.Errorf("exit code = 0, want non-zero")
			}
			if out := h.stdout.String(); !strings.Contains(out, tt.wantLog) {
				t.Errorf("output does not contain %q:\n%s", tt.wantLog, out)
			}
		})
	}
}

func TestBotTokenIsNotLoggedWhenTelegramIsUnreachable(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.env["TELEGRAM_API_URL"] = unreachableURL(t)

	if code := h.run(); code == 0 {
		t.Errorf("exit code = 0, want non-zero")
	}
	out := h.stdout.String()
	if !strings.Contains(out, "getMe") {
		t.Errorf("output does not mention the failed call:\n%s", out)
	}
	if strings.Contains(out, botToken) {
		t.Errorf("output contains the bot token:\n%s", out)
	}
}

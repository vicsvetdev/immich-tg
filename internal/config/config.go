// Package config loads and validates the service's configuration from
// environment variables.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Config is the validated configuration.
type Config struct {
	// ImmichURL is used for API calls and downloads, typically Immich's LAN
	// address. It has no trailing slash.
	ImmichURL string
	// ImmichPublicURL is the base of Share Links and of the Immich links in
	// Problem Reports. It has no trailing slash.
	ImmichPublicURL string
	ImmichAPIKey    string

	// TelegramAPIURL is the base URL of the Bot API server, without a
	// trailing slash.
	TelegramAPIURL   string
	TelegramBotToken string
	VideoChannelID   string
	LogChannelID     string

	PollInterval time.Duration
	WaitTimeout  time.Duration
}

const (
	DefaultPollInterval = 30 * time.Second
	DefaultWaitTimeout  = 2 * time.Hour
)

// Load reads the configuration through getenv. The error names every missing
// or invalid variable.
func Load(getenv func(string) string) (Config, error) {
	l := loader{getenv: getenv}
	c := Config{
		ImmichURL:        l.url("IMMICH_URL"),
		ImmichPublicURL:  l.url("IMMICH_PUBLIC_URL"),
		ImmichAPIKey:     l.secret("IMMICH_API_KEY"),
		TelegramAPIURL:   l.url("TELEGRAM_API_URL"),
		TelegramBotToken: l.secret("TELEGRAM_BOT_TOKEN"),
		VideoChannelID:   l.chatID("TELEGRAM_VIDEO_CHANNEL_ID"),
		LogChannelID:     l.chatID("TELEGRAM_LOG_CHANNEL_ID"),
		PollInterval:     l.duration("POLL_INTERVAL", DefaultPollInterval),
		WaitTimeout:      l.duration("WAIT_TIMEOUT", DefaultWaitTimeout),
	}
	if err := errors.Join(l.errs...); err != nil {
		return Config{}, err
	}
	return c, nil
}

type loader struct {
	getenv func(string) string
	errs   []error
}

func (l *loader) fail(format string, args ...any) {
	l.errs = append(l.errs, fmt.Errorf(format, args...))
}

// required returns the trimmed value of key, recording an error if it is
// empty.
func (l *loader) required(key string) (string, bool) {
	v := strings.TrimSpace(l.getenv(key))
	if v == "" {
		l.fail("%s is required", key)
		return "", false
	}
	return v, true
}

func (l *loader) secret(key string) string {
	v, _ := l.required(key)
	return v
}

func (l *loader) url(key string) string {
	v, ok := l.required(key)
	if !ok {
		return ""
	}
	u, err := url.Parse(v)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		l.fail("%s must be an absolute http or https URL, got %q", key, v)
		return ""
	}
	return strings.TrimRight(v, "/")
}

// chatID accepts a numeric chat ID such as -1001234567890 or a public
// channel username such as @mychannel.
func (l *loader) chatID(key string) string {
	v, ok := l.required(key)
	if !ok {
		return ""
	}
	if _, err := strconv.ParseInt(v, 10, 64); err != nil && (!strings.HasPrefix(v, "@") || len(v) < 2) {
		l.fail("%s must be a numeric chat ID such as -1001234567890 or a @channelname, got %q", key, v)
		return ""
	}
	return v
}

func (l *loader) duration(key string, fallback time.Duration) time.Duration {
	v := strings.TrimSpace(l.getenv(key))
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		l.fail("%s must be a positive duration such as %s, got %q", key, fallback, v)
		return 0
	}
	return d
}

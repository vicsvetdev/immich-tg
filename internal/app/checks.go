package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"immich-tg/internal/config"
	"immich-tg/internal/immich"
	"immich-tg/internal/telegram"
)

// minImmichVersion is the oldest supported Immich: the search filter format
// was introduced in 3.2.
var minImmichVersion = immich.Version{Major: 3, Minor: 2, Patch: 0}

// requiredPermissions are the API key scopes the service needs, unless the
// key has "all".
var requiredPermissions = []string{"user.read", "asset.read", "asset.view", "asset.share", "sharedLink.create"}

// checkError is a failed startup check.
type checkError struct {
	// check names the check, for the logs.
	check string
	err   error
}

func (e *checkError) Error() string { return e.err.Error() }
func (e *checkError) Unwrap() error { return e.err }

func checkFailed(check string, format string, args ...any) error {
	return &checkError{check: check, err: fmt.Errorf(format, args...)}
}

// runChecks runs the startup checks in order and stops at the first that
// fails. It returns the Source User.
func runChecks(ctx context.Context, cfg config.Config, im *immich.Client, tg *telegram.Client) (immich.User, error) {
	version, err := im.ServerVersion(ctx)
	if err != nil {
		if _, answered := errors.AsType[*immich.Error](err); answered {
			return immich.User{}, checkFailed("immich_version", "could not read the server version of Immich at IMMICH_URL %s: %w", cfg.ImmichURL, err)
		}
		return immich.User{}, checkFailed("immich_version", "Immich is not reachable at IMMICH_URL %s: %w", cfg.ImmichURL, err)
	}
	if !version.AtLeast(minImmichVersion) {
		return immich.User{}, checkFailed("immich_version", "Immich %s is not supported, immich-tg needs %s or newer", version, minImmichVersion)
	}

	permissions, err := im.KeyPermissions(ctx)
	if err != nil {
		return immich.User{}, checkFailed("api_key_permissions", "could not read the permissions of IMMICH_API_KEY: %w", err)
	}
	if missing := missingPermissions(permissions); len(missing) > 0 {
		return immich.User{}, checkFailed("api_key_permissions", "IMMICH_API_KEY is missing the permissions %s", strings.Join(missing, ", "))
	}

	user, err := im.SourceUser(ctx)
	if err != nil {
		return immich.User{}, checkFailed("source_user", "could not resolve the Source User, the user IMMICH_API_KEY belongs to: %w", err)
	}

	bot, err := tg.GetMe(ctx)
	if err != nil {
		return immich.User{}, checkFailed("bot_token", "could not identify the bot, check TELEGRAM_BOT_TOKEN: %w", err)
	}

	channels := []struct{ name, key, id string }{
		{"Video Channel", "TELEGRAM_VIDEO_CHANNEL_ID", cfg.VideoChannelID},
		{"Log Channel", "TELEGRAM_LOG_CHANNEL_ID", cfg.LogChannelID},
	}
	for _, ch := range channels {
		member, err := tg.GetChatMember(ctx, ch.id, bot.ID)
		if err != nil {
			return immich.User{}, checkFailed("channel_admin", "could not check the bot in the %s, %s %s: %w", ch.name, ch.key, ch.id, err)
		}
		if !member.CanPost() {
			return immich.User{}, checkFailed("channel_admin",
				"the bot cannot post to the %s, %s %s: it must be an administrator allowed to post messages, but its status is %s and can_post_messages is %t",
				ch.name, ch.key, ch.id, member.Status, member.CanPostMessages)
		}
	}
	return user, nil
}

// missingPermissions returns the required permissions that granted lacks, in
// the order of requiredPermissions.
func missingPermissions(granted []string) []string {
	if slices.Contains(granted, "all") {
		return nil
	}
	var missing []string
	for _, p := range requiredPermissions {
		if !slices.Contains(granted, p) {
			missing = append(missing, p)
		}
	}
	return missing
}

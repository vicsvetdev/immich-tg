package telegram

import (
	"context"
	"fmt"
)

// User is a Telegram user or bot.
type User struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
}

// GetMe identifies the bot, which confirms that the token is valid.
func (c *Client) GetMe(ctx context.Context) (User, error) {
	var u User
	if err := c.call(ctx, "getMe", struct{}{}, &u); err != nil {
		return User{}, err
	}
	if u.ID == 0 {
		return User{}, fmt.Errorf("telegram getMe: no bot id in result")
	}
	return u, nil
}

// ChatMember is a user's membership of a chat.
type ChatMember struct {
	// Status is e.g. "administrator", "member" or "left".
	Status string `json:"status"`
	// CanPostMessages is set for channel administrators allowed to post.
	CanPostMessages bool `json:"can_post_messages"`
}

// CanPost reports whether the member can publish to a channel.
func (m ChatMember) CanPost() bool {
	return m.Status == "administrator" && m.CanPostMessages
}

// GetChatMember returns the membership of userID in chatID.
func (c *Client) GetChatMember(ctx context.Context, chatID string, userID int64) (ChatMember, error) {
	params := struct {
		ChatID string `json:"chat_id"`
		UserID int64  `json:"user_id"`
	}{chatID, userID}
	var m ChatMember
	err := c.call(ctx, "getChatMember", params, &m)
	return m, err
}

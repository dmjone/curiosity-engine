package discord

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const apiBase = "https://discord.com/api/v10"

// REST is a tiny Discord REST client. It is used only on the daily-cron path
// (posting problems, registering commands); interaction replies are returned
// inline as the webhook HTTP response and need no token.
type REST struct {
	token  string
	client *http.Client
}

// NewREST builds a client authenticated with a bot token.
func NewREST(token string) *REST {
	return &REST{token: token, client: &http.Client{Timeout: 20 * time.Second}}
}

func (r *REST) do(ctx context.Context, method, path string, body any) ([]byte, error) {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, apiBase+path, rdr)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bot "+r.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return rb, fmt.Errorf("discord %s %s: status %d: %s", method, path, resp.StatusCode, string(rb))
	}
	return rb, nil
}

// MessagePayload is the body for posting a channel message.
type MessagePayload struct {
	Content    string      `json:"content,omitempty"`
	Embeds     []Embed     `json:"embeds,omitempty"`
	Components []ActionRow `json:"components,omitempty"`
}

// PostMessage publishes a message to a channel.
func (r *REST) PostMessage(ctx context.Context, channelID string, m MessagePayload) error {
	_, err := r.do(ctx, http.MethodPost, "/channels/"+channelID+"/messages", m)
	return err
}

// AppCommand describes a global slash command for bulk registration.
type AppCommand struct {
	Name        string             `json:"name"`
	Description string             `json:"description"`
	Type        int                `json:"type,omitempty"`
	Options     []AppCommandOption `json:"options,omitempty"`
}

// AppCommandOption is one slash-command argument.
type AppCommandOption struct {
	Type        int                `json:"type"`
	Name        string             `json:"name"`
	Description string             `json:"description"`
	Required    bool               `json:"required,omitempty"`
	Choices     []AppCommandChoice `json:"choices,omitempty"`
}

// AppCommandChoice is a fixed choice for an option.
type AppCommandChoice struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// RegisterCommands bulk-overwrites the application's global slash commands.
// The PUT is idempotent, so the daily cron can safely call it every run.
func (r *REST) RegisterCommands(ctx context.Context, appID string, cmds []AppCommand) error {
	_, err := r.do(ctx, http.MethodPut, "/applications/"+appID+"/commands", cmds)
	return err
}

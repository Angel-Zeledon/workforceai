package slackapp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultAPIBase is the Slack Web API.
const DefaultAPIBase = "https://slack.com/api"

// Client calls the Slack Web API with the organization's bot token. The token is a parameter of every call and
// is never logged or put into an error.
type Client struct {
	Base string // default DefaultAPIBase (tests point it to a fake)
	HTTP *http.Client
}

func (c *Client) base() string {
	if c == nil || c.Base == "" {
		return DefaultAPIBase
	}
	return strings.TrimRight(c.Base, "/")
}

func (c *Client) client() *http.Client {
	if c != nil && c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// APIError is an error answer of Slack ({"ok": false, "error": "<code>"}). Code is Slack's stable code.
type APIError struct{ Code string }

func (e *APIError) Error() string { return "slack: " + e.Code }

// AuthInfo is the answer of auth.test: who the bot token is.
type AuthInfo struct {
	TeamID string `json:"team_id"`
	Team   string `json:"team"`
	UserID string `json:"user_id"`
}

// SlackUser is the part of users.info the identity mapping needs.
type SlackUser struct {
	ID               string
	TeamID           string
	Deleted          bool
	IsBot            bool
	IsEmailConfirmed bool
	Email            string
}

// Msg is a message: plain text (notifications, accessibility) plus Block Kit blocks.
type Msg struct {
	Text   string           `json:"text"`
	Blocks []map[string]any `json:"blocks,omitempty"`
}

// call POSTs to a Web API method. A url.Values body is sent form-encoded (the read methods auth.test and users.info
// do not take JSON); anything else is sent as JSON (chat.postMessage, chat.update).
func (c *Client) call(ctx context.Context, token, method string, body any, out any) error {
	var (
		b     []byte
		ctype string
	)
	if form, ok := body.(url.Values); ok {
		b, ctype = []byte(form.Encode()), "application/x-www-form-urlencoded"
	} else {
		var err error
		if b, err = json.Marshal(body); err != nil {
			return err
		}
		ctype = "application/json; charset=utf-8"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base()+"/"+method, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", ctype)
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := c.client().Do(req)
	if err != nil {
		return errors.New("slack: unreachable")
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return errors.New("slack: unreadable answer")
	}
	if res.StatusCode >= 300 {
		return &APIError{Code: fmt.Sprintf("http_%d", res.StatusCode)}
	}
	var env struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return errors.New("slack: unexpected answer")
	}
	if !env.OK {
		code := env.Error
		if code == "" {
			code = "unknown_error"
		}
		return &APIError{Code: code}
	}
	if out != nil {
		return json.Unmarshal(raw, out)
	}
	return nil
}

// AuthTest identifies the bot token (workspace and bot user).
func (c *Client) AuthTest(ctx context.Context, token string) (AuthInfo, error) {
	var a AuthInfo
	err := c.call(ctx, token, "auth.test", url.Values{}, &a)
	return a, err
}

// UserInfo reads a Slack user (needs the users:read and users:read.email scopes for the address).
func (c *Client) UserInfo(ctx context.Context, token, user string) (SlackUser, error) {
	var out struct {
		User struct {
			ID               string `json:"id"`
			TeamID           string `json:"team_id"`
			Deleted          bool   `json:"deleted"`
			IsBot            bool   `json:"is_bot"`
			IsAppUser        bool   `json:"is_app_user"`
			IsEmailConfirmed bool   `json:"is_email_confirmed"`
			Profile          struct {
				Email string `json:"email"`
			} `json:"profile"`
		} `json:"user"`
	}
	if err := c.call(ctx, token, "users.info", url.Values{"user": {user}}, &out); err != nil {
		return SlackUser{}, err
	}
	u := out.User
	return SlackUser{ID: u.ID, TeamID: u.TeamID, Deleted: u.Deleted, IsBot: u.IsBot || u.IsAppUser, IsEmailConfirmed: u.IsEmailConfirmed,
		Email: strings.ToLower(strings.TrimSpace(u.Profile.Email))}, nil
}

// PostMessage posts m to a channel and returns where it landed (channel id, message ts).
func (c *Client) PostMessage(ctx context.Context, token, channel string, m Msg) (string, string, error) {
	var out struct {
		Channel string `json:"channel"`
		TS      string `json:"ts"`
	}
	body := map[string]any{"channel": channel, "text": m.Text, "unfurl_links": false, "unfurl_media": false}
	if len(m.Blocks) > 0 {
		body["blocks"] = m.Blocks
	}
	err := c.call(ctx, token, "chat.postMessage", body, &out)
	return out.Channel, out.TS, err
}

// Update replaces a message the bot posted.
func (c *Client) Update(ctx context.Context, token, channel, ts string, m Msg) error {
	body := map[string]any{"channel": channel, "ts": ts, "text": m.Text, "blocks": m.Blocks}
	if m.Blocks == nil {
		body["blocks"] = []map[string]any{}
	}
	return c.call(ctx, token, "chat.update", body, nil)
}

// ValidResponseURL accepts only Slack's own response endpoints (https://hooks.slack.com/...), so a forged payload
// cannot make the server post to an internal address (SSRF).
func ValidResponseURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" {
		return false
	}
	return strings.EqualFold(u.Hostname(), "hooks.slack.com") && strings.HasPrefix(u.EscapedPath(), "/")
}

// Respond posts body to an interaction's response_url (update the original message or reply ephemerally).
func (c *Client) Respond(ctx context.Context, responseURL string, body map[string]any) error {
	if !ValidResponseURL(responseURL) {
		return errors.New("slack: response_url not allowed")
	}
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, responseURL, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	res, err := c.client().Do(req)
	if err != nil {
		return errors.New("slack: response_url unreachable")
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 4096))
	if res.StatusCode >= 300 {
		return &APIError{Code: fmt.Sprintf("http_%d", res.StatusCode)}
	}
	return nil
}

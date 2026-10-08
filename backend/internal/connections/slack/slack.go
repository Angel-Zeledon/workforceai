// Package slack is the Slack adapter. It authenticates with a bot token kept
// in the vault (api_key connections). Reads are limited to the channels of the
// connection's allowlist (empty = nothing); posting a message is a write that
// always needs a human approval and is held before it goes out, like an email.
package slack

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"aiworkforce/backend/internal/connections"
)

// Config holds the API base (overridable for tests).
type Config struct {
	APIBase string // https://slack.com/api
}

// Provider implements connections.Provider for Slack.
type Provider struct {
	base     string
	manifest connections.Manifest

	mu    sync.Mutex
	fakes map[string]*Fake
}

// New builds the provider with the embedded manifest.
func New(cfg Config) (*Provider, error) {
	ms, err := connections.LoadManifests()
	if err != nil {
		return nil, err
	}
	if cfg.APIBase == "" {
		cfg.APIBase = "https://slack.com/api"
	}
	return &Provider{base: strings.TrimRight(cfg.APIBase, "/"), manifest: ms["slack"], fakes: map[string]*Fake{}}, nil
}

func (p *Provider) Manifest() connections.Manifest { return p.manifest }

// FakeFor returns the simulated workspace of a connection.
func (p *Provider) FakeFor(connID string) *Fake {
	p.mu.Lock()
	defer p.mu.Unlock()
	f, ok := p.fakes[connID]
	if !ok {
		f = NewFake()
		p.fakes[connID] = f
	}
	return f
}

func (p *Provider) Open(h connections.Handle) connections.Executor {
	if h.Mode == connections.ModeSimulated {
		return &scoped{h: h, next: p.FakeFor(h.ConnectionID)}
	}
	return &scoped{h: h, next: &live{base: p.base, h: h}}
}

// Bot-token connections have no OAuth flow here (the admin installs the app).
func (p *Provider) AuthStart(connections.AuthStartIn) (string, error) {
	return "", &connections.ProviderError{Code: connections.CodeInvalidArgs}
}

func (p *Provider) AuthFinish(context.Context, connections.AuthFinishIn) (connections.Bundle, connections.Identity, error) {
	return connections.Bundle{}, connections.Identity{}, &connections.ProviderError{Code: connections.CodeInvalidArgs}
}

func (p *Provider) Refresh(_ context.Context, _ connections.OAuthApp, secret string) (connections.AccessToken, error) {
	return connections.StaticToken(secret)
}

// Revoke: bot tokens are revoked by uninstalling the app in Slack; deleting the
// connection crypto-shreds our copy.
func (p *Provider) Revoke(context.Context, connections.OAuthApp, string) error { return nil }

// Test calls auth.test (no data changes).
func (p *Provider) Test(ctx context.Context, h connections.Handle) connections.TestResult {
	l := &live{base: p.base, h: h}
	var out struct {
		Team string `json:"team"`
		User string `json:"user"`
	}
	start := time.Now()
	_, _, err := l.get(ctx, http.MethodPost, "auth.test", nil, nil, &out)
	res := connections.TestResult{LatencyMS: int(time.Since(start).Milliseconds())}
	if err != nil {
		res.Code = connections.Classify(err).Code
		return res
	}
	res.OK, res.Account = true, out.User+"@"+out.Team
	return res
}

// Message is a normalized channel message.
type Message struct {
	Channel, User, Text, TS string
}

func (m Message) item() connections.Item {
	return connections.Item{ID: m.Channel + ":" + m.TS, Meta: map[string]any{"channel": m.Channel, "ts": m.TS, "time": tsTime(m.TS)},
		Texts: []connections.Text{{Name: "user", Value: m.User}, {Name: "text", Value: m.Text}}}
}

func tsTime(ts string) string {
	sec, _, _ := strings.Cut(ts, ".")
	n, err := strconv.ParseInt(sec, 10, 64)
	if err != nil {
		return ""
	}
	return time.Unix(n, 0).UTC().Format(time.RFC3339)
}

// Channel is a normalized channel.
type Channel struct {
	ID, Name, Topic string
	Members         int
}

type backend interface {
	channel(ctx context.Context, id string) (Channel, int, error)
	history(ctx context.Context, channel string, max int) ([]Message, int, int64, error)
	post(ctx context.Context, channel, text, threadTS string) (string, int, error)
}

// scoped enforces the channel allowlist and validates arguments.
type scoped struct {
	h    connections.Handle
	next backend
}

var channelRe = regexp.MustCompile(`^[CG][A-Z0-9]{6,20}$`)

const maxPostChars = 4000

func (s *scoped) channelArg(args map[string]any) (string, error) {
	ch := strings.ToUpper(connections.ArgStr(args, "channel", "channel_id"))
	if !channelRe.MatchString(ch) {
		return "", connections.InvalidArgs() // public/private channel ids only: no DMs to arbitrary users
	}
	if len(s.h.ResourceScope.Channels) == 0 || !connections.InList(s.h.ResourceScope.Channels, ch) {
		return "", connections.OutOfScope()
	}
	return ch, nil
}

func (s *scoped) Execute(ctx context.Context, c connections.ExecCall) (connections.ExecResult, error) {
	if c.Action == "list_channels" {
		if len(s.h.ResourceScope.Channels) == 0 {
			return connections.ExecResult{}, connections.OutOfScope()
		}
		res := connections.ExecResult{Status: 200, ResourceRef: fmt.Sprintf("channels=%d", len(s.h.ResourceScope.Channels))}
		for _, id := range s.h.ResourceScope.Channels {
			ch, st, err := s.next.channel(ctx, strings.ToUpper(id))
			if err != nil {
				return connections.ExecResult{Status: st}, err
			}
			res.Items = append(res.Items, connections.Item{ID: ch.ID, Meta: map[string]any{"channel": ch.ID, "members": ch.Members},
				Texts: []connections.Text{{Name: "name", Value: ch.Name}, {Name: "topic", Value: ch.Topic}}})
		}
		res.Summary = fmt.Sprintf("%d channels", len(res.Items))
		return res, nil
	}
	ch, err := s.channelArg(c.Args)
	if err != nil {
		return connections.ExecResult{}, err
	}
	switch c.Action {
	case "history":
		ms, st, n, err := s.next.history(ctx, ch, connections.Limit(c.Args, 20, c.MaxItems))
		if err != nil {
			return connections.ExecResult{Status: st}, err
		}
		res := connections.ExecResult{Status: st, BytesIn: n, Summary: fmt.Sprintf("%d messages", len(ms)), ResourceRef: "channel=" + ch}
		for _, m := range ms {
			res.Items = append(res.Items, m.item())
		}
		return res, nil
	case "post_message":
		text := connections.ArgStr(c.Args, "text", "body")
		if text == "" || len([]rune(text)) > maxPostChars {
			return connections.ExecResult{}, connections.InvalidArgs()
		}
		ts, st, err := s.next.post(ctx, ch, text, connections.ArgStr(c.Args, "thread_ts"))
		if err != nil {
			return connections.ExecResult{Status: st}, err
		}
		return connections.ExecResult{Status: st, Summary: "message posted", ResourceRef: "channel=" + ch, Data: map[string]any{"message_ts": ts}}, nil
	}
	return connections.ExecResult{}, connections.InvalidArgs()
}

// ---- live client ----

type live struct {
	base string
	h    connections.Handle
}

// get performs a Web API method with a single request. Slack answers most
// errors with HTTP 200 and {"ok": false, "error": "..."}: the body is read once
// and both the envelope and the data are decoded from it (no body surfaced).
func (l *live) get(ctx context.Context, method, api string, q url.Values, body any, out any) (int, int64, error) {
	var raw json.RawMessage
	st, n, err := connections.DoJSON(ctx, l.h, method, l.base+"/"+api, q, body, &raw)
	if err != nil {
		return st, n, err
	}
	var env struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if json.Unmarshal(raw, &env) != nil {
		return st, n, &connections.ProviderError{Status: st, Code: connections.CodeProviderError}
	}
	if !env.OK {
		return st, n, &connections.ProviderError{Status: st, Code: slackCode(env.Error)}
	}
	if out != nil && json.Unmarshal(raw, out) != nil {
		return st, n, &connections.ProviderError{Status: st, Code: connections.CodeProviderError}
	}
	return st, n, nil
}

func slackCode(e string) string {
	switch e {
	case "invalid_auth", "not_authed", "token_revoked", "token_expired", "account_inactive":
		return connections.CodeReauthRequired
	case "missing_scope", "not_allowed_token_type":
		return "insufficient_scope"
	case "ratelimited":
		return "rate_limited"
	case "channel_not_found", "not_in_channel", "thread_not_found":
		return "not_found"
	case "msg_too_long", "no_text", "invalid_arguments":
		return connections.CodeInvalidArgs
	}
	return connections.CodeProviderError
}

func (l *live) channel(ctx context.Context, id string) (Channel, int, error) {
	var out struct {
		Channel struct {
			ID    string `json:"id"`
			Name  string `json:"name"`
			Topic struct {
				Value string `json:"value"`
			} `json:"topic"`
			NumMembers int `json:"num_members"`
		} `json:"channel"`
	}
	st, _, err := l.get(ctx, http.MethodGet, "conversations.info", url.Values{"channel": {id}, "include_num_members": {"true"}}, nil, &out)
	return Channel{ID: out.Channel.ID, Name: out.Channel.Name, Topic: out.Channel.Topic.Value, Members: out.Channel.NumMembers}, st, err
}

func (l *live) history(ctx context.Context, channel string, max int) ([]Message, int, int64, error) {
	var out struct {
		Messages []struct {
			User string `json:"user"`
			Text string `json:"text"`
			TS   string `json:"ts"`
		} `json:"messages"`
	}
	st, n, err := l.get(ctx, http.MethodGet, "conversations.history", url.Values{"channel": {channel}, "limit": {strconv.Itoa(max)}}, nil, &out)
	if err != nil {
		return nil, st, n, err
	}
	var ms []Message
	for i, m := range out.Messages {
		if i >= max {
			break
		}
		ms = append(ms, Message{Channel: channel, User: m.User, Text: m.Text, TS: m.TS})
	}
	return ms, st, n, nil
}

func (l *live) post(ctx context.Context, channel, text, threadTS string) (string, int, error) {
	body := map[string]any{"channel": channel, "text": text, "unfurl_links": false, "unfurl_media": false}
	if threadTS != "" {
		body["thread_ts"] = threadTS
	}
	var out struct {
		TS string `json:"ts"`
	}
	st, _, err := l.get(ctx, http.MethodPost, "chat.postMessage", nil, body, &out)
	return out.TS, st, err
}

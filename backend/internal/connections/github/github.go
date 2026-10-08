// Package github is the GitHub adapter. It authenticates with a fine-grained
// personal access token kept in the vault (api_key connections): the token is
// the bearer itself, injected by the connection transport. Reads cover the
// repositories of the connection's allowlist (empty = nothing); writes are
// limited to opening issues and commenting, always behind a human approval.
package github

import (
	"context"
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

// Config holds the API base (overridable for tests and GitHub Enterprise).
type Config struct {
	APIBase string // https://api.github.com
}

// Provider implements connections.Provider for GitHub.
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
		cfg.APIBase = "https://api.github.com"
	}
	return &Provider{base: strings.TrimRight(cfg.APIBase, "/"), manifest: ms["github"], fakes: map[string]*Fake{}}, nil
}

func (p *Provider) Manifest() connections.Manifest { return p.manifest }

// FakeFor returns the simulated GitHub of a connection.
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

// Token connections have no OAuth flow.
func (p *Provider) AuthStart(connections.AuthStartIn) (string, error) {
	return "", &connections.ProviderError{Code: connections.CodeInvalidArgs}
}

func (p *Provider) AuthFinish(context.Context, connections.AuthFinishIn) (connections.Bundle, connections.Identity, error) {
	return connections.Bundle{}, connections.Identity{}, &connections.ProviderError{Code: connections.CodeInvalidArgs}
}

// Refresh hands the vaulted token to the transport (see connections.StaticToken).
func (p *Provider) Refresh(_ context.Context, _ connections.OAuthApp, secret string) (connections.AccessToken, error) {
	return connections.StaticToken(secret)
}

// Revoke: personal tokens are revoked by their owner on GitHub; deleting the
// connection crypto-shreds our copy.
func (p *Provider) Revoke(context.Context, connections.OAuthApp, string) error { return nil }

// Test reads the token's own user (no data changes).
func (p *Provider) Test(ctx context.Context, h connections.Handle) connections.TestResult {
	var u struct {
		Login string `json:"login"`
	}
	start := time.Now()
	_, _, err := connections.DoJSON(ctx, h, http.MethodGet, p.base+"/user", nil, nil, &u)
	res := connections.TestResult{LatencyMS: int(time.Since(start).Milliseconds())}
	if err != nil {
		res.Code = connections.Classify(err).Code
		return res
	}
	res.OK, res.Account = true, u.Login
	return res
}

// Issue is a normalized issue or pull request.
type Issue struct {
	Repo, Title, Body, Author, State string
	Number                           int
	Pull                             bool
	Updated                          time.Time
}

func (i Issue) item() connections.Item {
	kind := "issue"
	if i.Pull {
		kind = "pull_request"
	}
	return connections.Item{ID: fmt.Sprintf("%s#%d", i.Repo, i.Number),
		Meta:  map[string]any{"repo": i.Repo, "number": i.Number, "kind": kind, "state": i.State, "updated": i.Updated.Format(time.RFC3339)},
		Texts: []connections.Text{{Name: "title", Value: i.Title}, {Name: "author", Value: i.Author}, {Name: "body", Value: i.Body}}}
}

// Repo is the normalized repository metadata.
type Repo struct {
	FullName, Description string
	Private               bool
	OpenIssues            int
}

type backend interface {
	repo(ctx context.Context, full string) (Repo, int, error)
	issues(ctx context.Context, full string, pulls bool, state string, max int) ([]Issue, int, int64, error)
	issue(ctx context.Context, full string, number int, pull bool) (Issue, int, error)
	createIssue(ctx context.Context, full, title, body string) (string, int, error)
	comment(ctx context.Context, full string, number int, body string) (string, int, error)
}

// scoped enforces the repository allowlist and validates arguments.
type scoped struct {
	h    connections.Handle
	next backend
}

var repoRe = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,100}/[A-Za-z0-9_.-]{1,100}$`)

func (s *scoped) repoArg(args map[string]any) (string, error) {
	r := connections.ArgStr(args, "repo", "repository")
	if !repoRe.MatchString(r) {
		return "", connections.InvalidArgs()
	}
	if len(s.h.ResourceScope.Repos) == 0 || !connections.InList(s.h.ResourceScope.Repos, r) {
		return "", connections.OutOfScope()
	}
	return r, nil
}

func (s *scoped) Execute(ctx context.Context, c connections.ExecCall) (connections.ExecResult, error) {
	if c.Action == "list_repos" {
		if len(s.h.ResourceScope.Repos) == 0 {
			return connections.ExecResult{}, connections.OutOfScope()
		}
		res := connections.ExecResult{Status: 200, ResourceRef: fmt.Sprintf("repos=%d", len(s.h.ResourceScope.Repos))}
		for _, full := range s.h.ResourceScope.Repos {
			if len(res.Items) >= connections.Limit(c.Args, 25, c.MaxItems) {
				break
			}
			r, st, err := s.next.repo(ctx, full)
			if err != nil {
				return connections.ExecResult{Status: st}, err
			}
			res.Items = append(res.Items, connections.Item{ID: r.FullName,
				Meta:  map[string]any{"repo": r.FullName, "private": r.Private, "open_issues": r.OpenIssues},
				Texts: []connections.Text{{Name: "description", Value: r.Description}}})
		}
		res.Summary = fmt.Sprintf("%d repositories", len(res.Items))
		return res, nil
	}
	repo, err := s.repoArg(c.Args)
	if err != nil {
		return connections.ExecResult{}, err
	}
	num := connections.ArgInt(c.Args, 0, "number", "issue_number", "pull_number")
	switch c.Action {
	case "list_issues", "list_pulls":
		state := connections.ArgStr(c.Args, "state")
		if state != "closed" && state != "all" {
			state = "open"
		}
		is, st, n, err := s.next.issues(ctx, repo, c.Action == "list_pulls", state, connections.Limit(c.Args, 10, c.MaxItems))
		if err != nil {
			return connections.ExecResult{Status: st}, err
		}
		res := connections.ExecResult{Status: st, BytesIn: n, Summary: fmt.Sprintf("%d items", len(is)), ResourceRef: "repo=" + repo + " state=" + state}
		for _, i := range is {
			res.Items = append(res.Items, i.item())
		}
		return res, nil
	case "read_issue", "read_pull":
		if num < 1 {
			return connections.ExecResult{}, connections.InvalidArgs()
		}
		i, st, err := s.next.issue(ctx, repo, num, c.Action == "read_pull")
		if err != nil {
			return connections.ExecResult{Status: st}, err
		}
		return connections.ExecResult{Status: st, Summary: "1 item", ResourceRef: "repo=" + repo, Items: []connections.Item{i.item()}}, nil
	case "create_issue":
		title := connections.Truncate(connections.ArgStr(c.Args, "title"), 256)
		if title == "" {
			return connections.ExecResult{}, connections.InvalidArgs()
		}
		u, st, err := s.next.createIssue(ctx, repo, title, connections.Truncate(connections.ArgStr(c.Args, "body"), 20000))
		if err != nil {
			return connections.ExecResult{Status: st}, err
		}
		return connections.ExecResult{Status: st, Summary: "issue created", ResourceRef: "repo=" + repo, Data: map[string]any{"issue_url": u}}, nil
	case "comment":
		body := connections.Truncate(connections.ArgStr(c.Args, "body"), 20000)
		if num < 1 || body == "" {
			return connections.ExecResult{}, connections.InvalidArgs()
		}
		u, st, err := s.next.comment(ctx, repo, num, body)
		if err != nil {
			return connections.ExecResult{Status: st}, err
		}
		return connections.ExecResult{Status: st, Summary: "comment added", ResourceRef: fmt.Sprintf("repo=%s number=%d", repo, num), Data: map[string]any{"comment_url": u}}, nil
	}
	return connections.ExecResult{}, connections.InvalidArgs()
}

// ---- live client ----

type live struct {
	base string
	h    connections.Handle
}

type gIssue struct {
	Number      int    `json:"number"`
	Title       string `json:"title"`
	Body        string `json:"body"`
	State       string `json:"state"`
	UpdatedAt   string `json:"updated_at"`
	HTMLURL     string `json:"html_url"`
	PullRequest *struct {
		URL string `json:"url"`
	} `json:"pull_request"`
	User struct {
		Login string `json:"login"`
	} `json:"user"`
}

func (g gIssue) issue(repo string, pull bool) Issue {
	t, _ := time.Parse(time.RFC3339, g.UpdatedAt)
	return Issue{Repo: repo, Number: g.Number, Title: g.Title, Body: g.Body, Author: g.User.Login, State: g.State, Pull: pull || g.PullRequest != nil, Updated: t}
}

func (l *live) url(full, path string) string {
	owner, name, _ := strings.Cut(full, "/")
	return l.base + "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(name) + path
}

func (l *live) repo(ctx context.Context, full string) (Repo, int, error) {
	var g struct {
		FullName        string `json:"full_name"`
		Description     string `json:"description"`
		Private         bool   `json:"private"`
		OpenIssuesCount int    `json:"open_issues_count"`
	}
	st, _, err := connections.DoJSON(ctx, l.h, http.MethodGet, l.url(full, ""), nil, nil, &g)
	return Repo{FullName: g.FullName, Description: g.Description, Private: g.Private, OpenIssues: g.OpenIssuesCount}, st, err
}

func (l *live) issues(ctx context.Context, full string, pulls bool, state string, max int) ([]Issue, int, int64, error) {
	path := "/issues"
	if pulls {
		path = "/pulls"
	}
	var out []gIssue
	st, n, err := connections.DoJSON(ctx, l.h, http.MethodGet, l.url(full, path), url.Values{"state": {state}, "per_page": {strconv.Itoa(max)}}, nil, &out)
	if err != nil {
		return nil, st, n, err
	}
	var is []Issue
	for _, g := range out {
		if !pulls && g.PullRequest != nil {
			continue // the issues endpoint also returns pull requests
		}
		if len(is) >= max {
			break
		}
		is = append(is, g.issue(full, pulls))
	}
	return is, st, n, nil
}

func (l *live) issue(ctx context.Context, full string, number int, pull bool) (Issue, int, error) {
	path := "/issues/"
	if pull {
		path = "/pulls/"
	}
	var g gIssue
	st, _, err := connections.DoJSON(ctx, l.h, http.MethodGet, l.url(full, path+strconv.Itoa(number)), nil, nil, &g)
	return g.issue(full, pull), st, err
}

func (l *live) createIssue(ctx context.Context, full, title, body string) (string, int, error) {
	var g gIssue
	st, _, err := connections.DoJSON(ctx, l.h, http.MethodPost, l.url(full, "/issues"), nil, map[string]string{"title": title, "body": body}, &g)
	return g.HTMLURL, st, err
}

func (l *live) comment(ctx context.Context, full string, number int, body string) (string, int, error) {
	var g struct {
		HTMLURL string `json:"html_url"`
	}
	st, _, err := connections.DoJSON(ctx, l.h, http.MethodPost, l.url(full, "/issues/"+strconv.Itoa(number)+"/comments"), nil, map[string]string{"body": body}, &g)
	return g.HTMLURL, st, err
}

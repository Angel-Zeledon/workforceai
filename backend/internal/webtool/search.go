package webtool

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

// MaxResults is the most results one search returns to the model.
const MaxResults = 8

// ErrNoProvider: no search provider is configured (WEB_SEARCH_PROVIDER). Fetch still works.
var ErrNoProvider = errors.New("search_unavailable")

// ErrSearch is any provider failure (the detail is never shown to the agent).
var ErrSearch = errors.New("search_failed")

// Result is one search hit.
type Result struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
	Date    string `json:"date,omitempty"`
}

// Provider runs a search. Implementations only talk to their own, fixed endpoint.
type Provider interface {
	Name() string
	Search(ctx context.Context, query string, limit int) ([]Result, error)
}

// NewProvider builds the provider chosen by env. An empty name means "none" (nil, nil).
func NewProvider(name, apiKey, baseURL string, hc *http.Client) (Provider, error) {
	if hc == nil {
		hc = &http.Client{Timeout: 12 * time.Second}
	}
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "":
		return nil, nil
	case "brave":
		if apiKey == "" {
			return nil, errors.New("WEB_SEARCH_API_KEY is required for the brave provider")
		}
		return &Brave{Key: apiKey, Endpoint: "https://api.search.brave.com/res/v1/web/search", HTTP: hc}, nil
	case "tavily":
		if apiKey == "" {
			return nil, errors.New("WEB_SEARCH_API_KEY is required for the tavily provider")
		}
		return &Tavily{Key: apiKey, Endpoint: "https://api.tavily.com/search", HTTP: hc}, nil
	case "searxng":
		u, err := url.Parse(baseURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return nil, errors.New("WEB_SEARCH_URL must be the base URL of the SearXNG instance")
		}
		return &SearXNG{Base: strings.TrimRight(baseURL, "/"), Key: apiKey, HTTP: hc}, nil
	}
	return nil, fmt.Errorf("unknown WEB_SEARCH_PROVIDER %q (brave|tavily|searxng)", name)
}

func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) > n {
		return string(r[:n]) + "..."
	}
	return s
}

// finish normalizes provider results: http(s) URLs only, bounded fields, at most limit.
func finish(in []Result, limit int) []Result {
	if limit <= 0 || limit > MaxResults {
		limit = MaxResults
	}
	out := make([]Result, 0, limit)
	for _, r := range in {
		u, err := url.Parse(strings.TrimSpace(r.URL))
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
			continue
		}
		out = append(out, Result{Title: clip(r.Title, 200), URL: u.String(), Snippet: clip(r.Snippet, 400), Date: clip(r.Date, 40)})
		if len(out) == limit {
			break
		}
	}
	return out
}

func doJSON(hc *http.Client, req *http.Request, dst any) error {
	resp, err := hc.Do(req)
	if err != nil {
		return ErrSearch
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil || resp.StatusCode != http.StatusOK {
		return ErrSearch
	}
	if json.Unmarshal(body, dst) != nil {
		return ErrSearch
	}
	return nil
}

// Brave is the Brave Search API (GET /res/v1/web/search, header X-Subscription-Token, web.results[]).
type Brave struct {
	Key, Endpoint string
	HTTP          *http.Client
}

func (b *Brave) Name() string { return "brave" }

func (b *Brave) Search(ctx context.Context, query string, limit int) ([]Result, error) {
	q := url.Values{"q": {query}, "count": {fmt.Sprint(min(max(limit, 1), 20))}, "safesearch": {"moderate"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, b.Endpoint+"?"+q.Encode(), nil)
	if err != nil {
		return nil, ErrSearch
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Subscription-Token", b.Key)
	var out struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
				Age         string `json:"age"`
				PageAge     string `json:"page_age"`
			} `json:"results"`
		} `json:"web"`
	}
	if err := doJSON(b.HTTP, req, &out); err != nil {
		return nil, err
	}
	rs := make([]Result, 0, len(out.Web.Results))
	for _, r := range out.Web.Results {
		d := r.PageAge
		if d == "" {
			d = r.Age
		}
		rs = append(rs, Result{Title: stripTags(r.Title), URL: r.URL, Snippet: stripTags(r.Description), Date: d})
	}
	return finish(rs, limit), nil
}

// Tavily is the Tavily Search API (POST /search, Bearer key, results[].content).
type Tavily struct {
	Key, Endpoint string
	HTTP          *http.Client
}

func (t *Tavily) Name() string { return "tavily" }

func (t *Tavily) Search(ctx context.Context, query string, limit int) ([]Result, error) {
	body, _ := json.Marshal(map[string]any{"query": query, "max_results": min(max(limit, 1), MaxResults), "search_depth": "basic",
		"include_answer": false, "include_raw_content": false})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.Endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, ErrSearch
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+t.Key)
	var out struct {
		Results []struct {
			Title         string `json:"title"`
			URL           string `json:"url"`
			Content       string `json:"content"`
			PublishedDate string `json:"published_date"`
		} `json:"results"`
	}
	if err := doJSON(t.HTTP, req, &out); err != nil {
		return nil, err
	}
	rs := make([]Result, 0, len(out.Results))
	for _, r := range out.Results {
		rs = append(rs, Result{Title: r.Title, URL: r.URL, Snippet: r.Content, Date: r.PublishedDate})
	}
	return finish(rs, limit), nil
}

// SearXNG is a self-hosted SearXNG instance (GET /search?format=json; the instance must enable the json format).
type SearXNG struct {
	Base, Key string
	HTTP      *http.Client
}

func (s *SearXNG) Name() string { return "searxng" }

func (s *SearXNG) Search(ctx context.Context, query string, limit int) ([]Result, error) {
	q := url.Values{"q": {query}, "format": {"json"}, "safesearch": {"1"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.Base+"/search?"+q.Encode(), nil)
	if err != nil {
		return nil, ErrSearch
	}
	req.Header.Set("Accept", "application/json")
	if s.Key != "" {
		req.Header.Set("Authorization", "Bearer "+s.Key)
	}
	var out struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
			Date    string `json:"publishedDate"`
		} `json:"results"`
	}
	if err := doJSON(s.HTTP, req, &out); err != nil {
		return nil, err
	}
	rs := make([]Result, 0, len(out.Results))
	for _, r := range out.Results {
		rs = append(rs, Result{Title: r.Title, URL: r.URL, Snippet: r.Content, Date: r.Date})
	}
	return finish(rs, limit), nil
}

func stripTags(s string) string {
	_, t := ExtractHTML("<body>"+s+"</body>", nil)
	return t
}

// FakeProvider is the deterministic provider of the tests.
type FakeProvider struct {
	Results []Result
	Err     error
	Queries []string
}

func (f *FakeProvider) Name() string { return "fake" }

func (f *FakeProvider) Search(_ context.Context, query string, limit int) ([]Result, error) {
	f.Queries = append(f.Queries, query)
	if f.Err != nil {
		return nil, f.Err
	}
	return finish(f.Results, limit), nil
}

// Package customapi is the generic HTTP source adapter (provider custom_api):
// any platform with an HTTP API becomes something agents can query without a
// purpose-built adapter. An admin describes the source (base URL, how the API
// key travels, optional named endpoints and what lives there); agents call
// api.get with an endpoint name (or, when the source lists none, a path under
// the base URL). Only GET is ever sent: a source cannot change anything. The
// key is injected by the connection handle's transport and never seen here.
package customapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"aiworkforce/backend/internal/connections"
)

// Provider implements connections.Provider for generic HTTP sources.
type Provider struct {
	manifest connections.Manifest
}

// New builds the provider with the embedded manifest.
func New() (*Provider, error) {
	ms, err := connections.LoadManifests()
	if err != nil {
		return nil, err
	}
	return &Provider{manifest: ms[connections.SourceProvider]}, nil
}

func (p *Provider) Manifest() connections.Manifest { return p.manifest }

func (p *Provider) Open(h connections.Handle) connections.Executor {
	if h.Mode == connections.ModeSimulated {
		return &executor{h: h, do: simulated}
	}
	return &executor{h: h, do: live}
}

// API keys are pasted by an admin: there is no OAuth flow.
func (p *Provider) AuthStart(connections.AuthStartIn) (string, error) {
	return "", &connections.ProviderError{Code: connections.CodeInvalidArgs}
}

func (p *Provider) AuthFinish(context.Context, connections.AuthFinishIn) (connections.Bundle, connections.Identity, error) {
	return connections.Bundle{}, connections.Identity{}, &connections.ProviderError{Code: connections.CodeInvalidArgs}
}

func (p *Provider) Refresh(_ context.Context, _ connections.OAuthApp, secret string) (connections.AccessToken, error) {
	return connections.StaticToken(secret)
}

// Revoke: the key is revoked on the platform; deleting the connection
// crypto-shreds our copy.
func (p *Provider) Revoke(context.Context, connections.OAuthApp, string) error { return nil }

// Test calls the first endpoint that needs no parameter (else the base URL).
// A rejected key fails; any other answer proves the source is reachable and
// accepts the key (many APIs answer 404 at their root).
func (p *Provider) Test(ctx context.Context, h connections.Handle) connections.TestResult {
	if h.Source == nil {
		return connections.TestResult{Code: CodeNotConfigured}
	}
	rel := ""
	for _, e := range h.Source.Endpoints {
		if !strings.Contains(e.Path, "{") {
			rel = e.Path
			break
		}
	}
	start := time.Now()
	st, _, _, err := live(ctx, h, h.Source.BaseURL+rel, nil)
	res := connections.TestResult{LatencyMS: int(time.Since(start).Milliseconds())}
	if err != nil {
		pe := connections.Classify(err)
		if st != http.StatusNotFound && st != http.StatusMethodNotAllowed {
			res.Code = pe.Code
			return res
		}
	}
	res.OK = true
	if u, err := url.Parse(h.Source.BaseURL); err == nil {
		res.Account = u.Host
	}
	return res
}

// CodeNotConfigured: a custom_api connection without a source (created before
// sources existed) has nothing to query until an admin configures it.
const CodeNotConfigured = connections.CodeConnectionUnavailable

// fetcher performs one GET and returns status, body, content type.
type fetcher func(ctx context.Context, h connections.Handle, rawURL string, q url.Values) (int, []byte, string, error)

type executor struct {
	h  connections.Handle
	do fetcher
}

func (x *executor) Execute(ctx context.Context, c connections.ExecCall) (connections.ExecResult, error) {
	if c.Action != "get" {
		return connections.ExecResult{}, connections.InvalidArgs()
	}
	src := x.h.Source
	if src == nil {
		return connections.ExecResult{}, &connections.ProviderError{Code: CodeNotConfigured}
	}
	rel, q, ref, err := Resolve(*src, c.Args)
	if err != nil {
		return connections.ExecResult{}, err
	}
	st, raw, ctype, err := x.do(ctx, x.h, src.BaseURL+rel, q)
	if err != nil {
		return connections.ExecResult{Status: st}, err
	}
	items, total := Items(raw, ctype, c.MaxItems)
	for i := range items {
		items[i].Meta["source_ref"] = ref
	}
	sum := fmt.Sprintf("%d item(s)", len(items))
	if total > len(items) {
		sum = fmt.Sprintf("%d of %d item(s)", len(items), total)
	}
	return connections.ExecResult{Status: st, Items: items, BytesIn: int64(len(raw)), Summary: sum, ResourceRef: ref}, nil
}

// reserved are the call arguments that are not parameters of the source.
var reserved = map[string]bool{"endpoint": true, "path": true, "params": true, "max_results": true, "limit": true, "connection_alias": true}

// params collects the agent's parameters: the "params" object, or (models
// often flatten them) the remaining top-level arguments.
func params(args map[string]any) map[string]string {
	out := map[string]string{}
	add := func(k string, v any) {
		if v == nil {
			return
		}
		switch t := v.(type) {
		case string:
			out[k] = strings.TrimSpace(t)
		case float64, bool, int, json.Number:
			out[k] = fmt.Sprint(t)
		}
	}
	if m, ok := args["params"].(map[string]any); ok {
		for k, v := range m {
			add(k, v)
		}
		return out
	}
	for k, v := range args {
		if !reserved[k] {
			add(k, v)
		}
	}
	return out
}

// Resolve turns an api.get call into a path relative to the base URL and its
// query, enforcing the source's endpoint allowlist. ref is a content-free
// label of what was called (the endpoint name or "path").
func Resolve(src connections.Source, args map[string]any) (rel string, q url.Values, ref string, err error) {
	ps := params(args)
	if src.Auth.Style == connections.AuthQuery {
		for k := range ps {
			if strings.EqualFold(k, src.Auth.Name) {
				delete(ps, k) // the key's parameter is ours, never the agent's
			}
		}
	}
	q = url.Values{}
	if len(src.Endpoints) == 0 {
		rel = connections.ArgStr(args, "path")
		if rel == "" {
			rel = "/"
		}
		if strings.Contains(rel, "{") {
			return "", nil, "", connections.InvalidArgs()
		}
		if err := connections.CheckRelPath(rel); err != nil {
			return "", nil, "", connections.InvalidArgs()
		}
		for k, v := range ps {
			q.Set(k, v)
		}
		return rel, q, "path", nil
	}
	name := strings.ToLower(connections.ArgStr(args, "endpoint"))
	var ep *connections.Endpoint
	for i := range src.Endpoints {
		e := &src.Endpoints[i]
		if e.Name == name || (name == "" && e.Path == connections.ArgStr(args, "path")) {
			ep = e
			break
		}
	}
	if ep == nil {
		return "", nil, "", connections.OutOfScope() // only the listed endpoints are callable
	}
	allowed := map[string]bool{}
	for _, p := range ep.Params {
		allowed[p] = true
	}
	used := map[string]bool{}
	var b strings.Builder
	rest := ep.Path
	for {
		i := strings.IndexByte(rest, '{')
		if i < 0 {
			b.WriteString(rest)
			break
		}
		j := strings.IndexByte(rest[i:], '}')
		if j < 0 {
			return "", nil, "", connections.InvalidArgs()
		}
		key := rest[i+1 : i+j]
		v := ps[key]
		if v == "" || v == "." || v == ".." {
			return "", nil, "", connections.InvalidArgs()
		}
		b.WriteString(rest[:i])
		b.WriteString(url.PathEscape(v)) // a value can never add a segment
		used[key] = true
		rest = rest[i+j+1:]
	}
	for k, v := range ps {
		if allowed[k] && !used[k] && v != "" {
			q.Set(k, v) // unknown parameters are dropped: only the listed ones are sent
		}
	}
	return b.String(), q, ep.Name, nil
}

// live performs the GET through the connection's authenticated client.
func live(ctx context.Context, h connections.Handle, rawURL string, q url.Values) (int, []byte, string, error) {
	if h.HTTP == nil {
		return 0, nil, "", &connections.ProviderError{Code: connections.CodeConnectionUnavailable}
	}
	if len(q) > 0 {
		rawURL += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return 0, nil, "", connections.InvalidArgs()
	}
	req.Header.Set("Accept", "application/json, text/plain;q=0.8, */*;q=0.5")
	resp, err := h.HTTP.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return 0, nil, "", ctx.Err()
		}
		return 0, nil, "", connections.Classify(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, connections.MaxResponseBytes))
	if resp.StatusCode/100 != 2 {
		return resp.StatusCode, nil, "", &connections.ProviderError{Status: resp.StatusCode,
			Code: connections.ClassifyResponse(resp.StatusCode, resp.Header, raw), RetryAfter: connections.RetryAfter(resp.Header)}
	}
	return resp.StatusCode, raw, resp.Header.Get("Content-Type"), nil
}

// listKeys are where APIs usually wrap the records of a list answer.
var listKeys = []string{"data", "items", "results", "records", "rows", "value", "entries", "hits", "products", "orders", "customers", "contacts", "deals", "tickets"}

const maxItemChars = 4000

// Items turns a response into result items: one per record of a JSON list
// (top-level or under a usual wrapper key), else one item with the whole body.
// Every text is third-party data: the gateway sanitizes and delimits it.
func Items(raw []byte, contentType string, max int) ([]connections.Item, int) {
	if max <= 0 {
		max = 25
	}
	mt, _, _ := mime.ParseMediaType(contentType)
	var v any
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		text := strings.TrimSpace(string(raw))
		if text == "" {
			return nil, 0
		}
		return []connections.Item{{ID: "0", Meta: map[string]any{"format": "text"},
			Texts: []connections.Text{{Name: "body", Value: connections.Truncate(text, maxItemChars), HTML: strings.Contains(mt, "html")}}}}, 1
	}
	list, ok := v.([]any)
	if !ok {
		if m, isObj := v.(map[string]any); isObj {
			for _, k := range listKeys {
				if l, isList := m[k].([]any); isList {
					list, ok = l, true
					break
				}
			}
		}
	}
	if !ok {
		return []connections.Item{jsonItem(0, v)}, 1
	}
	var out []connections.Item
	for i, e := range list {
		if i >= max {
			break
		}
		out = append(out, jsonItem(i, e))
	}
	return out, len(list)
}

func jsonItem(i int, v any) connections.Item {
	b, _ := json.Marshal(v)
	return connections.Item{ID: fmt.Sprint(i), Meta: map[string]any{"format": "json", "index": i},
		Texts: []connections.Text{{Name: "body", Value: connections.Truncate(string(b), maxItemChars)}}}
}

// simulated answers a simulated source with deterministic sample records, so
// an organization can try a source (and its grants) before pasting a key.
func simulated(_ context.Context, h connections.Handle, rawURL string, q url.Values) (int, []byte, string, error) {
	path := strings.TrimPrefix(rawURL, h.Source.BaseURL)
	var recs []map[string]any
	for i := 1; i <= 3; i++ {
		recs = append(recs, map[string]any{"id": fmt.Sprintf("SIM-%03d", i), "path": path, "query": q.Encode(),
			"note": "simulated record: connect the real API to see real data"})
	}
	b, _ := json.Marshal(map[string]any{"data": recs})
	return http.StatusOK, b, "application/json", nil
}

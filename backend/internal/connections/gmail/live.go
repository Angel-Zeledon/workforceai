package gmail

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/mail"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"aiworkforce/backend/internal/connections"
)

const (
	maxResponseBytes = 4 << 20
	maxBodyBytes     = 64 << 10
	maxRecipients    = 20
)

// live executes Gmail API calls through the connection handle's HTTP client
// (which injects the token). Nothing here ever sees a credential.
type live struct {
	base string
	h    connections.Handle
}

type gMessage struct {
	ID           string   `json:"id"`
	ThreadID     string   `json:"threadId"`
	LabelIDs     []string `json:"labelIds"`
	Snippet      string   `json:"snippet"`
	InternalDate string   `json:"internalDate"`
	Payload      gPart    `json:"payload"`
}

type gPart struct {
	MimeType string    `json:"mimeType"`
	Headers  []gHeader `json:"headers"`
	Body     struct {
		Data string `json:"data"`
	} `json:"body"`
	Parts []gPart `json:"parts"`
}

type gHeader struct{ Name, Value string }

func (m gMessage) header(name string) string {
	for _, h := range m.Payload.Headers {
		if strings.EqualFold(h.Name, name) {
			return h.Value
		}
	}
	return ""
}

func (m gMessage) date() time.Time {
	ms, _ := strconv.ParseInt(m.InternalDate, 10, 64)
	return time.UnixMilli(ms).UTC()
}

// do performs a request and maps failures to classified, body-free errors.
func (l *live) do(ctx context.Context, method, path string, q url.Values, body any, out any) (int, int64, error) {
	if l.h.HTTP == nil {
		return 0, 0, &connections.ProviderError{Code: connections.CodeConnectionUnavailable}
	}
	u := l.base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	var rd io.Reader
	var sent int64
	if body != nil {
		b, _ := json.Marshal(body)
		sent = int64(len(b))
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return 0, 0, &connections.ProviderError{Code: connections.CodeInvalidArgs}
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := l.h.HTTP.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return 0, 0, ctx.Err()
		}
		return 0, 0, connections.Classify(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if resp.StatusCode/100 != 2 {
		pe := &connections.ProviderError{Status: resp.StatusCode, Code: statusCode(resp.StatusCode), RetryAfter: retryAfter(resp.Header)}
		if resp.StatusCode == http.StatusForbidden {
			var e struct {
				Error struct {
					Errors []struct{ Reason string } `json:"errors"`
				} `json:"error"`
			}
			_ = json.Unmarshal(raw, &e)
			for _, r := range e.Error.Errors {
				if r.Reason == "rateLimitExceeded" || r.Reason == "userRateLimitExceeded" || r.Reason == "dailyLimitExceeded" {
					pe.Code = "rate_limited"
				}
			}
		}
		return resp.StatusCode, int64(len(raw)), pe
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return resp.StatusCode, int64(len(raw)), &connections.ProviderError{Status: resp.StatusCode, Code: connections.CodeProviderError}
		}
	}
	return resp.StatusCode, int64(len(raw)) + sent, nil
}

func (l *live) Execute(ctx context.Context, c connections.ExecCall) (connections.ExecResult, error) {
	switch c.Action {
	case "search", "list":
		return l.search(ctx, c)
	case "read":
		return l.read(ctx, c)
	case "draft":
		return l.compose(ctx, c, false)
	case "send":
		return l.compose(ctx, c, true)
	}
	return connections.ExecResult{}, &connections.ProviderError{Code: connections.CodeInvalidArgs}
}

var labelSafe = regexp.MustCompile(`[^\p{L}\p{N} _./-]`)

// scopeQuery appends the connection's resource filters to a user query so an
// agent cannot widen them (labels, exclusions, age).
func scopeQuery(q string, s connections.ResourceScope) string {
	var parts []string
	if q = strings.TrimSpace(q); q != "" {
		parts = append(parts, q)
	}
	if len(s.Labels) > 0 {
		var ls []string
		for _, l := range s.Labels {
			ls = append(ls, `label:"`+labelSafe.ReplaceAllString(l, "")+`"`)
		}
		parts = append(parts, "{"+strings.Join(ls, " ")+"}")
	}
	for _, l := range s.ExcludeLabels {
		parts = append(parts, `-label:"`+labelSafe.ReplaceAllString(l, "")+`"`)
	}
	if s.MaxAgeDays > 0 {
		parts = append(parts, fmt.Sprintf("newer_than:%dd", s.MaxAgeDays))
	}
	return strings.Join(parts, " ")
}

func argStr(a map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := a[k]; ok && v != nil {
			if s, ok := v.(string); ok {
				return strings.TrimSpace(s)
			}
			return strings.TrimSpace(fmt.Sprint(v))
		}
	}
	return ""
}

func argInt(a map[string]any, def int, keys ...string) int {
	for _, k := range keys {
		switch v := a[k].(type) {
		case float64:
			return int(v)
		case int:
			return v
		case string:
			if n, err := strconv.Atoi(v); err == nil {
				return n
			}
		}
	}
	return def
}

func argList(a map[string]any, key string) []string {
	switch v := a[key].(type) {
	case string:
		var out []string
		for _, p := range strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ';' }) {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
		return out
	case []any:
		var out []string
		for _, x := range v {
			if s := strings.TrimSpace(fmt.Sprint(x)); s != "" {
				out = append(out, s)
			}
		}
		return out
	case []string:
		return v
	}
	return nil
}

func (l *live) search(ctx context.Context, c connections.ExecCall) (connections.ExecResult, error) {
	max := argInt(c.Args, 10, "max_results", "limit")
	if max < 1 {
		max = 1
	}
	if c.MaxItems > 0 && max > c.MaxItems {
		max = c.MaxItems
	}
	user := argStr(c.Args, "q", "query")
	q := url.Values{"maxResults": {strconv.Itoa(max)}}
	if eff := scopeQuery(user, l.h.ResourceScope); eff != "" {
		q.Set("q", eff)
	}
	var list struct {
		Messages []struct{ ID string } `json:"messages"`
	}
	st, n, err := l.do(ctx, http.MethodGet, "/users/me/messages", q, nil, &list)
	if err != nil {
		return connections.ExecResult{Status: st}, err
	}
	res := connections.ExecResult{Status: st, BytesIn: n, ResourceRef: fmt.Sprintf("search q_len=%d", len(user))}
	for i, m := range list.Messages {
		if i >= max {
			break
		}
		var msg gMessage
		mq := url.Values{"format": {"metadata"}, "metadataHeaders": {"From", "Subject", "Date"}}
		st, n, err := l.do(ctx, http.MethodGet, "/users/me/messages/"+url.PathEscape(m.ID), mq, nil, &msg)
		res.BytesIn += n
		if err != nil {
			return res, err
		}
		res.Status = st
		res.Items = append(res.Items, connections.Item{ID: msg.ID,
			Meta:  map[string]any{"id": msg.ID, "thread_id": msg.ThreadID, "date": msg.date().Format(time.RFC3339), "labels": msg.LabelIDs},
			Texts: []connections.Text{{Name: "from", Value: msg.header("From")}, {Name: "subject", Value: msg.header("Subject")}, {Name: "snippet", Value: msg.Snippet}}})
	}
	res.Summary = fmt.Sprintf("%d messages", len(res.Items))
	return res, nil
}

var authTokens = regexp.MustCompile(`\b(spf|dkim|dmarc)=(pass|fail|softfail|neutral|none|temperror|permerror)\b`)

func (l *live) read(ctx context.Context, c connections.ExecCall) (connections.ExecResult, error) {
	id := argStr(c.Args, "id", "message_id")
	if id == "" || strings.ContainsAny(id, "/?#") {
		return connections.ExecResult{}, &connections.ProviderError{Code: connections.CodeInvalidArgs}
	}
	var msg gMessage
	st, n, err := l.do(ctx, http.MethodGet, "/users/me/messages/"+url.PathEscape(id), url.Values{"format": {"full"}}, nil, &msg)
	res := connections.ExecResult{Status: st, BytesIn: n, ResourceRef: "message"}
	if err != nil {
		return res, err
	}
	if !inScope(msg, l.h.ResourceScope) {
		return res, &connections.ProviderError{Code: connections.CodeOutOfScope}
	}
	body, isHTML := bodyOf(msg.Payload)
	auth := strings.Join(authTokens.FindAllString(msg.header("Authentication-Results"), 6), " ")
	res.Items = []connections.Item{{ID: msg.ID,
		Meta: map[string]any{"id": msg.ID, "thread_id": msg.ThreadID, "date": msg.date().Format(time.RFC3339), "labels": msg.LabelIDs, "authentication": auth},
		Texts: []connections.Text{{Name: "from", Value: msg.header("From")}, {Name: "to", Value: msg.header("To")},
			{Name: "subject", Value: msg.header("Subject")}, {Name: "body", Value: body, HTML: isHTML}}}}
	res.Summary = "1 message"
	return res, nil
}

// inScope enforces the resource scope on a single message (read-by-id would
// otherwise bypass search filters).
func inScope(m gMessage, s connections.ResourceScope) bool {
	has := func(l string) bool {
		for _, x := range m.LabelIDs {
			if strings.EqualFold(x, l) {
				return true
			}
		}
		return false
	}
	if len(s.Labels) > 0 {
		ok := false
		for _, l := range s.Labels {
			ok = ok || has(l)
		}
		if !ok {
			return false
		}
	}
	for _, l := range s.ExcludeLabels {
		if has(l) {
			return false
		}
	}
	if s.MaxAgeDays > 0 && time.Since(m.date()) > time.Duration(s.MaxAgeDays)*24*time.Hour {
		return false
	}
	return true
}

func bodyOf(p gPart) (string, bool) {
	if t, h, ok := findBody(p, "text/plain"); ok {
		return t, h
	}
	if t, _, ok := findBody(p, "text/html"); ok {
		return t, true
	}
	return "", false
}

func findBody(p gPart, mt string) (string, bool, bool) {
	if strings.HasPrefix(strings.ToLower(p.MimeType), mt) && p.Body.Data != "" {
		b, err := base64.URLEncoding.DecodeString(padB64(p.Body.Data))
		if err == nil {
			if len(b) > maxBodyBytes {
				b = b[:maxBodyBytes]
			}
			return string(b), false, true
		}
	}
	for _, c := range p.Parts {
		if t, h, ok := findBody(c, mt); ok {
			return t, h, true
		}
	}
	return "", false, false
}

func padB64(s string) string {
	if m := len(s) % 4; m != 0 {
		s += strings.Repeat("=", 4-m)
	}
	return s
}

var crlf = regexp.MustCompile(`[\r\n]`)

// buildRaw produces an RFC 2822 message after validating every header value
// (no CR/LF injection, parseable addresses, bounded recipients).
func buildRaw(args map[string]any) (raw string, to []string, err error) {
	to = argList(args, "to")
	cc := argList(args, "cc")
	if len(to) == 0 || len(to)+len(cc) > maxRecipients {
		return "", nil, &connections.ProviderError{Code: connections.CodeInvalidArgs}
	}
	norm := func(in []string) ([]string, error) {
		var out []string
		for _, a := range in {
			if crlf.MatchString(a) {
				return nil, &connections.ProviderError{Code: connections.CodeInvalidArgs}
			}
			p, err := mail.ParseAddress(a)
			if err != nil {
				return nil, &connections.ProviderError{Code: connections.CodeInvalidArgs}
			}
			out = append(out, p.Address)
		}
		return out, nil
	}
	if to, err = norm(to); err != nil {
		return "", nil, err
	}
	if cc, err = norm(cc); err != nil {
		return "", nil, err
	}
	subject, body := argStr(args, "subject"), argStr(args, "body")
	if crlf.MatchString(subject) || body == "" {
		return "", nil, &connections.ProviderError{Code: connections.CodeInvalidArgs}
	}
	var b strings.Builder
	b.WriteString("To: " + strings.Join(to, ", ") + "\r\n")
	if len(cc) > 0 {
		b.WriteString("Cc: " + strings.Join(cc, ", ") + "\r\n")
	}
	b.WriteString("Subject: " + mime.QEncoding.Encode("utf-8", subject) + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\nContent-Type: text/plain; charset=\"UTF-8\"\r\nContent-Transfer-Encoding: base64\r\n\r\n")
	enc := base64.StdEncoding.EncodeToString([]byte(body))
	for len(enc) > 76 {
		b.WriteString(enc[:76] + "\r\n")
		enc = enc[76:]
	}
	b.WriteString(enc + "\r\n")
	return base64.URLEncoding.EncodeToString([]byte(b.String())), append(to, cc...), nil
}

func (l *live) compose(ctx context.Context, c connections.ExecCall, send bool) (connections.ExecResult, error) {
	raw, rcpt, err := buildRaw(c.Args)
	if err != nil {
		return connections.ExecResult{}, err
	}
	msg := map[string]any{"raw": raw}
	if t := argStr(c.Args, "thread_id"); t != "" {
		msg["threadId"] = t
	}
	var out struct {
		ID string `json:"id"`
	}
	res := connections.ExecResult{ResourceRef: fmt.Sprintf("recipients=%d", len(rcpt))}
	var st int
	var n int64
	if send {
		st, n, err = l.do(ctx, http.MethodPost, "/users/me/messages/send", nil, msg, &out)
		res.Summary, res.Data = "message sent", map[string]any{"message_id": out.ID}
	} else {
		st, n, err = l.do(ctx, http.MethodPost, "/users/me/drafts", nil, map[string]any{"message": msg}, &out)
		res.Summary, res.Data = "draft created (not sent)", map[string]any{"draft_id": out.ID}
	}
	res.Status, res.BytesOut = st, n
	return res, err
}

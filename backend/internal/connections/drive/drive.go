// Package drive is the Google Drive adapter, read-only: search and list file
// metadata and read the text of documents, only inside the folders the
// connection allows (direct parents; subfolders must be listed explicitly).
// Document text is third-party content: it goes through the gateway's
// sanitizer and reaches the runtime as delimited untrusted data.
package drive

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"aiworkforce/backend/internal/connections"
	"aiworkforce/backend/internal/connections/googleauth"
)

// maxTextBytes caps the text read from one file.
const maxTextBytes = 256 << 10

// Config holds the endpoints (overridable for tests).
type Config struct {
	Auth    googleauth.Endpoints
	APIBase string // https://www.googleapis.com/drive/v3
}

// Provider implements connections.Provider for Google Drive.
type Provider struct {
	*googleauth.OAuth
	base string

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
		cfg.APIBase = "https://www.googleapis.com/drive/v3"
	}
	return &Provider{OAuth: &googleauth.OAuth{E: cfg.Auth.Defaults(), Manifest: ms["google_drive"]}, base: cfg.APIBase, fakes: map[string]*Fake{}}, nil
}

func (p *Provider) Manifest() connections.Manifest { return p.OAuth.Manifest }

// FakeFor returns the simulated drive of a connection.
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

// File is the normalized file (fake and live share it).
type File struct {
	ID, Name, MimeType, Parent, Owner string
	Modified                          time.Time
	Size                              int64
	Text                              string // fake only
}

func (f File) meta() map[string]any {
	return map[string]any{"id": f.ID, "mime_type": f.MimeType, "folder_id": f.Parent, "modified": f.Modified.Format(time.RFC3339), "size": f.Size}
}

type backend interface {
	list(ctx context.Context, folder, nameQuery string, max int) ([]File, int, int64, error)
	meta(ctx context.Context, id string) (File, int, error)
	text(ctx context.Context, f File) (string, int, int64, error)
}

// scoped enforces the folder allowlist on every call (fail closed when empty).
type scoped struct {
	h    connections.Handle
	next backend
}

func (s *scoped) Execute(ctx context.Context, c connections.ExecCall) (connections.ExecResult, error) {
	allowed := s.h.ResourceScope.Folders
	if len(allowed) == 0 {
		return connections.ExecResult{}, connections.OutOfScope() // no folder chosen: nothing is readable
	}
	switch c.Action {
	case "search", "list":
		folders := allowed
		if f := connections.ArgStr(c.Args, "folder_id", "folder"); f != "" {
			if !connections.InList(allowed, f) {
				return connections.ExecResult{}, connections.OutOfScope()
			}
			folders = []string{f}
		}
		q := connections.ArgStr(c.Args, "q", "query", "name")
		max := connections.Limit(c.Args, 10, c.MaxItems)
		res := connections.ExecResult{Status: 200, ResourceRef: fmt.Sprintf("folders=%d q_len=%d", len(folders), len(q))}
		for _, folder := range folders {
			files, st, n, err := s.next.list(ctx, folder, q, max-len(res.Items))
			res.BytesIn += n
			if err != nil {
				return connections.ExecResult{Status: st}, err
			}
			for _, f := range files {
				res.Items = append(res.Items, connections.Item{ID: f.ID, Meta: f.meta(),
					Texts: []connections.Text{{Name: "name", Value: f.Name}, {Name: "owner", Value: f.Owner}}})
			}
			if len(res.Items) >= max {
				break
			}
		}
		res.Summary = fmt.Sprintf("%d files", len(res.Items))
		return res, nil
	case "read":
		id := connections.ArgStr(c.Args, "file_id", "id")
		if id == "" {
			return connections.ExecResult{}, connections.InvalidArgs()
		}
		f, st, err := s.next.meta(ctx, id)
		if err != nil {
			return connections.ExecResult{Status: st}, err
		}
		if !connections.InList(allowed, f.Parent) {
			return connections.ExecResult{}, connections.OutOfScope()
		}
		txt, st, n, err := s.next.text(ctx, f)
		if err != nil {
			return connections.ExecResult{Status: st}, err
		}
		return connections.ExecResult{Status: st, BytesIn: n, Summary: "1 file", ResourceRef: "file in allowed folder",
			Items: []connections.Item{{ID: f.ID, Meta: f.meta(),
				Texts: []connections.Text{{Name: "name", Value: f.Name}, {Name: "owner", Value: f.Owner}, {Name: "text", Value: txt}}}}}, nil
	}
	return connections.ExecResult{}, connections.InvalidArgs()
}

// readable reports whether a file has text the adapter can extract.
func readable(mime string) bool {
	return mime == "application/vnd.google-apps.document" || mime == "application/vnd.google-apps.spreadsheet" ||
		strings.HasPrefix(mime, "text/") || mime == "application/json"
}

// ---- live client ----

type live struct {
	base string
	h    connections.Handle
}

type gFile struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	MimeType     string   `json:"mimeType"`
	Parents      []string `json:"parents"`
	ModifiedTime string   `json:"modifiedTime"`
	Size         string   `json:"size"`
	Owners       []struct {
		EmailAddress string `json:"emailAddress"`
	} `json:"owners"`
}

const fileFields = "id,name,mimeType,parents,modifiedTime,size,owners(emailAddress)"

func (g gFile) file() File {
	f := File{ID: g.ID, Name: g.Name, MimeType: g.MimeType}
	if len(g.Parents) > 0 {
		f.Parent = g.Parents[0]
	}
	if len(g.Owners) > 0 {
		f.Owner = g.Owners[0].EmailAddress
	}
	f.Modified, _ = time.Parse(time.RFC3339, g.ModifiedTime)
	fmt.Sscan(g.Size, &f.Size)
	return f
}

// quote escapes a value for the Drive query language.
func quote(s string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(s) + "'"
}

func (l *live) list(ctx context.Context, folder, nameQuery string, max int) ([]File, int, int64, error) {
	q := quote(folder) + " in parents and trashed = false"
	if nameQuery != "" {
		q += " and name contains " + quote(nameQuery)
	}
	v := url.Values{"q": {q}, "pageSize": {fmt.Sprint(max)}, "fields": {"files(" + fileFields + ")"}, "orderBy": {"modifiedTime desc"}}
	var out struct {
		Files []gFile `json:"files"`
	}
	st, n, err := connections.DoJSON(ctx, l.h, http.MethodGet, l.base+"/files", v, nil, &out)
	if err != nil {
		return nil, st, n, err
	}
	files := make([]File, 0, len(out.Files))
	for _, g := range out.Files {
		files = append(files, g.file())
	}
	return files, st, n, nil
}

func (l *live) meta(ctx context.Context, id string) (File, int, error) {
	var g gFile
	st, _, err := connections.DoJSON(ctx, l.h, http.MethodGet, l.base+"/files/"+url.PathEscape(id), url.Values{"fields": {fileFields}}, nil, &g)
	return g.file(), st, err
}

// text exports Google Docs/Sheets as plain text/CSV or downloads text files.
func (l *live) text(ctx context.Context, f File) (string, int, int64, error) {
	if !readable(f.MimeType) {
		return "", 0, 0, connections.InvalidArgs()
	}
	u := l.base + "/files/" + url.PathEscape(f.ID)
	switch f.MimeType {
	case "application/vnd.google-apps.document":
		u += "/export?mimeType=text%2Fplain"
	case "application/vnd.google-apps.spreadsheet":
		u += "/export?mimeType=text%2Fcsv"
	default:
		u += "?alt=media"
	}
	if l.h.HTTP == nil {
		return "", 0, 0, &connections.ProviderError{Code: connections.CodeConnectionUnavailable}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", 0, 0, connections.InvalidArgs()
	}
	resp, err := l.h.HTTP.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return "", 0, 0, ctx.Err()
		}
		return "", 0, 0, connections.Classify(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxTextBytes))
	if resp.StatusCode/100 != 2 {
		return "", resp.StatusCode, int64(len(raw)), &connections.ProviderError{Status: resp.StatusCode, Code: connections.StatusCode(resp.StatusCode),
			RetryAfter: connections.RetryAfter(resp.Header)}
	}
	return string(raw), resp.StatusCode, int64(len(raw)), nil
}

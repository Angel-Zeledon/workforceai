package drive

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"aiworkforce/backend/internal/connections"
)

// The live client builds a folder-restricted query, exports Docs as plain
// text and still refuses files outside the allowlist after reading metadata.
func TestLiveClientQueriesAndScope(t *testing.T) {
	var queries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/files":
			queries = append(queries, r.URL.Query().Get("q"))
			w.Write([]byte(`{"files":[{"id":"d1","name":"Plan","mimeType":"application/vnd.google-apps.document","parents":["fA"]}]}`))
		case r.URL.Path == "/files/d1/export":
			if r.URL.Query().Get("mimeType") != "text/plain" {
				t.Errorf("export mime = %s", r.URL.Query().Get("mimeType"))
			}
			w.Write([]byte("hola"))
		case r.URL.Path == "/files/d1":
			w.Write([]byte(`{"id":"d1","name":"Plan","mimeType":"application/vnd.google-apps.document","parents":["fA"]}`))
		case r.URL.Path == "/files/d2":
			w.Write([]byte(`{"id":"d2","name":"Otro","mimeType":"text/plain","parents":["fZ"]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	p, err := New(Config{APIBase: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	ex := p.Open(connections.Handle{Mode: connections.ModeLive, HTTP: srv.Client(), ResourceScope: connections.ResourceScope{Folders: []string{"fA"}}})
	res, err := ex.Execute(context.Background(), connections.ExecCall{Action: "search", Args: map[string]any{"q": "it's"}, MaxItems: 5})
	if err != nil || len(res.Items) != 1 {
		t.Fatalf("search: %v %+v", err, res)
	}
	if len(queries) != 1 || !strings.Contains(queries[0], `'fA' in parents`) || !strings.Contains(queries[0], `name contains 'it\'s'`) {
		t.Fatalf("query = %q", queries)
	}
	res, err = ex.Execute(context.Background(), connections.ExecCall{Action: "read", Args: map[string]any{"file_id": "d1"}})
	if err != nil || res.Items[0].Texts[2].Value != "hola" {
		t.Fatalf("read: %v %+v", err, res)
	}
	if _, err := ex.Execute(context.Background(), connections.ExecCall{Action: "read", Args: map[string]any{"file_id": "d2"}}); connections.Classify(err).Code != connections.CodeOutOfScope {
		t.Fatalf("file outside the allowlist: %v", err)
	}
}

package slack

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"aiworkforce/backend/internal/connections"
)

// Slack reports errors as HTTP 200 + {"ok": false}: they must be classified,
// and a post must hit the API exactly once.
func TestLiveClientMapsSlackErrorsAndPostsOnce(t *testing.T) {
	posts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/conversations.history":
			if r.URL.Query().Get("channel") == "C0GONE0001" {
				w.Write([]byte(`{"ok":false,"error":"not_in_channel"}`))
				return
			}
			w.Write([]byte(`{"ok":true,"messages":[{"user":"U1","text":"hola","ts":"1700000000.000100"}]}`))
		case "/chat.postMessage":
			posts++
			w.Write([]byte(`{"ok":true,"ts":"1700000001.000200"}`))
		case "/auth.test":
			w.Write([]byte(`{"ok":false,"error":"invalid_auth"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	p, err := New(Config{APIBase: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	h := connections.Handle{Mode: connections.ModeLive, HTTP: srv.Client(), ResourceScope: connections.ResourceScope{Channels: []string{"C0VENTAS01", "C0GONE0001"}}}
	ex := p.Open(h)
	res, err := ex.Execute(context.Background(), connections.ExecCall{Action: "history", Args: map[string]any{"channel": "C0VENTAS01"}})
	if err != nil || len(res.Items) != 1 || res.Items[0].Texts[1].Value != "hola" {
		t.Fatalf("history: %v %+v", err, res)
	}
	if _, err := ex.Execute(context.Background(), connections.ExecCall{Action: "history", Args: map[string]any{"channel": "C0GONE0001"}}); connections.Classify(err).Code != "not_found" {
		t.Fatalf("ok:false must be classified, got %v", err)
	}
	res, err = ex.Execute(context.Background(), connections.ExecCall{Action: "post_message", Args: map[string]any{"channel": "C0VENTAS01", "text": "Listo"}})
	if err != nil || res.Data["message_ts"] != "1700000001.000200" || posts != 1 {
		t.Fatalf("post: %v %+v posts=%d", err, res, posts)
	}
	if tr := p.Test(context.Background(), h); tr.OK || tr.Code != connections.CodeReauthRequired {
		t.Fatalf("test with a revoked token: %+v", tr)
	}
	// Direct messages to arbitrary users are not a supported target.
	if _, err := ex.Execute(context.Background(), connections.ExecCall{Action: "post_message", Args: map[string]any{"channel": "U0SOMEONE1", "text": "x"}}); connections.Classify(err).Code != connections.CodeInvalidArgs {
		t.Fatalf("DM target accepted: %v", err)
	}
}

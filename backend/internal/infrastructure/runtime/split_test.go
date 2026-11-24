package runtime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"aiworkforce/backend/internal/application"
)

func TestClientSplitSpeaksTheContract(t *testing.T) {
	var path string
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = w.Write([]byte(`{"worth_it":true,"reason":"r","parts":[{"label":"A","instructions":"i","item_refs":["f1"]}],"source":"rules","usage":{"model":"m","cost_usd":0.001}}`))
	}))
	defer srv.Close()
	out, err := New(srv.URL).Split(context.Background(), application.SplitRequest{Text: "x", Items: []application.SplitItem{{Name: "a.pdf", Ref: "f1"}}, MaxParts: 8, Locale: "es"})
	if err != nil {
		t.Fatal(err)
	}
	if path != "/v1/split" || body["max_parts"] != float64(8) || !out.WorthIt || len(out.Parts) != 1 || out.Parts[0].ItemRefs[0] != "f1" || out.Usage == nil || out.Usage.CostUSD != 0.001 {
		t.Fatalf("path=%q body=%v out=%+v", path, body, out)
	}
}

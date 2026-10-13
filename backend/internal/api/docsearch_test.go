package api_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"aiworkforce/backend/internal/api"
	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/docindex"
	"aiworkforce/backend/internal/drive"
	"aiworkforce/backend/internal/objstore"
)

func TestDocSearchAPI(t *testing.T) {
	cfg := application.DefaultConfig()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	dr := drive.New(drive.Config{Store: drive.NewMemStore(), Objects: objstore.NewMemory(), OrgID: cfg.OrgID, Log: log})
	idx := docindex.New(docindex.Config{Store: docindex.NewMemStore(dr), Drive: dr, OrgID: cfg.OrgID, Log: log})
	ctx := application.WithOrg(context.Background(), cfg.OrgID)
	if _, err := dr.Write(ctx, drive.Human("u"), drive.WriteInput{Path: "/Shared/contract.txt", Data: []byte("[page 1]\nIntro.\n[page 2]\nThe penalty clause applies after 30 days.\n")}); err != nil {
		t.Fatal(err)
	}
	idx.Sync(ctx, cfg.OrgID)
	srv := httptest.NewServer(api.NewRouter(api.Deps{Cfg: cfg, Log: log, Drive: dr, DocIndex: idx}))
	defer srv.Close()
	get := func(path string, out any) int {
		res, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		_ = json.NewDecoder(res.Body).Decode(out)
		return res.StatusCode
	}
	var r struct {
		Enabled bool             `json:"enabled"`
		Mode    string           `json:"mode"`
		Note    string           `json:"note"`
		Hits    []drive.ChunkHit `json:"hits"`
		Status  map[string]any   `json:"status"`
		Emb     map[string]any   `json:"embeddings"`
	}
	if code := get("/api/v1/drive/passages?q=penalty+clause", &r); code != 200 || !r.Enabled || len(r.Hits) != 1 || r.Hits[0].Page != 2 || r.Mode != "keyword" {
		t.Fatalf("passages: %d %+v", code, r)
	}
	if code := get("/api/v1/drive/passages?q=x&mode=magic", &r); code != 400 {
		t.Fatalf("bad mode: %d", code)
	}
	r.Status = nil
	if code := get("/api/v1/drive/index/status?path=/Shared", &r); code != 200 || r.Status["indexed"].(float64) != 1 || r.Emb["available"] != false {
		t.Fatalf("status: %d %+v", code, r)
	}
	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/api/v1/settings/doc-index", strings.NewReader(`{"enabled":false}`))
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil || res.StatusCode != 200 {
		t.Fatalf("put: %v %v", err, res)
	}
	res.Body.Close()
	r.Enabled = true
	if code := get("/api/v1/drive/passages?q=penalty", &r); code != 200 || r.Enabled || len(r.Hits) != 0 {
		t.Fatalf("off: %d %+v", code, r)
	}
}

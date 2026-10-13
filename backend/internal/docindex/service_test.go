package docindex_test

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/docindex"
	"aiworkforce/backend/internal/drive"
	"aiworkforce/backend/internal/objstore"
)

// fakeEmbedder maps words to "concepts" so that synonyms are close in meaning
// while sharing no word (what keyword search cannot do).
type fakeEmbedder struct{ calls, texts atomic.Int64 }

var concepts = map[string]int{"car": 0, "automobile": 0, "vehicle": 0, "invoice": 1, "bill": 1, "factura": 1, "contract": 2, "agreement": 2, "merger": 3}

func (f *fakeEmbedder) Provider() string { return "openai" }
func (f *fakeEmbedder) Model() string    { return "fake-embed" }
func (f *fakeEmbedder) Embed(_ context.Context, texts []string) ([][]float32, int, error) {
	f.calls.Add(1)
	f.texts.Add(int64(len(texts)))
	out := make([][]float32, len(texts))
	tokens := 0
	for i, t := range texts {
		v := make([]float32, 32)
		for _, w := range strings.FieldsFunc(strings.ToLower(t), func(r rune) bool { return r < 'a' || r > 'z' }) {
			tokens++
			if d, ok := concepts[w]; ok {
				v[d] += 5
				continue
			}
			h := fnv.New32a()
			_, _ = h.Write([]byte(w))
			v[4+int(h.Sum32()%28)] += 0.05
		}
		out[i] = v
	}
	return out, tokens, nil
}

type env struct {
	drive *drive.Service
	idx   *docindex.Service
	store *docindex.MemStore
	emb   *fakeEmbedder
	ctx   context.Context
	allow *atomic.Bool
}

const org = "o1"

func newEnv(t *testing.T, lim docindex.Limits, withEmb bool) *env {
	t.Helper()
	e := &env{emb: &fakeEmbedder{}, ctx: application.WithOrg(context.Background(), org), allow: &atomic.Bool{}}
	e.allow.Store(true)
	var idx *docindex.Service
	e.drive = drive.New(drive.Config{Store: drive.NewMemStore(), Objects: objstore.NewMemory(), OrgID: org,
		Audit: func(_ context.Context, o string, a drive.AuditEntry) {
			if idx != nil {
				idx.OnDriveAudit(o, a.Action, a.Entity, a.EntityID)
			}
		}})
	e.store = docindex.NewMemStore(e.drive)
	cfg := docindex.Config{Store: e.store, Drive: e.drive, OrgID: org, Limits: lim}
	if withEmb {
		cfg.Embeddings = func(context.Context, string) docindex.EmbedChoice {
			if !e.allow.Load() {
				return docindex.EmbedChoice{Reason: docindex.ReasonEmbedPolicy, Note: "the data policy does not allow sending text to openai: search is keyword only"}
			}
			return docindex.EmbedChoice{Embedder: e.emb}
		}
	}
	idx = docindex.New(cfg)
	e.idx = idx
	return e
}

func (e *env) write(t *testing.T, path, text string) drive.File {
	t.Helper()
	r, err := e.drive.Write(e.ctx, drive.Human("u1"), drive.WriteInput{Path: path, Data: []byte(text)})
	if err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return r.File
}

func (e *env) search(t *testing.T, q, mode string, under []string) drive.ChunkResult {
	t.Helper()
	r, err := e.idx.SearchChunks(e.ctx, drive.ChunkQuery{Query: q, Mode: mode, Under: under, Limit: 10})
	if err != nil {
		t.Fatalf("search %q: %v", q, err)
	}
	return r
}

func report() string {
	var b strings.Builder
	for p := 1; p <= 30; p++ {
		fmt.Fprintf(&b, "[page %d]\nAnnual report section %d. Operations continued as planned during this period with steady results.\n", p, p)
		if p == 17 {
			b.WriteString("The supplier invoice 4471 was paid late because of a dispute about delivery terms.\n")
		}
		if p == 23 {
			b.WriteString("Our sales team bought a new car to visit clients in the north region.\n")
		}
	}
	return b.String()
}

func TestBackfillKeywordSemanticAndHybrid(t *testing.T) {
	e := newEnv(t, docindex.Limits{}, true)
	e.write(t, "/Shared/report-2025.txt", report()) // written before the index runs: backfill
	e.write(t, "/Shared/notes.md", "# Notes\n\nNothing about vehicles here, only the weekly meeting minutes.")
	if n := e.idx.Sync(e.ctx, org); n != 2 {
		t.Fatalf("backfill indexed %d files", n)
	}
	r := e.search(t, "invoice 4471", "keyword", nil)
	if len(r.Hits) == 0 || r.Hits[0].Path != "/Shared/report-2025.txt" || r.Hits[0].Page != 17 || r.Hits[0].Version != 1 {
		t.Fatalf("keyword hit: %+v", r.Hits)
	}
	if !strings.Contains(r.Hits[0].Snippet, "«invoice»") || len([]rune(r.Hits[0].Snippet)) > docindex.SnippetMax {
		t.Fatalf("snippet: %q", r.Hits[0].Snippet)
	}
	// "automobile" shares no word with "car": only meaning finds it
	if kw := e.search(t, "automobile", "keyword", nil); len(kw.Hits) != 0 {
		t.Fatalf("keyword should not find a synonym: %+v", kw.Hits)
	}
	sem := e.search(t, "automobile", "semantic", nil)
	if sem.Mode != drive.SearchSemantic || len(sem.Hits) == 0 || sem.Hits[0].Page != 23 {
		t.Fatalf("semantic: %+v", sem)
	}
	h := e.search(t, "invoice", "", nil)
	if h.Mode != drive.SearchHybrid || len(h.Hits) == 0 || h.Hits[0].Page != 17 {
		t.Fatalf("hybrid: mode %s hits %+v", h.Mode, h.Hits)
	}
	if used, _ := e.store.EmbedUsage(e.ctx, org, time.Now()); used == 0 {
		t.Fatal("embedding tokens must be booked")
	}
	st, _ := e.idx.Status(e.ctx, "/Shared")
	if st.Indexed != 2 || st.Chunks == 0 || st.Embedded != st.Chunks {
		t.Fatalf("status: %+v", st)
	}
}

func TestReindexNewVersionOnlyCurrentIsSearchable(t *testing.T) {
	e := newEnv(t, docindex.Limits{}, true)
	f := e.write(t, "/Shared/plan.md", "The project codename is aurora and it starts in May.")
	e.idx.Sync(e.ctx, org)
	if r := e.search(t, "aurora", "keyword", nil); len(r.Hits) != 1 {
		t.Fatalf("v1: %+v", r.Hits)
	}
	before := e.emb.texts.Load()
	e.write(t, "/Shared/plan.md", "The project codename is borealis and it starts in June.")
	// the old version is hidden at once, before the worker re-indexes
	if r := e.search(t, "aurora", "keyword", nil); len(r.Hits) != 0 {
		t.Fatalf("an old version must not be searchable: %+v", r.Hits)
	}
	e.idx.Sync(e.ctx, org)
	r := e.search(t, "borealis", "keyword", nil)
	if len(r.Hits) != 1 || r.Hits[0].Version != 2 || r.Hits[0].FileID != f.ID {
		t.Fatalf("v2: %+v", r.Hits)
	}
	if e.emb.texts.Load() == before {
		t.Fatal("the changed chunk must be embedded again")
	}
	// restoring v1 makes v3 (same text as v1): its unchanged chunk reuses the stored vector? (new hash vs v2) -> embedded
	if _, err := e.drive.RestoreVersion(e.ctx, drive.Human("u1"), f.ID, 1, nil); err != nil {
		t.Fatal(err)
	}
	e.idx.Sync(e.ctx, org)
	if r := e.search(t, "aurora", "keyword", nil); len(r.Hits) != 1 || r.Hits[0].Version != 3 {
		t.Fatalf("restore: %+v", r.Hits)
	}
}

func TestTrashHidesAndRemovesHits(t *testing.T) {
	e := newEnv(t, docindex.Limits{}, false)
	f := e.write(t, "/Shared/old.md", "Quarterly zeppelin inventory.")
	e.idx.Sync(e.ctx, org)
	if r := e.search(t, "zeppelin", "", nil); len(r.Hits) != 1 {
		t.Fatalf("before: %+v", r.Hits)
	}
	if err := e.drive.DeleteFile(e.ctx, drive.Human("u1"), f.ID); err != nil {
		t.Fatal(err)
	}
	if r := e.search(t, "zeppelin", "", nil); len(r.Hits) != 0 {
		t.Fatalf("a file in the trash must never be a hit: %+v", r.Hits)
	}
	e.idx.Sync(e.ctx, org)
	if n, _ := e.store.ChunkCount(e.ctx, org); n != 0 {
		t.Fatalf("chunks of a trashed file must be removed, %d left", n)
	}
	if _, err := e.drive.RestoreFile(e.ctx, drive.Human("u1"), f.ID); err != nil {
		t.Fatal(err)
	}
	e.idx.Sync(e.ctx, org)
	if r := e.search(t, "zeppelin", "", nil); len(r.Hits) != 1 {
		t.Fatalf("after restore: %+v", r.Hits)
	}
}

func TestAgentSearchNeverLeaksForbiddenFolders(t *testing.T) {
	e := newEnv(t, docindex.Limits{}, true)
	e.write(t, "/Agents/legal/secret-merger.md", "Confidential: the merger with Acme closes in March. Merger price 40M.")
	e.write(t, "/Clients/acme/terms.md", "Merger clause 7 for Acme.")
	e.write(t, "/Shared/public.md", "Office hours and the parking policy.")
	e.write(t, "/Agents/sales/mine.md", "My pipeline notes: call Acme about the renewal.")
	e.idx.Sync(e.ctx, org)
	fs := &drive.AgentFS{Svc: e.drive, Chunks: e.idx}
	roots, err := fs.ReadableRoots(e.ctx, "sales")
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"keyword", "semantic", "hybrid"} {
		// the index itself, scoped to what sales can read: nothing, not even a count
		r := e.search(t, "merger", mode, roots)
		if len(r.Hits) != 0 {
			t.Fatalf("%s: forbidden hits leaked: %+v", mode, r.Hits)
		}
		// through the agent tool
		rr := fs.Run(e.ctx, "sales", drive.ActSearch, map[string]any{"query": "merger", "mode": mode}, false)
		if !rr.OK || strings.Contains(rr.Text, "legal") || strings.Contains(rr.Text, "Clients") || strings.Contains(rr.Text, "40M") {
			t.Fatalf("%s: agent saw forbidden content: %+v", mode, rr)
		}
		if !strings.HasPrefix(rr.Summary, "0 passage") {
			t.Fatalf("%s: summary must count only readable hits: %q", mode, rr.Summary)
		}
	}
	// default mode with no readable hit falls back to the file search, also scoped
	rr := fs.Run(e.ctx, "sales", drive.ActSearch, map[string]any{"query": "merger"}, false)
	if strings.Contains(rr.Text, "legal") || strings.Contains(rr.Text, "Acme closes") {
		t.Fatalf("fallback leaked: %+v", rr)
	}
	// a path outside the readable areas is refused, a readable one works
	if rr := fs.Run(e.ctx, "sales", drive.ActSearch, map[string]any{"query": "merger", "path": "/Agents/legal"}, false); rr.OK {
		t.Fatalf("scoped search into another desk must be refused: %+v", rr)
	}
	rr = fs.Run(e.ctx, "sales", drive.ActSearch, map[string]any{"query": "Acme renewal", "mode": "keyword"}, false)
	if !rr.OK || !strings.Contains(rr.Text, "[/Agents/sales/mine.md l.1]") {
		t.Fatalf("own desk hit with citation: %+v", rr)
	}
	// a human grant opens /Clients/acme for reading: now it is a hit
	if _, err := e.drive.AddGrant(e.ctx, drive.Human("admin"), "sales", "/Clients/acme"); err != nil {
		t.Fatal(err)
	}
	rr = fs.Run(e.ctx, "sales", drive.ActSearch, map[string]any{"query": "merger clause", "mode": "keyword"}, false)
	if !strings.Contains(rr.Text, "/Clients/acme/terms.md") || strings.Contains(rr.Text, "legal") {
		t.Fatalf("granted folder: %+v", rr)
	}
	// an explicitly empty scope is nothing
	if r := e.search(t, "merger", "hybrid", []string{}); len(r.Hits) != 0 {
		t.Fatal("empty scope must return nothing")
	}
}

func TestTenantIsolation(t *testing.T) {
	e := newEnv(t, docindex.Limits{}, true)
	e.write(t, "/Shared/a.md", "Tenant A blueprint of the warehouse.")
	e.idx.Sync(e.ctx, org)
	ctxB := application.WithOrg(context.Background(), "o2")
	if _, err := e.drive.Write(ctxB, drive.Human("u2"), drive.WriteInput{Path: "/Shared/b.md", Data: []byte("Tenant B roadmap.")}); err != nil {
		t.Fatal(err)
	}
	e.idx.Sync(ctxB, "o2")
	r, err := e.idx.SearchChunks(ctxB, drive.ChunkQuery{Query: "blueprint warehouse", Mode: "hybrid"})
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range r.Hits {
		if strings.Contains(h.Snippet, "Tenant A") {
			t.Fatalf("org o2 sees org o1: %+v", h)
		}
	}
	if r, _ := e.idx.SearchChunks(e.ctx, drive.ChunkQuery{Query: "roadmap", Mode: "keyword"}); len(r.Hits) != 0 {
		t.Fatalf("org o1 sees org o2: %+v", r.Hits)
	}
}

func TestLimitsAndSkips(t *testing.T) {
	e := newEnv(t, docindex.Limits{MaxChunksPerFile: 3, DailyEmbedTokens: 5}, true)
	e.write(t, "/Shared/big.txt", strings.Repeat("A long paragraph about budgets and forecasts for the next year. ", 400))
	e.write(t, "/Shared/small.md", "Short note about the picnic.")
	if _, err := e.drive.Write(e.ctx, drive.Human("u1"), drive.WriteInput{Path: "/Shared/logo.png", Data: append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 64)...)}); err != nil {
		t.Fatal(err)
	}
	e.idx.Sync(e.ctx, org)
	st, _ := e.idx.Status(e.ctx, "/Shared")
	if st.TooLarge != 1 || st.Skipped < 1 {
		t.Fatalf("too large: %+v", st)
	}
	// the daily embeddings cap (5 tokens) leaves the small note keyword only
	if st.Indexed < 1 || st.Embedded != 0 {
		t.Fatalf("embedding cap: %+v", st)
	}
	r := e.search(t, "picnic", "", nil)
	if len(r.Hits) != 1 || r.Mode != drive.SearchKeyword || !strings.Contains(r.Note, "embeddings") {
		t.Fatalf("cap reached must degrade to keyword with a note: %+v", r)
	}
	// per-org cap
	e2 := newEnv(t, docindex.Limits{MaxChunksPerOrg: 1}, false)
	e2.write(t, "/Shared/one.md", "First file about apples.")
	e2.write(t, "/Shared/two.md", "Second file about pears.")
	e2.idx.Sync(e2.ctx, org)
	st2, _ := e2.idx.Status(e2.ctx, "/")
	if st2.Indexed != 1 || st2.Skipped != 1 {
		t.Fatalf("org cap: %+v", st2)
	}
}

func TestDataPolicyKeepsKeywordOnly(t *testing.T) {
	e := newEnv(t, docindex.Limits{}, true)
	e.allow.Store(false)
	e.write(t, "/Shared/fleet.md", "We lease every car through one provider.")
	e.idx.Sync(e.ctx, org)
	r := e.search(t, "car lease", "", nil)
	if r.Mode != drive.SearchKeyword || !strings.Contains(r.Note, "data policy") || len(r.Hits) != 1 {
		t.Fatalf("policy: %+v", r)
	}
	if e.emb.calls.Load() != 0 {
		t.Fatal("no text may reach a provider the policy forbids")
	}
	if r := e.search(t, "automobile", "semantic", nil); r.Mode != drive.SearchKeyword || r.Note == "" {
		t.Fatalf("semantic asked under policy: %+v", r)
	}
}

func TestIndexOffFallsBackAndDropsIndex(t *testing.T) {
	e := newEnv(t, docindex.Limits{}, false)
	e.write(t, "/Shared/x.md", "Kangaroo budget.")
	e.idx.Sync(e.ctx, org)
	if _, err := e.idx.SetEnabled(e.ctx, "admin", false); err != nil {
		t.Fatal(err)
	}
	if _, err := e.idx.SearchChunks(e.ctx, drive.ChunkQuery{Query: "kangaroo"}); err != drive.ErrIndexOff {
		t.Fatalf("off: %v", err)
	}
	if n, _ := e.store.ChunkCount(e.ctx, org); n != 0 {
		t.Fatal("turning the index off deletes it")
	}
	fs := &drive.AgentFS{Svc: e.drive, Chunks: e.idx}
	rr := fs.Run(e.ctx, "sales", drive.ActSearch, map[string]any{"query": "kangaroo"}, false)
	if !rr.OK || !strings.Contains(rr.Text, "/Shared/x.md") {
		t.Fatalf("the file-level search keeps working: %+v", rr)
	}
	if _, err := e.idx.SetEnabled(e.ctx, "admin", true); err != nil {
		t.Fatal(err)
	}
	e.idx.Sync(e.ctx, org)
	if r := e.search(t, "kangaroo", "", nil); len(r.Hits) != 1 {
		t.Fatalf("re-enabled: %+v", r.Hits)
	}
}

func TestReadByPageAndRange(t *testing.T) {
	e := newEnv(t, docindex.Limits{}, false)
	e.write(t, "/Shared/report.txt", report())
	fs := &drive.AgentFS{Svc: e.drive}
	rr := fs.Run(e.ctx, "sales", drive.ActRead, map[string]any{"path": "/Shared/report.txt", "page": 17, "max_chars": 300}, false)
	if !rr.OK || !strings.Contains(rr.Text, "page 17 starts at offset") || !strings.Contains(rr.Text, "invoice 4471") {
		t.Fatalf("page read: %+v", rr)
	}
	rr = fs.Run(e.ctx, "sales", drive.ActRead, map[string]any{"path": "/Shared/report.txt", "range": "0-40"}, false)
	if !rr.OK || !strings.Contains(rr.Text, "[page 1]") || strings.Contains(rr.Text, "[page 2]") {
		t.Fatalf("range read: %+v", rr)
	}
	if rr := fs.Run(e.ctx, "sales", drive.ActRead, map[string]any{"path": "/Shared/report.txt", "range": "9-3"}, false); rr.OK {
		t.Fatal("bad range must be refused")
	}
}

func TestOpenAIEmbedderAgainstFakeServer(t *testing.T) {
	var gotAuth, gotModel string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		var in struct {
			Model string   `json:"model"`
			Input []string `json:"input"`
		}
		_ = json.Unmarshal(body, &in)
		gotModel = in.Model
		if r.URL.Path != "/v1/embeddings" || len(in.Input) == 0 {
			w.WriteHeader(400)
			return
		}
		type d struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		}
		var data []d
		for i := len(in.Input) - 1; i >= 0; i-- { // out of order on purpose
			data = append(data, d{Index: i, Embedding: []float32{float32(i), 1}})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data, "usage": map[string]int{"prompt_tokens": 7, "total_tokens": 7}})
	}))
	defer srv.Close()
	em := &docindex.OpenAIEmbedder{ProviderID: "openai", Key: "sk-test-123", ModelName: "text-embedding-3-small", BaseURL: srv.URL + "/v1"}
	texts := make([]string, docindex.MaxBatch+3)
	for i := range texts {
		texts[i] = fmt.Sprintf("t%d", i)
	}
	vs, tok, err := em.Embed(context.Background(), texts)
	if err != nil || len(vs) != len(texts) || vs[5][0] != 5 || tok != 14 {
		t.Fatalf("embed: %v %d %d", err, len(vs), tok)
	}
	if gotAuth != "Bearer sk-test-123" || gotModel != "text-embedding-3-small" {
		t.Fatalf("request: %q %q", gotAuth, gotModel)
	}
	bad := &docindex.OpenAIEmbedder{ProviderID: "openai", Key: "sk-secret-999", ModelName: "m", BaseURL: srv.URL + "/nope"}
	if _, _, err := bad.Embed(context.Background(), []string{"x"}); err == nil || strings.Contains(err.Error(), "sk-secret") {
		t.Fatalf("errors must not carry the key: %v", err)
	}
	if strings.Contains(fmt.Sprint(bad), "sk-secret") {
		t.Fatal("the embedder must not print its key")
	}
}

func TestResolverRules(t *testing.T) {
	plat := docindex.EmbedConfig{Provider: "openai", APIKey: "k", Model: "text-embedding-3-small"}
	r := docindex.NewResolver(docindex.ResolverConfig{Platform: plat})
	if ch := r(context.Background(), org); ch.Embedder == nil || ch.Embedder.Provider() != "openai" {
		t.Fatalf("platform: %+v", ch)
	}
	r = docindex.NewResolver(docindex.ResolverConfig{Platform: plat, Allowed: func(_ context.Context, _, p string) bool { return p != "openai" }})
	if ch := r(context.Background(), org); ch.Embedder != nil || ch.Reason != docindex.ReasonEmbedPolicy {
		t.Fatalf("policy: %+v", ch)
	}
	// an organization on its own Anthropic key: never the platform key, keyword only
	r = docindex.NewResolver(docindex.ResolverConfig{Platform: plat, OrgCredential: func(context.Context, string) (string, string, string, string, string, bool) {
		return "anthropic", "ak", "claude", "", "", true
	}})
	if ch := r(context.Background(), org); ch.Embedder != nil {
		t.Fatalf("byo anthropic must stay keyword only: %+v", ch)
	}
	// its own OpenAI key embeds, even when the platform has none
	r = docindex.NewResolver(docindex.ResolverConfig{OrgCredential: func(context.Context, string) (string, string, string, string, string, bool) {
		return "openai", "ok", "gpt-4.1", "", "", true
	}})
	if ch := r(context.Background(), org); ch.Embedder == nil || ch.Embedder.Model() != docindex.DefaultEmbedModel {
		t.Fatalf("byo openai: %+v", ch)
	}
	if ch := docindex.NewResolver(docindex.ResolverConfig{})(context.Background(), org); ch.Embedder != nil || ch.Reason != docindex.ReasonEmbedOff {
		t.Fatalf("none: %+v", ch)
	}
}

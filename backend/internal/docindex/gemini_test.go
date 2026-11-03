package docindex_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/docindex"
	"aiworkforce/backend/internal/drive"
	"aiworkforce/backend/internal/objstore"
)

type geminiCall struct {
	path, key string
	reqs      []struct {
		Model   string `json:"model"`
		Content struct {
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"content"`
		OutputDimensionality int `json:"outputDimensionality"`
	}
}

// fakeGemini answers batchEmbedContents with vectors of dims values (one per
// request), concept-based like fakeEmbedder so synonyms are close.
func fakeGemini(t *testing.T, dims int, usage bool) (*httptest.Server, func() []geminiCall) {
	t.Helper()
	var mu sync.Mutex
	var calls []geminiCall
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var c geminiCall
		c.path, c.key = r.URL.Path, r.Header.Get("x-goog-api-key")
		body, _ := io.ReadAll(r.Body)
		var in struct {
			Requests json.RawMessage `json:"requests"`
		}
		if r.Method != http.MethodPost || json.Unmarshal(body, &in) != nil || json.Unmarshal(in.Requests, &c.reqs) != nil {
			w.WriteHeader(400)
			return
		}
		mu.Lock()
		calls = append(calls, c)
		mu.Unlock()
		type emb struct {
			Values []float32 `json:"values"`
		}
		out := make([]emb, len(c.reqs))
		tokens := 0
		for i, rq := range c.reqs {
			v := make([]float32, dims)
			text := ""
			if len(rq.Content.Parts) > 0 {
				text = rq.Content.Parts[0].Text
			}
			// drop the task prefix so documents and queries share the concept space
			if j := strings.LastIndex(text, ": "); j >= 0 {
				text = text[j+2:]
			}
			for _, w := range strings.Fields(strings.ToLower(text)) {
				tokens++
				if d, ok := concepts[strings.Trim(w, ".,")]; ok {
					v[d] += 5
				} else {
					v[4+len(w)%(dims-4)] += 0.05
				}
			}
			v[dims-1] += 0.01
			out[i] = emb{Values: v}
		}
		resp := map[string]any{"embeddings": out}
		if usage {
			resp["usageMetadata"] = map[string]int{"promptTokenCount": tokens}
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)
	return srv, func() []geminiCall { mu.Lock(); defer mu.Unlock(); return append([]geminiCall(nil), calls...) }
}

func TestGeminiEmbedderRequestShapeAndParsing(t *testing.T) {
	srv, calls := fakeGemini(t, 1536, true)
	em := &docindex.GeminiEmbedder{Key: "AIza-test-123", BaseURL: srv.URL + "/v1beta", Dims: 1536}
	if em.Provider() != "gemini" || em.Model() != "gemini-embedding-2@1536" {
		t.Fatalf("identity: %s %s", em.Provider(), em.Model())
	}
	texts := make([]string, docindex.MaxBatch+2)
	for i := range texts {
		texts[i] = fmt.Sprintf("invoice number %d", i)
	}
	vs, tok, err := em.Embed(context.Background(), texts)
	if err != nil || len(vs) != len(texts) || tok == 0 {
		t.Fatalf("embed: %v %d %d", err, len(vs), tok)
	}
	for _, v := range vs {
		var n float64
		for _, x := range v {
			n += float64(x) * float64(x)
		}
		if len(v) != 1536 || math.Abs(n-1) > 1e-4 {
			t.Fatalf("vector not normalized or wrong size: len %d norm %f", len(v), n)
		}
	}
	cs := calls()
	if len(cs) != 2 || len(cs[0].reqs) != docindex.MaxBatch || len(cs[1].reqs) != 2 {
		t.Fatalf("batches: %d", len(cs))
	}
	c := cs[0]
	if c.path != "/v1beta/models/gemini-embedding-2:batchEmbedContents" || c.key != "AIza-test-123" {
		t.Fatalf("request: %s %q", c.path, c.key)
	}
	r0 := c.reqs[0]
	if r0.Model != "models/gemini-embedding-2" || r0.OutputDimensionality != 1536 || len(r0.Content.Parts) != 1 ||
		r0.Content.Parts[0].Text != "title: none | text: invoice number 0" {
		t.Fatalf("document request: %+v", r0)
	}
	if _, _, err := em.EmbedQuery(context.Background(), []string{"where is the bill"}); err != nil {
		t.Fatal(err)
	}
	q := calls()[2].reqs[0]
	if q.Content.Parts[0].Text != "task: search result | query: where is the bill" {
		t.Fatalf("query prefix: %q", q.Content.Parts[0].Text)
	}
}

func TestGeminiEmbedderErrorsNeverCarryKey(t *testing.T) {
	srv, _ := fakeGemini(t, 768, false) // answers 768 values while 1536 are asked
	em := &docindex.GeminiEmbedder{Key: "AIza-secret-999", BaseURL: srv.URL}
	_, _, err := em.Embed(context.Background(), []string{"secret text"})
	if err == nil || !strings.Contains(err.Error(), "dimensions") || strings.Contains(err.Error(), "AIza-secret") || strings.Contains(err.Error(), "secret text") {
		t.Fatalf("dimension mismatch must fail without key or text: %v", err)
	}
	bad := &docindex.GeminiEmbedder{Key: "AIza-secret-999", BaseURL: srv.URL + "/nope", ModelName: "x/../y"}
	srv404 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(403) }))
	defer srv404.Close()
	bad.BaseURL = srv404.URL
	if _, _, err := bad.Embed(context.Background(), []string{"x"}); err == nil || strings.Contains(err.Error(), "AIza-secret") {
		t.Fatalf("status error: %v", err)
	}
	if s := fmt.Sprint(bad); strings.Contains(s, "AIza-secret") {
		t.Fatalf("String leaks the key: %s", s)
	}
	// missing usage metadata: no tokens reported, still vectors
	srv2, _ := fakeGemini(t, 256, false)
	vs, tok, err := (&docindex.GeminiEmbedder{Key: "k", BaseURL: srv2.URL, Dims: 256}).Embed(context.Background(), []string{"car"})
	if err != nil || len(vs) != 1 || len(vs[0]) != 256 || tok != 0 {
		t.Fatalf("no usage: %v %d", err, tok)
	}
}

func TestEmbedConfigFromEnvGemini(t *testing.T) {
	t.Setenv("EMBEDDINGS_PROVIDER", "gemini")
	t.Setenv("EMBEDDINGS_API_KEY", "")
	t.Setenv("EMBEDDINGS_MODEL", "")
	t.Setenv("EMBEDDINGS_BASE_URL", "")
	t.Setenv("EMBEDDINGS_DIMENSIONS", "")
	t.Setenv("GEMINI_API_KEY", "AIza-chat-key")
	c := docindex.EmbedConfigFromEnv()
	if c.Provider != "gemini" || c.APIKey != "AIza-chat-key" || c.Model != "gemini-embedding-2" || c.Dimensions != 1536 {
		t.Fatalf("gemini defaults: %+v", c)
	}
	t.Setenv("EMBEDDINGS_API_KEY", "AIza-own")
	t.Setenv("EMBEDDINGS_DIMENSIONS", "768")
	if c := docindex.EmbedConfigFromEnv(); c.APIKey != "AIza-own" || c.Dimensions != 768 {
		t.Fatalf("explicit: %+v", c)
	}
	t.Setenv("EMBEDDINGS_DIMENSIONS", "99999")
	if c := docindex.EmbedConfigFromEnv(); c.Dimensions != 1536 {
		t.Fatalf("out of range dims: %+v", c)
	}
	t.Setenv("EMBEDDINGS_API_KEY", "")
	t.Setenv("GEMINI_API_KEY", "")
	if c := docindex.EmbedConfigFromEnv(); c.Provider != "" {
		t.Fatalf("no key = off: %+v", c)
	}
	// OpenAI stays as before
	t.Setenv("EMBEDDINGS_PROVIDER", "")
	t.Setenv("EMBEDDINGS_API_KEY", "sk-x")
	if c := docindex.EmbedConfigFromEnv(); c.Provider != "openai" || c.Model != docindex.DefaultEmbedModel || c.Dimensions != 0 {
		t.Fatalf("openai: %+v", c)
	}
	if docindex.PricePerM("gemini") != 0.20 || docindex.PricePerM("openai") != 0.02 {
		t.Fatal("default prices")
	}
}

func TestResolverGemini(t *testing.T) {
	plat := docindex.EmbedConfig{Provider: "gemini", APIKey: "AIza-platform", Model: "gemini-embedding-2", Dimensions: 1536}
	r := docindex.NewResolver(docindex.ResolverConfig{Platform: plat})
	ch := r(context.Background(), org)
	if ch.Embedder == nil || ch.Embedder.Provider() != "gemini" || ch.Embedder.Model() != "gemini-embedding-2@1536" {
		t.Fatalf("platform gemini: %+v", ch)
	}
	if ch := docindex.NewResolver(docindex.ResolverConfig{Platform: plat, Allowed: func(_ context.Context, _, p string) bool { return p != "gemini" }})(context.Background(), org); ch.Embedder != nil || ch.Reason != docindex.ReasonEmbedPolicy {
		t.Fatalf("policy forbids gemini: %+v", ch)
	}
	// an organization on its own Gemini key embeds with it (never the platform's)
	byo := func(context.Context, string) (string, string, string, string, string, bool) {
		return "gemini", "AIza-org", "gemini-3.6-flash", "", "", true
	}
	ch = docindex.NewResolver(docindex.ResolverConfig{OrgCredential: byo})(context.Background(), org)
	g, ok := ch.Embedder.(*docindex.GeminiEmbedder)
	if !ok || g.Key != "AIza-org" || g.Model() != "gemini-embedding-2@1536" {
		t.Fatalf("byo gemini: %+v", ch)
	}
	ch = docindex.NewResolver(docindex.ResolverConfig{Platform: plat, OrgCredential: byo})(context.Background(), org)
	if g, ok := ch.Embedder.(*docindex.GeminiEmbedder); !ok || g.Key != "AIza-org" {
		t.Fatalf("byo gemini must use its own key: %+v", ch)
	}
	// a BYO OpenAI org next to a Gemini platform gets the OpenAI default model, not gemini-embedding-2
	ch = docindex.NewResolver(docindex.ResolverConfig{Platform: plat, OrgCredential: func(context.Context, string) (string, string, string, string, string, bool) {
		return "openai", "sk", "gpt", "", "", true
	}})(context.Background(), org)
	if ch.Embedder == nil || ch.Embedder.Provider() != "openai" || ch.Embedder.Model() != docindex.DefaultEmbedModel {
		t.Fatalf("byo openai beside gemini: %+v", ch)
	}
}

type spendRec struct {
	mu      sync.Mutex
	allowed []float64
	booked  []float64
	models  []string
}

func (s *spendRec) Allow(_ context.Context, _ string, usd float64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.allowed = append(s.allowed, usd)
	return true
}
func (s *spendRec) Record(_ context.Context, _ string, _ int, usd float64, model string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.booked, s.models = append(s.booked, usd), append(s.models, model)
}

func geminiEnv(t *testing.T, lim docindex.Limits, srvURL string) (*env, *spendRec) {
	t.Helper()
	e := &env{ctx: application.WithOrg(context.Background(), org)}
	var idx *docindex.Service
	e.drive = drive.New(drive.Config{Store: drive.NewMemStore(), Objects: objstore.NewMemory(), OrgID: org,
		Audit: func(_ context.Context, o string, a drive.AuditEntry) {
			if idx != nil {
				idx.OnDriveAudit(o, a.Action, a.Entity, a.EntityID)
			}
		}})
	e.store = docindex.NewMemStore(e.drive)
	idx = docindex.New(docindex.Config{Store: e.store, Drive: e.drive, OrgID: org, Limits: lim})
	sp := &spendRec{}
	idx.Bind(docindex.Binding{Spend: sp, Embeddings: docindex.NewResolver(docindex.ResolverConfig{
		Platform: docindex.EmbedConfig{Provider: "gemini", APIKey: "AIza-k", Model: "gemini-embedding-2", Dimensions: 128, BaseURL: srvURL}})})
	e.idx = idx
	return e, sp
}

func TestGeminiIndexSearchBudgetAndQueryForm(t *testing.T) {
	srv, calls := fakeGemini(t, 128, true)
	e, sp := geminiEnv(t, docindex.Limits{EmbedPricePerM: -1}, srv.URL)
	e.write(t, "/Shared/report.txt", report())
	e.idx.Sync(e.ctx, org)
	st, _ := e.idx.Status(e.ctx, "/Shared")
	if st.Chunks == 0 || st.Embedded != st.Chunks {
		t.Fatalf("gemini must embed every chunk: %+v", st)
	}
	r := e.search(t, "automobile", "semantic", nil)
	if r.Mode != drive.SearchSemantic || len(r.Hits) == 0 || r.Hits[0].Page != 23 {
		t.Fatalf("semantic with gemini: %+v", r)
	}
	cs := calls()
	last := cs[len(cs)-1].reqs[0].Content.Parts[0].Text
	if last != "task: search result | query: automobile" {
		t.Fatalf("the search query must use the query form: %q", last)
	}
	if !strings.HasPrefix(cs[0].reqs[0].Content.Parts[0].Text, "title: none | text: ") {
		t.Fatalf("chunks must use the document form: %q", cs[0].reqs[0].Content.Parts[0].Text)
	}
	// budget reservation before every call and ledger after, at the Gemini default price, model@dims
	if len(sp.allowed) < 2 || len(sp.booked) < 2 || sp.models[0] != "gemini-embedding-2@128" {
		t.Fatalf("spend: %+v", sp)
	}
	used, _ := e.store.EmbedUsage(e.ctx, org, time.Now())
	if want := float64(used) * 0.20 / 1e6; math.Abs(sumF(sp.booked)-want) > 1e-12 {
		t.Fatalf("booked %.10f, want %.10f at 0.20/M", sumF(sp.booked), want)
	}
	// EmbedQueryTexts (memory) uses the query form too
	if _, m, reason := e.idx.EmbedQueryTexts(e.ctx, org, []string{"vehicle"}); reason != "" || m != "gemini-embedding-2@128" {
		t.Fatalf("query texts: %s %s", m, reason)
	}
	cs = calls()
	if got := cs[len(cs)-1].reqs[0].Content.Parts[0].Text; got != "task: search result | query: vehicle" {
		t.Fatalf("memory query form: %q", got)
	}
}

func TestGeminiRespectsDailyCap(t *testing.T) {
	srv, calls := fakeGemini(t, 128, true)
	e, _ := geminiEnv(t, docindex.Limits{DailyEmbedTokens: 5}, srv.URL)
	e.write(t, "/Shared/note.md", "A long note about the picnic and the car we rented for the trip to the coast.")
	e.idx.Sync(e.ctx, org)
	st, _ := e.idx.Status(e.ctx, "/Shared")
	if st.Indexed != 1 || st.Embedded != 0 || len(calls()) != 0 {
		t.Fatalf("over the daily cap nothing may reach Gemini: %+v calls %d", st, len(calls()))
	}
	if _, _, reason := e.idx.EmbedTexts(e.ctx, org, []string{strings.Repeat("word ", 50)}); reason != docindex.ReasonEmbedCap || len(calls()) != 0 {
		t.Fatalf("EmbedTexts over cap: %q calls %d", reason, len(calls()))
	}
	r := e.search(t, "picnic", "", nil)
	if r.Mode != drive.SearchKeyword || len(r.Hits) != 1 {
		t.Fatalf("degrade to keyword: %+v", r)
	}
}

func sumF(v []float64) float64 {
	s := 0.0
	for _, x := range v {
		s += x
	}
	return s
}

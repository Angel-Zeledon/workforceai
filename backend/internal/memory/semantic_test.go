package memory_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"aiworkforce/backend/internal/docindex"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/memory"
	"aiworkforce/backend/internal/sanitize"
)

// fakeEmbedder maps concepts to axes: billing words on axis 0, car words on
// axis 1, a small constant on axis 2 so no vector is zero. It can be told to
// refuse (daily cap, provider off or failing) like docindex.Service does.
type fakeEmbedder struct {
	mu     sync.Mutex
	model  string
	reason string // "" = embed; otherwise the refusal
	calls  int
	texts  []string
}

func (f *fakeEmbedder) set(reason string) { f.mu.Lock(); f.reason = reason; f.mu.Unlock() }

func (f *fakeEmbedder) EmbedModel(context.Context, string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.reason == docindex.ReasonEmbedOff {
		return ""
	}
	return f.model
}

func (f *fakeEmbedder) EmbedTexts(_ context.Context, _ string, texts []string) ([][]float32, string, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.reason != "" {
		return nil, f.model, f.reason
	}
	f.calls++
	f.texts = append(f.texts, texts...)
	out := make([][]float32, len(texts))
	for i, t := range texts {
		v := []float32{0, 0, 0.1}
		low := strings.ToLower(t)
		for _, w := range []string{"factura", "cobro", "invoice", "pago"} {
			if strings.Contains(low, w) {
				v[0] = 1
			}
		}
		for _, w := range []string{"auto", "carro", "vehiculo"} {
			if strings.Contains(low, w) {
				v[1] = 1
			}
		}
		out[i] = v
	}
	return out, f.model, ""
}

func (f *fakeEmbedder) callCount() int { f.mu.Lock(); defer f.mu.Unlock(); return f.calls }

func semanticSvc(store *memory.MemStore, emb *fakeEmbedder, sus *sanitize.Suspects) *memory.Service {
	sem := &memory.Semantic{Docs: store, Vecs: store, Embedder: emb, Suspects: sus}
	return memory.New(memory.Config{Docs: store, Embedder: emb, Suspects: sus,
		Retriever: &memory.Multi{Retrievers: []memory.Retriever{&memory.FTS{Docs: store}, sem}}})
}

func task(id, agent, title, summary string) domain.Task {
	fin := time.Now().UTC()
	return domain.Task{ID: id, RequestID: "req_" + id, AgentID: agent, Title: title, Status: domain.TaskDone, FinishedAt: &fin,
		Output: &domain.StructuredOutput{Summary: summary}}
}

func refs(items []memory.Item) string {
	var r []string
	for _, it := range items {
		r = append(r, it.Ref)
	}
	return strings.Join(r, ",")
}

func TestSemanticFindsByMeaningAndFusesWithKeyword(t *testing.T) {
	ctx, store, emb := context.Background(), memory.NewMemStore(), &fakeEmbedder{model: "fake"}
	svc := semanticSvc(store, emb, nil)
	svc.RememberTask(ctx, "org-a", task("1", "fin", "Factura de proveedor", "Revisión de la factura mensual"), "")
	svc.RememberTask(ctx, "org-a", task("2", "fin", "Cobro pendiente", "Seguimiento de clientes morosos"), "")
	svc.RememberTask(ctx, "org-a", task("3", "ops", "Auto nuevo", "Compra de un carro para reparto"), "")

	// "factura" matches task 1 by keyword AND by meaning; task 2 only by meaning.
	got, err := svc.Search(ctx, "org-a", "fin", "factura", 6)
	if err != nil || refs(got) != "task:1,task:2" {
		t.Fatalf("both legs agree on task 1, meaning adds task 2, the car is noise: %v %q", err, refs(got))
	}
	// An English word shares no term with any text: only the semantic leg answers.
	got, _ = svc.Search(ctx, "org-a", "fin", "invoice", 6)
	if refs(got) != "task:1,task:2" && refs(got) != "task:2,task:1" {
		t.Fatalf("found by meaning alone: %q", refs(got))
	}
}

func TestMultiRankFusionOrder(t *testing.T) {
	lex := fixed{{Ref: "a"}, {Ref: "b"}, {Ref: "c"}}
	sem := fixed{{Ref: "c"}, {Ref: "b"}, {Ref: "d"}}
	got, err := (&memory.Multi{Retrievers: []memory.Retriever{lex, sem}}).Retrieve(context.Background(), "o", memory.Query{})
	if err != nil || refs(got) != "c,b,a,d" {
		t.Fatalf("sum of 1/(60+rank): c (1/63+1/61) edges b (2/62), then a (1/61) and d (1/63): %v %q", err, refs(got))
	}
}

func TestSemanticFallsBackToKeywordOnly(t *testing.T) {
	ctx := context.Background()
	for name, reason := range map[string]string{"off": docindex.ReasonEmbedOff, "cap": docindex.ReasonEmbedCap, "budget": docindex.ReasonEmbedBudget, "failure": docindex.ReasonEmbedFailure} {
		t.Run(name, func(t *testing.T) {
			store, emb := memory.NewMemStore(), &fakeEmbedder{model: "fake", reason: reason}
			svc := semanticSvc(store, emb, nil)
			svc.RememberTask(ctx, "org-a", task("1", "fin", "Factura de proveedor", "Revisión mensual"), "")
			svc.RememberTask(ctx, "org-a", task("2", "fin", "Cobro pendiente", "Clientes morosos"), "")
			if emb.callCount() != 0 {
				t.Fatalf("a refused embedder embeds nothing")
			}
			if pend, _ := store.Unembedded(ctx, "org-a", "fake", 10); len(pend) != 2 {
				t.Fatalf("both documents are stored without a vector: %d", len(pend))
			}
			got, err := svc.Search(ctx, "org-a", "fin", "factura", 6)
			if err != nil || refs(got) != "task:1" {
				t.Fatalf("keyword search keeps working and finds no meaning-only hit: %v %q", err, refs(got))
			}
			if _, err := svc.Search(ctx, "org-a", "fin", "invoice", 6); err != nil {
				t.Fatalf("no error without a semantic leg: %v", err)
			}
		})
	}
	// no embedder wired at all
	store := memory.NewMemStore()
	svc := memory.New(memory.Config{Docs: store, Retriever: &memory.Multi{Retrievers: []memory.Retriever{&memory.FTS{Docs: store}, &memory.Semantic{Docs: store, Vecs: store}}}})
	svc.RememberTask(ctx, "org-a", task("1", "fin", "Factura", "mensual"), "")
	if got, _ := svc.Search(ctx, "org-a", "fin", "factura", 6); refs(got) != "task:1" {
		t.Fatalf("nil embedder: %q", refs(got))
	}
}

func TestBackfillEmbedsWhenTheCapResets(t *testing.T) {
	ctx, store, emb := context.Background(), memory.NewMemStore(), &fakeEmbedder{model: "fake", reason: docindex.ReasonEmbedCap}
	svc := semanticSvc(store, emb, nil)
	svc.RememberTask(ctx, "org-a", task("1", "fin", "Cobro pendiente", "Clientes morosos"), "")
	if got, _ := svc.Search(ctx, "org-a", "fin", "invoice", 6); len(got) != 0 {
		t.Fatalf("cap hit: nothing by meaning yet: %q", refs(got))
	}
	if n := svc.EmbedPending(ctx, "org-a", 10); n != 0 {
		t.Fatalf("still capped: %d", n)
	}
	emb.set("") // the next day
	if n := svc.EmbedPending(ctx, "org-a", 10); n != 1 {
		t.Fatalf("backfill embeds the pending document: %d", n)
	}
	if got, _ := svc.Search(ctx, "org-a", "fin", "invoice", 6); refs(got) != "task:1" {
		t.Fatalf("found by meaning after the backfill: %q", refs(got))
	}
	if n := svc.EmbedPending(ctx, "org-a", 10); n != 0 {
		t.Fatalf("idempotent: %d", n)
	}
}

func TestUnchangedTextKeepsItsVectorAndChangedTextLosesIt(t *testing.T) {
	ctx, store, emb := context.Background(), memory.NewMemStore(), &fakeEmbedder{model: "fake"}
	svc := semanticSvc(store, emb, nil)
	tk := task("1", "fin", "Cobro pendiente", "Clientes morosos")
	svc.RememberTask(ctx, "org-a", tk, "")
	svc.RememberTask(ctx, "org-a", tk, "") // re-index (backfill on every start)
	if emb.callCount() != 1 {
		t.Fatalf("an unchanged document is not embedded again: %d calls", emb.callCount())
	}
	emb.set(docindex.ReasonEmbedCap)
	tk.Output.Summary = "Clientes morosos del segundo trimestre"
	svc.RememberTask(ctx, "org-a", tk, "")
	if pend, _ := store.Unembedded(ctx, "org-a", "fake", 10); len(pend) != 1 {
		t.Fatalf("a changed text never keeps a stale vector: %d", len(pend))
	}
}

func TestModelChangeMakesDocumentsPending(t *testing.T) {
	ctx, store, emb := context.Background(), memory.NewMemStore(), &fakeEmbedder{model: "v1"}
	svc := semanticSvc(store, emb, nil)
	svc.RememberTask(ctx, "org-a", task("1", "fin", "Cobro pendiente", "Clientes"), "")
	emb.model = "v2"
	if got, _ := svc.Search(ctx, "org-a", "fin", "invoice", 6); len(got) != 0 {
		t.Fatalf("vectors of another model are never compared: %q", refs(got))
	}
	if n := svc.EmbedPending(ctx, "org-a", 10); n != 1 {
		t.Fatalf("re-embedded with the new model: %d", n)
	}
	if got, _ := svc.Search(ctx, "org-a", "fin", "invoice", 6); refs(got) != "task:1" {
		t.Fatalf("%q", refs(got))
	}
}

func TestSemanticRespectsOrganizationAndNotePrivacy(t *testing.T) {
	ctx, store, emb := context.Background(), memory.NewMemStore(), &fakeEmbedder{model: "fake"}
	svc := semanticSvc(store, emb, nil)
	svc.RememberTask(ctx, "org-a", task("1", "fin", "Cobro pendiente", "Clientes morosos"), "")
	if err := svc.RememberNote(ctx, "org-a", "fin", "El pago de la factura Acme vence el día 5 de cada mes"); err != nil {
		t.Fatal(err)
	}
	if got, _ := svc.Search(ctx, "org-b", "fin", "invoice", 6); len(got) != 0 {
		t.Fatalf("another organization sees nothing: %q", refs(got))
	}
	got, _ := svc.Search(ctx, "org-a", "ops", "invoice", 6)
	if refs(got) != "task:1" {
		t.Fatalf("another agent sees the organization knowledge but not the private note: %q", refs(got))
	}
	got, _ = svc.Search(ctx, "org-a", "fin", "invoice", 6)
	if len(got) != 2 {
		t.Fatalf("the owner sees both: %q", refs(got))
	}
	// a person deleted the note: it never comes back by meaning
	var noteID string
	for _, it := range got {
		if it.Kind == memory.KindNote {
			noteID = it.ID
		}
	}
	if err := svc.Delete(ctx, "org-a", "fin", noteID, "ana"); err != nil {
		t.Fatal(err)
	}
	if got, _ = svc.Search(ctx, "org-a", "fin", "invoice", 6); refs(got) != "task:1" {
		t.Fatalf("suppressed notes are not retrieved by meaning: %q", refs(got))
	}
}

func TestEmbeddingsNeverCarrySecrets(t *testing.T) {
	ctx, store, emb := context.Background(), memory.NewMemStore(), &fakeEmbedder{model: "fake"}
	sus := sanitize.NewSuspects()
	sus.Add("hunter2-super-secret")
	svc := semanticSvc(store, emb, sus)
	svc.RememberTask(ctx, "org-a", task("1", "fin", "Cobro pendiente", "La clave es hunter2-super-secret y la llave AKIAIOSFODNN7EXAMPLE"), "")
	_, _ = svc.Search(ctx, "org-a", "fin", "factura con hunter2-super-secret", 6)
	for _, txt := range emb.texts {
		if strings.Contains(txt, "hunter2-super-secret") || strings.Contains(txt, "AKIAIOSFODNN7EXAMPLE") {
			t.Fatalf("a secret reached the embeddings provider: %q", txt)
		}
	}
	if len(emb.texts) < 2 {
		t.Fatalf("document and query were embedded: %d", len(emb.texts))
	}
}

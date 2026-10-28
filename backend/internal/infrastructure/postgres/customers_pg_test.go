package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"aiworkforce/backend/internal/customers"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/memory"
)

// Integration test of migration 797 (see store_rls_test.go: TEST_DATABASE_URL): customers, clones and links with
// Row-Level Security, and the customer isolation of the memory documents in SQL.

func TestCustomerStorePostgres(t *testing.T) {
	env := testStore(t)
	ctx := context.Background()
	seedOrg(t, env.Store, "org-a")
	seedOrg(t, env.Store, "org-b")
	s := &CustomerStore{S: env.Store}
	now := time.Now().UTC().Truncate(time.Millisecond)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	a := customers.Customer{ID: "cus_a", OrgID: "org-a", Name: "Ferretería López", Color: "#2563eb", CreatedAt: now, UpdatedAt: now}
	must(s.CreateCustomer(ctx, a))
	must(s.CreateCustomer(ctx, customers.Customer{ID: "cus_b", OrgID: "org-a", Name: "Panadería", CreatedAt: now, UpdatedAt: now}))
	if err := s.CreateCustomer(ctx, customers.Customer{ID: "cus_dup", OrgID: "org-a", Name: "ferretería lópez", CreatedAt: now, UpdatedAt: now}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("active names are unique per org (case-insensitive): %v", err)
	}
	must(s.CreateCustomer(ctx, customers.Customer{ID: "cus_a", OrgID: "org-b", Name: "Ferretería López", CreatedAt: now, UpdatedAt: now}))
	if err := s.CreateCustomer(ctx, customers.Customer{ID: "cus_c", OrgID: "org-a", Name: "X", Color: "red", CreatedAt: now, UpdatedAt: now}); err == nil {
		t.Fatal("the color check must hold in SQL")
	}

	got, err := s.GetCustomer(ctx, "org-a", "cus_a")
	if err != nil || got.Name != a.Name || got.Archived {
		t.Fatalf("get: %+v %v", got, err)
	}
	got.Archived, got.ArchivedAt = true, &now
	must(s.UpdateCustomer(ctx, got))
	if l, _ := s.ListCustomers(ctx, "org-a", false); len(l) != 1 || l[0].ID != "cus_b" {
		t.Fatalf("archived hidden: %+v", l)
	}
	if l, _ := s.ListCustomers(ctx, "org-a", true); len(l) != 2 {
		t.Fatalf("include archived: %+v", l)
	}
	// An archived name can be reused by a new active customer.
	must(s.CreateCustomer(ctx, customers.Customer{ID: "cus_a2", OrgID: "org-a", Name: "Ferretería López", CreatedAt: now, UpdatedAt: now}))

	must(s.AddAssignment(ctx, "org-a", customers.Assignment{ID: "cla_1", CustomerID: "cus_b", AgentID: "finance", DisplayName: "Contador · Panadería", CreatedAt: now}))
	if err := s.AddAssignment(ctx, "org-a", customers.Assignment{ID: "cla_2", CustomerID: "cus_b", AgentID: "finance", DisplayName: "x", CreatedAt: now}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("one clone per agent and customer: %v", err)
	}
	if as, _ := s.Assignments(ctx, "org-a", ""); len(as) != 1 || as[0].DisplayName != "Contador · Panadería" {
		t.Fatalf("assignments: %+v", as)
	}
	must(s.AddLink(ctx, "org-a", customers.Link{Kind: customers.LinkRequest, RefID: "rq-1", CustomerID: "cus_b", CreatedAt: now}))
	if err := s.AddLink(ctx, "org-a", customers.Link{Kind: customers.LinkRequest, RefID: "rq-1", CustomerID: "cus_a2", CreatedAt: now}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("one customer per request: %v", err)
	}
	if l, err := s.LinkOf(ctx, "org-a", customers.LinkRequest, "rq-1"); err != nil || l.CustomerID != "cus_b" {
		t.Fatalf("link of: %+v %v", l, err)
	}
	if _, err := s.LinkOf(ctx, "org-b", customers.LinkRequest, "rq-1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("links are per organization: %v", err)
	}
	if err := s.AddLink(ctx, "org-a", customers.Link{Kind: customers.LinkRequest, RefID: "rq-2", CustomerID: "cus_nope", CreatedAt: now}); err == nil {
		t.Fatal("a link needs an existing customer (FK)")
	}

	// RLS: org B cannot see org A's rows even without a WHERE.
	var n int
	if err := env.WithOrgTx(ctx, "org-b", func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM customers) + (SELECT count(*) FROM customer_assignments) + (SELECT count(*) FROM customer_links)`).Scan(&n)
	}); err != nil || n != 1 {
		t.Fatalf("RLS: org B sees only its own customer: %d %v", n, err)
	}
	if err := env.WithOrgTx(ctx, "org-b", func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO customer_links (org_id, kind, ref_id, customer_id) VALUES ('org-a','request','rq-x','cus_b')`)
		return err
	}); err == nil {
		t.Fatal("RLS WITH CHECK must refuse writing a row of another organization")
	}
}

func TestMemoryCustomerIsolationPostgres(t *testing.T) {
	env := testStore(t)
	ctx := context.Background()
	seedOrg(t, env.Store, "org-a")
	m := &MemoryStore{S: env.Store}
	now := time.Now().UTC()
	vec := []float32{1, 0, 0}
	for _, d := range []memory.Doc{
		{ID: "task_a", Kind: memory.KindTask, Ref: "task:a", Title: "saldo bancos ferreteria", Body: "cuadra", Scope: memory.ScopeOrg, AgentID: "finance", CustomerID: "cus_a", Vec: vec, EmbedModel: "m"},
		{ID: "task_b", Kind: memory.KindTask, Ref: "task:b", Title: "saldo bancos panaderia", Body: "cuadra", Scope: memory.ScopeOrg, AgentID: "finance", CustomerID: "cus_b", Vec: vec, EmbedModel: "m"},
		{ID: "task_g", Kind: memory.KindTask, Ref: "task:g", Title: "saldo bancos general", Body: "cuadra", Scope: memory.ScopeOrg, AgentID: "finance", Vec: vec, EmbedModel: "m"},
		{ID: memory.UserNotePrefix + "finance_1", Kind: memory.KindNote, Ref: "memory:" + memory.UserNotePrefix + "finance_1", Title: "saldo bancos cada mes", Body: "saldo bancos", Scope: memory.ScopeAgent, AgentID: "finance", Vec: vec, EmbedModel: "m"},
		{ID: "note_t_1", Kind: memory.KindNote, Ref: "memory:t.1", Title: "saldo bancos nota de agente", Body: "saldo bancos", Scope: memory.ScopeAgent, AgentID: "finance", Vec: vec, EmbedModel: "m"},
	} {
		d.CreatedAt, d.UpdatedAt = now, now
		if err := m.Upsert(ctx, "org-a", d); err != nil {
			t.Fatal(err)
		}
	}
	refs := func(hits []memory.DocHit) map[string]bool {
		out := map[string]bool{}
		for _, h := range hits {
			out[h.Doc.Ref] = true
		}
		return out
	}
	scoped, err := m.Search(ctx, "org-a", memory.SearchParams{Terms: []string{"saldo", "bancos"}, AgentID: "finance", CustomerID: "cus_a"})
	if err != nil {
		t.Fatal(err)
	}
	r := refs(scoped)
	if !r["task:a"] || r["task:b"] || r["task:g"] || r["memory:t.1"] || !r["memory:"+memory.UserNotePrefix+"finance_1"] || len(r) != 2 {
		t.Fatalf("customer A sees its work and the person's note only: %v", r)
	}
	if all, _ := m.Search(ctx, "org-a", memory.SearchParams{Terms: []string{"saldo", "bancos"}, AgentID: "finance"}); len(all) != 5 {
		t.Fatalf("unscoped search keeps the old behaviour: %d", len(all))
	}
	cands, err := m.Candidates(ctx, "org-a", memory.VecQuery{AgentID: "finance", Model: "m", CustomerID: "cus_b"})
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, c := range cands {
		ids[c.ID] = true
	}
	if !ids["task_b"] || ids["task_a"] || ids["task_g"] || len(ids) != 2 {
		t.Fatalf("semantic candidates of B: %v", ids)
	}
	got, err := m.Get(ctx, "org-a", "task_a")
	if err != nil || got.CustomerID != "cus_a" {
		t.Fatalf("customer_id round-trips: %+v %v", got, err)
	}
	// B12 history: an explicit retag moves only the documents tagged `from`, and back.
	if n, err := m.RetagCustomer(ctx, "org-a", memory.RetagSelector{DocID: "task_g"}, "", "cus_a"); err != nil || n != 1 {
		t.Fatalf("retag: %d %v", n, err)
	}
	if n, _ := m.RetagCustomer(ctx, "org-a", memory.RetagSelector{DocID: "task_b"}, "", "cus_a"); n != 0 {
		t.Fatal("a document of another customer must not move")
	}
	if g, _ := m.Get(ctx, "org-a", "task_g"); g.CustomerID != "cus_a" {
		t.Fatalf("retagged: %+v", g)
	}
	if n, err := m.RetagCustomer(ctx, "org-a", memory.RetagSelector{DocID: "task_g"}, "cus_a", ""); err != nil || n != 1 {
		t.Fatalf("undo: %d %v", n, err)
	}
}

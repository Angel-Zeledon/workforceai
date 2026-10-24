package api_test

import (
	"context"
	"strings"
	"testing"

	"aiworkforce/backend/internal/domain"
)

// B12: requests born in the chat can be scoped to a customer, by the chat's customer picker (POST /messages
// with customer_id) or by the /cliente command. The scope is stored before the request exists.

func (e *cmdEnv) newCustomer(name string) string {
	e.t.Helper()
	out := e.json("POST", api1+"/customers", e.owner, map[string]any{"name": name}, 201)
	id, _ := out["id"].(string)
	if id == "" {
		e.t.Fatalf("customer %s: %v", name, out)
	}
	return id
}

func (e *cmdEnv) customerOfRequest(id string) string {
	e.t.Helper()
	cid, err := e.cust.CustomerOfRequest(context.Background(), e.org, id)
	if err != nil {
		e.t.Fatal(err)
	}
	return cid
}

func TestCustomerCommandScopesTheRequest(t *testing.T) {
	e := newCmdEnv(t, cmdOpts{customers: true})
	lopez := e.newCustomer("Ferretería López")
	e.newCustomer("Ferretería Norte")
	san := e.newCustomer("Panadería San José")

	for _, c := range []struct{ raw, want string }{
		{"/cliente Ferretería López: concilia los bancos de octubre", lopez},
		{"/cliente ferreteria lopez concilia los bancos", lopez}, // no colon: the longest name the words start with
		{"/customer panaderia: reconcile the bank accounts", san},  // a unique prefix
		{"/client " + san + ": reconcile", san},                     // the id
	} {
		out := e.ok(e.member, map[string]any{"raw": c.raw, "context": officeCtx})
		r := resultOf(out)
		id, _ := r["request_id"].(string)
		if out["status"] != "started" || id == "" {
			t.Fatalf("%s: %v", c.raw, out)
		}
		if got := e.customerOfRequest(id); got != c.want {
			t.Fatalf("%s: the request must be scoped to %s, got %q", c.raw, c.want, got)
		}
		req, err := e.store.GetRequest(context.Background(), e.org, id)
		if err != nil || strings.Contains(strings.ToLower(req.Text), "/cliente") || strings.Contains(req.Text, "López:") {
			t.Fatalf("%s: the request text is the task only: %q %v", c.raw, req.Text, err)
		}
	}
	e.fails(e.member, map[string]any{"raw": "/cliente Ferretería: algo", "context": officeCtx}, 400, "ambiguous_customer")
	e.fails(e.member, map[string]any{"raw": "/cliente Desconocido: algo", "context": officeCtx}, 400, "unknown_customer")
	if n := len(e.auditOf("customer.request.scoped")); n != 4 {
		t.Fatalf("every scoped request is audited: %d", n)
	}
}

// POST /messages carries customer_id to the orchestrator (the turn itself, and the request it creates, are tested in
// internal/application customer_test.go): an unknown or archived customer is refused before the message is stored.
func TestChatMessageCarriesTheCustomer(t *testing.T) {
	e := newCmdEnv(t, cmdOpts{customers: true})
	lopez := e.newCustomer("Ferretería López")
	before := len(e.messagesOf(e.member, domain.ChatOffice))
	e.json("POST", api1+"/messages", e.member, map[string]any{"conversation": "office", "text": "concilia los bancos", "customer_id": "cus_nope"}, 404)
	if after := len(e.messagesOf(e.member, domain.ChatOffice)); after != before {
		t.Fatalf("a refused message must not be stored: %d -> %d", before, after)
	}
	e.json("POST", api1+"/messages", e.member, map[string]any{"conversation": "office", "text": "hola", "customer_id": lopez}, 202)
	e.json("DELETE", api1+"/customers/"+lopez, e.owner, nil, 204)
	e.json("POST", api1+"/messages", e.member, map[string]any{"conversation": "office", "text": "hola", "customer_id": lopez}, 409)
}

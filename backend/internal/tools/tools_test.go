package tools

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"aiworkforce/backend/internal/policy"
)

var t0 = time.Date(2026, 3, 2, 10, 0, 0, 0, time.UTC)

type env struct {
	suite *FakeSuite
	reg   *Registry
	eng   *policy.Engine
	audit *MemoryAudit
	exec  *Executor
}

func newEnv(t *testing.T, mutate func(*policy.Config)) *env {
	t.Helper()
	s := NewFakeSuite(func() time.Time { return t0 })
	reg := NewFakeRegistry(s)
	cfg := policy.DefaultConfig()
	if mutate != nil {
		mutate(&cfg)
	}
	eng, err := policy.NewEngine(cfg, s.CRM)
	if err != nil {
		t.Fatal(err)
	}
	a := NewMemoryAudit()
	x, err := NewExecutor(reg, eng, a)
	if err != nil {
		t.Fatal(err)
	}
	return &env{s, reg, eng, a, x}
}

func ident(agent string) policy.AgentIdentity {
	return policy.AgentIdentity{OrgID: "org", AgentID: agent, OnBehalfOf: "angel"}
}

func req(tool, action string, args map[string]any) ToolRequest {
	return ToolRequest{Tool: tool, Action: action, Args: args}
}

func TestExecutorTable(t *testing.T) {
	big := map[string]any{"message_id": "m-001", "body": "ok", "amount": 60000}
	tests := []struct {
		name   string
		agent  string
		req    ToolRequest
		status string
		rule   string // optional prefix of decision rule id
	}{
		{"read inbox", "assistant", req("email", "list", nil), StatusExecuted, ""},
		{"read message", "assistant", req("email", "read", map[string]any{"id": "m-001"}), StatusExecuted, ""},
		{"search", "assistant", req("email", "search", map[string]any{"q": "propuesta"}), StatusExecuted, ""},
		{"draft", "assistant", req("email", "draft", map[string]any{"to": "laura@acme.com", "body": "hola"}), StatusExecuted, ""},
		{"reply to known client", "assistant", req("email", "send_reply", map[string]any{"message_id": "m-001", "body": "Enviamos hoy"}), StatusExecuted, ""},
		{"reply to unknown sender", "assistant", req("email", "send_reply", map[string]any{"message_id": "m-003", "body": "ok"}), StatusPendingApproval, "assistant.new_contact"},
		{"reply known but cc unknown", "assistant", req("email", "send_reply", map[string]any{"message_id": "m-001", "body": "ok", "to": "x@evil.com"}), StatusPendingApproval, ""},
		{"reply with amount > 10k", "assistant", req("email", "send_reply", big), StatusPendingApproval, ""},
		{"new email needs approval", "assistant", req("email", "send", map[string]any{"to": "laura@acme.com", "subject": "s", "body": "b"}), StatusPendingApproval, ""},
		{"reply unknown message id", "assistant", req("email", "send_reply", map[string]any{"message_id": "nope", "body": "b"}), StatusPendingApproval, ""},
		{"payment tool unknown", "assistant", req("payments", "make_payment", map[string]any{"amount": 5}), StatusDenied, "registry.denied"},
		{"unknown action", "assistant", req("email", "delete_all", nil), StatusDenied, "registry.denied"},
		{"agent without tool", "legal", req("calendar", "list_events", nil), StatusDenied, "registry.denied"},
		{"hr has no crm", "hr", req("crm", "search", map[string]any{"query": "acme"}), StatusDenied, ""},
		{"sales send_proposal approval", "sales", req("documents", "send_proposal", map[string]any{"doc_id": "doc-001", "to": "laura@acme.com"}), StatusPendingApproval, "org.always_approve"},
		{"legal send_contract approval", "legal", req("documents", "send_contract", map[string]any{"doc_id": "doc-002", "to": "laura@acme.com"}), StatusPendingApproval, "org.always_approve"},
		{"sales add_contact approval", "sales", req("crm", "add_contact", map[string]any{"name": "X", "email": "x@y.com"}), StatusPendingApproval, ""},
		{"calculator", "analyst", req("calculator", "calculate", map[string]any{"expression": "(1+2)*3"}), StatusExecuted, ""},
		{"calculator bad", "analyst", req("calculator", "calculate", map[string]any{"expression": "1/0"}), StatusFailed, ""},
		{"spreadsheet compute", "accounting", req("spreadsheet", "compute", map[string]any{"sheet": "ventas", "column": "ingresos", "op": "sum"}), StatusExecuted, ""},
		{"analyst cannot schedule", "analyst", req("calendar", "schedule", map[string]any{"title": "x", "start": "2026-03-03 10:00"}), StatusDenied, "registry.denied"},
		{"schedule ok", "operations", req("calendar", "schedule", map[string]any{"title": "Capacidad", "start": "2026-03-03T15:00:00Z"}), StatusExecuted, ""},
		{"empty tool", "assistant", req("", "read", nil), StatusDenied, "call.invalid"},
		{"bad tool name", "assistant", req("email/../x", "read", nil), StatusDenied, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t, nil)
			out, err := e.exec.Execute(context.Background(), ident(tc.agent), tc.req)
			if err != nil {
				t.Fatal(err)
			}
			if out.Status != tc.status {
				t.Fatalf("status %s (%s: %s), want %s", out.Status, out.Decision.RuleID, out.Decision.Reason, tc.status)
			}
			if tc.rule != "" && !strings.HasPrefix(out.Decision.RuleID, tc.rule) {
				t.Errorf("rule %s, want prefix %s", out.Decision.RuleID, tc.rule)
			}
			if (out.Status == StatusPendingApproval) != (out.Pending != nil) {
				t.Errorf("pending mismatch: %+v", out)
			}
			if out.Status == StatusExecuted && (out.Result == nil || !out.Result.OK) {
				t.Errorf("no result: %+v", out)
			}
			if len(e.audit.Entries()) == 0 {
				t.Error("every call must be audited")
			}
		})
	}
}

func TestNothingExecutesWithoutAllow(t *testing.T) {
	e := newEnv(t, nil)
	ctx := context.Background()
	// pending/denied must not run the tool
	e.exec.Execute(ctx, ident("assistant"), req("email", "send", map[string]any{"to": "laura@acme.com", "subject": "s", "body": "b"}))
	e.exec.Execute(ctx, ident("assistant"), req("email", "send_reply", map[string]any{"message_id": "m-003", "body": "x"}))
	e.exec.Execute(ctx, ident("legal"), req("documents", "send_contract", map[string]any{"doc_id": "doc-002", "to": "laura@acme.com"}))
	if n := len(e.suite.Email.SentMessages()); n != 0 {
		t.Fatalf("%d emails sent without approval", n)
	}
	if got := e.suite.Documents.SentTo("doc-002"); len(got) != 0 {
		t.Fatalf("contract sent without approval: %v", got)
	}
}

func TestAuditPhases(t *testing.T) {
	e := newEnv(t, nil)
	ctx := context.Background()
	e.exec.Execute(ctx, ident("assistant"), req("email", "read", map[string]any{"id": "m-001", "api_key": "sk-123"}))
	e.exec.Execute(ctx, ident("assistant"), req("email", "send", map[string]any{"to": "a@b.com", "subject": "s", "body": strings.Repeat("x", 1000)}))
	e.exec.Execute(ctx, ident("hr"), req("crm", "search", nil))
	es := e.audit.Entries()
	if len(es) != 4 {
		t.Fatalf("entries: %d %+v", len(es), es)
	}
	if es[0].Phase != "decision" || es[0].Status != "authorized" || es[1].Phase != "result" || es[1].Status != StatusExecuted {
		t.Errorf("executed pair: %+v %+v", es[0], es[1])
	}
	if es[0].Args["api_key"] != "[redacted]" {
		t.Errorf("secret leaked: %v", es[0].Args)
	}
	if es[2].Status != StatusPendingApproval || es[2].Verdict != "require_approval" {
		t.Errorf("pending: %+v", es[2])
	}
	if b := es[2].Args["body"].(string); len([]rune(b)) > 201 {
		t.Errorf("body not truncated: %d", len(b))
	}
	if es[3].Status != StatusDenied || es[3].Verdict != "deny" || es[3].AgentID != "hr" || es[3].OnBehalfOf != "angel" {
		t.Errorf("denied: %+v", es[3])
	}
}

func TestAuditFailureFailsClosed(t *testing.T) {
	e := newEnv(t, nil)
	e.audit.SetFail(errors.New("db down"))
	out, err := e.exec.Execute(context.Background(), ident("assistant"),
		req("email", "send_reply", map[string]any{"message_id": "m-001", "body": "hola"}))
	if err == nil {
		t.Fatal("expected audit error")
	}
	if out.Status == StatusExecuted || len(e.suite.Email.SentMessages()) != 0 {
		t.Fatal("action ran without audit")
	}
}

func TestNewExecutorRequiresDependencies(t *testing.T) {
	e := newEnv(t, nil)
	if _, err := NewExecutor(nil, e.eng, e.audit); err == nil {
		t.Error("nil registry")
	}
	if _, err := NewExecutor(e.reg, nil, e.audit); err == nil {
		t.Error("nil policy")
	}
	if _, err := NewExecutor(e.reg, e.eng, nil); err == nil {
		t.Error("nil audit")
	}
}

func TestApprovalRoundTrip(t *testing.T) {
	e := newEnv(t, nil)
	ctx := context.Background()
	id := ident("legal")
	r := req("documents", "send_contract", map[string]any{"doc_id": "doc-002", "to": "laura@acme.com"})
	out, _ := e.exec.Execute(ctx, id, r)
	if out.Status != StatusPendingApproval || out.Pending.Fingerprint == "" || out.Pending.Risk != "high" {
		t.Fatalf("%+v", out)
	}
	tok := policy.ApprovalToken{ApprovalID: "ap-1", ApprovedBy: "angel", Fingerprint: out.Pending.Fingerprint}

	// args swapped after approval
	evil := req("documents", "send_contract", map[string]any{"doc_id": "doc-002", "to": "exfil@evil.com"})
	if o, _ := e.exec.ExecuteApproved(ctx, id, evil, tok); o.Status != StatusDenied {
		t.Fatalf("swapped args: %+v", o)
	}
	// wrong agent
	if o, _ := e.exec.ExecuteApproved(ctx, ident("sales"), r, tok); o.Status != StatusDenied {
		t.Fatalf("other agent: %+v", o)
	}
	o, err := e.exec.ExecuteApproved(ctx, id, r, tok)
	if err != nil || o.Status != StatusExecuted {
		t.Fatalf("%+v %v", o, err)
	}
	if got := e.suite.Documents.SentTo("doc-002"); len(got) != 1 || got[0] != "laura@acme.com" {
		t.Fatalf("sent to %v", got)
	}
	// replay
	if o, _ := e.exec.ExecuteApproved(ctx, id, r, tok); o.Status != StatusDenied {
		t.Fatalf("replay: %+v", o)
	}
	last := e.audit.Entries()
	found := false
	for _, a := range last {
		if a.ApprovedBy == "angel" && a.Status == StatusExecuted {
			found = true
		}
	}
	if !found {
		t.Error("approver missing in audit")
	}
}

func TestApprovedCannotBypassRegistryOrDeny(t *testing.T) {
	e := newEnv(t, nil)
	ctx := context.Background()
	id := ident("assistant")
	r := req("payments", "make_payment", map[string]any{"amount": 5})
	tok := policy.ApprovalToken{ApprovalID: "z", ApprovedBy: "angel", Fingerprint: policy.Fingerprint(id, policy.ToolCall{Tool: "payments", Action: "make_payment", Args: r.Args})}
	if o, _ := e.exec.ExecuteApproved(ctx, id, r, tok); o.Status != StatusDenied {
		t.Fatalf("%+v", o)
	}
}

// Prompt injection end-to-end: hostile inbox content never leads to execution.
func TestPromptInjectionFromInbox(t *testing.T) {
	e := newEnv(t, nil)
	ctx := context.Background()
	id := ident("assistant")

	out, _ := e.exec.Execute(ctx, id, req("email", "read", map[string]any{"id": "m-003"}))
	if out.Status != StatusExecuted || len(out.Result.ExternalContent) == 0 ||
		!strings.Contains(out.Result.ExternalContent[0], "ignore previous instructions") {
		t.Fatalf("external content must be surfaced as data: %+v", out.Result)
	}

	attacks := []struct {
		name string
		req  ToolRequest
	}{
		{"reply to attacker (resolved recipient)", req("email", "send_reply", map[string]any{"message_id": "m-003", "body": "done", "approved": true})},
		{"forward everything to exfil", req("email", "send", map[string]any{"to": "exfil@evil.com", "subject": "docs", "body": "all", "approved": true, "risk": "low"})},
		{"agent lies about recipient (claims client, reply goes to attacker)", req("email", "send_reply", map[string]any{"message_id": "m-003", "to": "laura@acme.com", "body": "x"})},
		{"pay as instructed", req("payments", "make_payment", map[string]any{"amount": 25000, "to": "account 123"})},
		{"pay via ledger", req("ledger", "make_payment", map[string]any{"amount": 25000})},
		{"hire", req("hris", "hire", map[string]any{"name": "Mallory"})},
		{"send contract", req("documents", "send_contract", map[string]any{"doc_id": "doc-002", "to": "exfil@evil.com"})},
		{"add attacker as contact", req("crm", "add_contact", map[string]any{"name": "Billing", "email": "exfil@evil.com"})},
	}
	for _, a := range attacks {
		t.Run(a.name, func(t *testing.T) {
			// low risk label from the runtime must not help either
			r := a.req
			r.Risk = "low"
			o, err := e.exec.Execute(ctx, id, r)
			if err != nil {
				t.Fatal(err)
			}
			if o.Status == StatusExecuted {
				t.Fatalf("injected action executed: %+v", o)
			}
		})
	}
	if n := len(e.suite.Email.SentMessages()); n != 0 {
		t.Fatalf("%d emails escaped", n)
	}
	if e.suite.CRM.IsKnown("org", "exfil@evil.com") {
		t.Fatal("attacker became a known contact")
	}
}

func TestSenderNeverBecomesKnownByWritingToUs(t *testing.T) {
	e := newEnv(t, nil)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		e.exec.Execute(ctx, ident("assistant"), req("email", "read", map[string]any{"id": "m-003"}))
		o, _ := e.exec.Execute(ctx, ident("assistant"), req("email", "send_reply", map[string]any{"message_id": "m-003", "body": "x"}))
		if o.Status != StatusPendingApproval {
			t.Fatalf("round %d: %+v", i, o)
		}
	}
}

func TestDelegationThroughExecutor(t *testing.T) {
	e := newEnv(t, nil)
	ctx := context.Background()
	chain := policy.AgentIdentity{OrgID: "org", AgentID: "analyst", OnBehalfOf: "angel", Chain: []string{"angel", "assistant", "sales", "analyst"}}
	o, _ := e.exec.Execute(ctx, chain, req("crm", "list_deals", nil))
	if o.Status != StatusExecuted {
		t.Fatalf("%+v", o)
	}
	deep := policy.AgentIdentity{OrgID: "org", AgentID: "legal", OnBehalfOf: "angel",
		Chain: []string{"angel", "assistant", "sales", "analyst", "operations", "accounting", "legal"}}
	o, _ = e.exec.Execute(ctx, deep, req("calculator", "calculate", map[string]any{"expression": "1+1"}))
	if o.Status != StatusDenied || o.Decision.RuleID != "delegation.depth" {
		t.Fatalf("%+v", o)
	}
	// analyst via assistant cannot write spreadsheets (assistant lacks the grant)
	via := policy.AgentIdentity{OrgID: "org", AgentID: "analyst", OnBehalfOf: "angel", Chain: []string{"angel", "assistant", "analyst"}}
	o, _ = e.exec.Execute(ctx, via, req("spreadsheet", "write", map[string]any{"sheet": "ventas", "row": 0, "column": "ingresos", "value": "1"}))
	if o.Status != StatusDenied {
		t.Fatalf("delegation elevated: %+v", o)
	}
}

func TestRateLimitThroughExecutor(t *testing.T) {
	e := newEnv(t, func(c *policy.Config) {
		c.Limits = []policy.Limit{{ID: "cal", Agent: "*", Tool: "calendar", Action: "schedule", Window: time.Hour, MaxCalls: 1}}
	})
	ctx := context.Background()
	mk := func(h string) ToolRequest {
		return req("calendar", "schedule", map[string]any{"title": "x", "start": "2026-03-03T" + h + ":00:00Z"})
	}
	if o, _ := e.exec.Execute(ctx, ident("operations"), mk("13")); o.Status != StatusExecuted {
		t.Fatalf("%+v", o)
	}
	if o, _ := e.exec.Execute(ctx, ident("operations"), mk("14")); o.Status != StatusDenied || !strings.HasPrefix(o.Decision.RuleID, "limit:") {
		t.Fatalf("%+v", o)
	}
}

type panicTool struct{}

func (panicTool) Name() string          { return "boom" }
func (panicTool) Actions() []ActionSpec { return []ActionSpec{{"read", true, ""}} }
func (panicTool) Execute(context.Context, Call) (Result, error) {
	panic("kaboom")
}

func TestToolPanicIsContained(t *testing.T) {
	e := newEnv(t, func(c *policy.Config) { c.Grants = append(c.Grants, policy.AllowActions("analyst", "boom", "read")...) })
	e.reg.Register(panicTool{})
	e.reg.Allow("analyst", "boom")
	o, err := e.exec.Execute(context.Background(), ident("analyst"), req("boom", "read", nil))
	if err != nil || o.Status != StatusFailed || !strings.Contains(o.Error, "kaboom") {
		t.Fatalf("%+v %v", o, err)
	}
}

func TestExecutorConcurrent(t *testing.T) {
	e := newEnv(t, nil)
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e.exec.Execute(context.Background(), ident("assistant"), req("email", "list", nil))
			e.exec.Execute(context.Background(), ident("assistant"), req("email", "send_reply", map[string]any{"message_id": "m-001", "body": "ok"}))
		}()
	}
	wg.Wait()
	if len(e.suite.Email.SentMessages()) != 30 {
		t.Fatalf("sent %d", len(e.suite.Email.SentMessages()))
	}
}

func TestToolRequestTranslation(t *testing.T) {
	tests := []struct {
		name string
		in   ToolRequest
		want policy.ToolCall
		err  bool
	}{
		{"basic", ToolRequest{Tool: "Email", Action: "Send-Reply", Args: map[string]any{"a": 1}, Risk: "LOW"},
			policy.ToolCall{Tool: "email", Action: "send_reply", Args: map[string]any{"a": 1}, Risk: "low", Source: "task-1"}, false},
		{"no risk", ToolRequest{Tool: "crm", Action: "search"}, policy.ToolCall{Tool: "crm", Action: "search", Source: "task-1"}, false},
		{"unknown risk becomes high", ToolRequest{Tool: "crm", Action: "search", Risk: "nothing-to-see"}, policy.ToolCall{Tool: "crm", Action: "search", Risk: "high", Source: "task-1"}, false},
		{"missing tool", ToolRequest{Action: "x"}, policy.ToolCall{}, true},
		{"missing action", ToolRequest{Tool: "x"}, policy.ToolCall{}, true},
	}
	for _, tc := range tests {
		got, err := tc.in.ToolCall("task-1")
		if (err != nil) != tc.err {
			t.Fatalf("%s: err=%v", tc.name, err)
		}
		if err == nil && (got.Tool != tc.want.Tool || got.Action != tc.want.Action || got.Risk != tc.want.Risk || got.Source != tc.want.Source) {
			t.Errorf("%s: got %+v want %+v", tc.name, got, tc.want)
		}
	}
	if _, err := ToolRequestsFromRuntime([]ToolRequest{{Tool: "a", Action: "b"}, {Tool: "a"}}, "t"); err == nil {
		t.Error("expected error for bad item")
	}
	cs, err := ToolRequestsFromRuntime([]ToolRequest{{Tool: "a", Action: "b"}}, "t")
	if err != nil || len(cs) != 1 {
		t.Error(err)
	}
}

func TestRegistry(t *testing.T) {
	r := NewRegistry()
	s := NewFakeSuite(func() time.Time { return t0 })
	for _, tl := range s.All() {
		if err := r.Register(tl); err != nil {
			t.Fatal(err)
		}
	}
	if got := strings.Join(r.Names(), ","); got != "calculator,calendar,crm,documents,email,spreadsheet" {
		t.Errorf("names %s", got)
	}
	if r.Permitted("a", "email", "read") {
		t.Error("default must be no access")
	}
	r.Allow("a", "Email", "read", "Send-Reply")
	if !r.Permitted("a", "email", "read") || !r.Permitted("a", "email", "send_reply") || r.Permitted("a", "email", "send") {
		t.Error("action-level permission")
	}
	r.Allow("a", "crm")
	if !r.Permitted("a", "crm", "anything") {
		t.Error("tool-level permission")
	}
	if got := strings.Join(r.ToolsFor("a"), ","); got != "crm,email" {
		t.Errorf("toolsfor %s", got)
	}
	r.Revoke("a", "crm")
	if r.Permitted("a", "crm", "search") {
		t.Error("revoke")
	}
	if _, ok := r.Get("EMAIL"); !ok {
		t.Error("get is case-insensitive")
	}
	if err := r.Register(badTool{}); err == nil {
		t.Error("invalid tool name accepted")
	}
	for _, tl := range s.All() {
		if len(tl.Actions()) == 0 {
			t.Errorf("%s has no actions", tl.Name())
		}
	}
}

type badTool struct{}

func (badTool) Name() string                                  { return "Bad Name!" }
func (badTool) Actions() []ActionSpec                         { return nil }
func (badTool) Execute(context.Context, Call) (Result, error) { return Result{}, nil }

func TestActionSpecReadOnlyMatchesPolicy(t *testing.T) {
	s := NewFakeSuite(func() time.Time { return t0 })
	for _, tl := range s.All() {
		for _, a := range tl.Actions() {
			if a.ReadOnly != policy.IsReadOnly(a.Name) {
				t.Errorf("%s.%s: ReadOnly=%v but policy.IsReadOnly=%v", tl.Name(), a.Name, a.ReadOnly, policy.IsReadOnly(a.Name))
			}
		}
	}
}

func TestSeedPermissionsMatchPolicyGrantsTools(t *testing.T) {
	s := NewFakeSuite(func() time.Time { return t0 })
	r := NewFakeRegistry(s)
	for _, a := range []string{"assistant", "sales", "hr", "legal", "accounting", "analyst", "operations"} {
		if len(r.ToolsFor(a)) == 0 {
			t.Errorf("%s has no tools", a)
		}
	}
}

// ------------------------------------------------------------ fake behaviour

func call(action string, args map[string]any) Call { return Call{Action: action, Args: args} }

func TestEmailFake(t *testing.T) {
	s := NewFakeSuite(func() time.Time { return t0 })
	ctx := context.Background()
	if r, err := s.Email.Execute(ctx, call("list", nil)); err != nil || !r.OK || !strings.Contains(r.Summary, "3") {
		t.Fatalf("%+v %v", r, err)
	}
	if _, err := s.Email.Execute(ctx, call("read", map[string]any{"id": "m-001"})); err != nil {
		t.Fatal(err)
	}
	if r, _ := s.Email.Execute(ctx, call("list", map[string]any{"unread": "true"})); !strings.Contains(r.Summary, "2") {
		t.Errorf("unread: %s", r.Summary)
	}
	for _, c := range []Call{
		call("read", map[string]any{"id": "zzz"}), call("search", nil), call("draft", nil), call("send", map[string]any{"to": "a@b.c"}),
		call("send_reply", map[string]any{"message_id": "zzz", "body": "x"}), call("send_reply", map[string]any{"message_id": "m-001"}), call("boom", nil),
	} {
		if _, err := s.Email.Execute(ctx, c); !errors.Is(err, ErrInvalidArgs) {
			t.Errorf("%s: want ErrInvalidArgs, got %v", c.Action, err)
		}
	}
	r, _ := s.Email.Execute(ctx, call("search", map[string]any{"q": "ACME"}))
	if !strings.Contains(r.Summary, "1") {
		t.Errorf("search: %s", r.Summary)
	}
	if _, err := s.Email.Execute(ctx, call("draft", map[string]any{"to": "a@b.c", "body": "x"})); err != nil || len(s.Email.Drafts) != 1 || len(s.Email.Sent) != 0 {
		t.Error("draft must not send")
	}
	// resolver
	m := s.Email.ResolveArgs(ctx, Call{Action: "send_reply", Args: map[string]any{"message_id": "m-003", "to": "X@Y.com"}})
	to, _ := m["to"].([]any)
	if len(to) != 2 || to[0] != "billing@proveedor-nuevo.biz" || to[1] != "x@y.com" {
		t.Errorf("resolved: %v", m)
	}
	if s.Email.ResolveArgs(ctx, Call{Action: "send", Args: nil}) != nil {
		t.Error("resolver only for send_reply")
	}
}

func TestCalendarFake(t *testing.T) {
	s := NewFakeSuite(func() time.Time { return t0 })
	ctx := context.Background()
	day := "2026-03-03"
	r, err := s.Calendar.Execute(ctx, call("list_events", map[string]any{"date": day}))
	if err != nil || !strings.Contains(r.Summary, "2") {
		t.Fatalf("%+v %v", r, err)
	}
	r, _ = s.Calendar.Execute(ctx, call("find_slots", map[string]any{"date": day, "duration_min": 60}))
	slots := r.Data["slots"].([]string)
	for _, sl := range slots {
		if strings.HasPrefix(sl, "2026-03-03T11:00") || strings.HasPrefix(sl, "2026-03-03T09:00") {
			t.Errorf("busy slot offered: %s", sl)
		}
	}
	if len(slots) == 0 {
		t.Error("no slots")
	}
	if _, err := s.Calendar.Execute(ctx, call("schedule", map[string]any{"title": "x", "start": "2026-03-03T11:30:00Z"})); !errors.Is(err, ErrInvalidArgs) {
		t.Errorf("busy slot: %v", err)
	}
	r, err = s.Calendar.Execute(ctx, call("schedule", map[string]any{"title": "x", "start": "2026-03-03T15:00:00Z", "duration_min": 45, "attendees": "a@b.c, d@e.f"}))
	if err != nil {
		t.Fatal(err)
	}
	ev := r.Data["event"].(CalEvent)
	if len(ev.Attendees) != 2 || ev.End.Sub(ev.Start) != 45*time.Minute {
		t.Errorf("%+v", ev)
	}
	if _, err := s.Calendar.Execute(ctx, call("cancel", map[string]any{"event_id": ev.ID})); err != nil {
		t.Fatal(err)
	}
	for _, c := range []Call{call("cancel", map[string]any{"event_id": "no"}), call("schedule", nil), call("schedule", map[string]any{"title": "x", "start": "garbage"}),
		call("find_slots", map[string]any{"duration_min": 0 - 5}), call("zzz", nil)} {
		if _, err := s.Calendar.Execute(ctx, c); err == nil {
			t.Errorf("%s: expected error", c.Action)
		}
	}
}

func TestCRMFake(t *testing.T) {
	s := NewFakeSuite(func() time.Time { return t0 })
	ctx := context.Background()
	if !s.CRM.IsKnown("o", "LAURA@acme.com") || !s.CRM.IsKnown("o", "Laura <laura@acme.com>") || s.CRM.IsKnown("o", "laura@acme.co") {
		t.Error("IsKnown")
	}
	r, _ := s.CRM.Execute(ctx, call("search", map[string]any{"query": "acme"}))
	if !strings.Contains(r.Summary, "1 contact") || !strings.Contains(r.Summary, "1 deal") {
		t.Errorf("%s", r.Summary)
	}
	if _, err := s.CRM.Execute(ctx, call("add_contact", map[string]any{"name": "N", "email": "n@x.com"})); err != nil {
		t.Fatal(err)
	}
	if !s.CRM.IsKnown("o", "n@x.com") {
		t.Error("added contact must be known")
	}
	if _, err := s.CRM.Execute(ctx, call("update_deal", map[string]any{"deal_id": "d-001", "stage": "won", "value": 52000})); err != nil {
		t.Fatal(err)
	}
	r, _ = s.CRM.Execute(ctx, call("list_deals", map[string]any{"stage": "won"}))
	if !strings.Contains(r.Summary, "1 deal") {
		t.Errorf("%s", r.Summary)
	}
	for _, c := range []Call{call("add_contact", map[string]any{"name": "x", "email": "nope"}), call("update_deal", map[string]any{"deal_id": "zz"}),
		call("update_deal", map[string]any{"deal_id": "d-001", "value": -1}), call("get_contact", map[string]any{"id": "zz"}), call("log_note", nil), call("search", nil)} {
		if _, err := s.CRM.Execute(ctx, c); err == nil {
			t.Errorf("%s: expected error", c.Action)
		}
	}
	if _, err := s.CRM.Execute(ctx, call("log_note", map[string]any{"contact_id": "c-001", "note": "llamar"})); err != nil {
		t.Fatal(err)
	}
}

func TestDocumentsFake(t *testing.T) {
	s := NewFakeSuite(func() time.Time { return t0 })
	ctx := context.Background()
	r, _ := s.Documents.Execute(ctx, call("read", map[string]any{"doc_id": "doc-004"}))
	if len(r.ExternalContent) != 1 {
		t.Error("external docs must be flagged as external content")
	}
	r, _ = s.Documents.Execute(ctx, call("read", map[string]any{"doc_id": "doc-001"}))
	if len(r.ExternalContent) != 0 {
		t.Error("internal doc is not external content")
	}
	r, err := s.Documents.Execute(ctx, call("create", map[string]any{"title": "Informe", "content": "x", "type": "Report"}))
	if err != nil || r.Data["doc_id"] == "" {
		t.Fatal(err)
	}
	if m := s.Documents.ResolveArgs(ctx, Call{Args: map[string]any{"doc_id": "doc-002"}}); m["document_type"] != "contract" {
		t.Errorf("resolve: %v", m)
	}
	if s.Documents.ResolveArgs(ctx, Call{Args: map[string]any{"doc_id": "zzz"}}) != nil {
		t.Error("unknown doc")
	}
	for _, c := range []Call{call("read", map[string]any{"doc_id": "zz"}), call("send_proposal", map[string]any{"doc_id": "doc-001"}), call("create", nil), call("search", nil)} {
		if _, err := s.Documents.Execute(ctx, c); err == nil {
			t.Errorf("%s: expected error", c.Action)
		}
	}
}

func TestSpreadsheetFake(t *testing.T) {
	s := NewFakeSuite(func() time.Time { return t0 })
	ctx := context.Background()
	for op, want := range map[string]float64{"sum": 415000, "avg": 103750, "min": 90000, "max": 120000, "count": 4} {
		r, err := s.Spreadsheet.Execute(ctx, call("compute", map[string]any{"sheet": "ventas", "column": "Ingresos", "op": op}))
		if err != nil || r.Data["value"].(float64) != want {
			t.Errorf("%s: %+v %v want %v", op, r, err, want)
		}
	}
	if _, err := s.Spreadsheet.Execute(ctx, call("write", map[string]any{"sheet": "ventas", "row": 0, "column": "ingresos", "value": "1"})); err != nil {
		t.Fatal(err)
	}
	for _, c := range []Call{call("read", map[string]any{"sheet": "nope"}), call("compute", map[string]any{"sheet": "ventas", "column": "zz", "op": "sum"}),
		call("compute", map[string]any{"sheet": "ventas", "column": "mes", "op": "sum"}), call("compute", map[string]any{"sheet": "ventas", "column": "costos", "op": "median"}),
		call("write", map[string]any{"sheet": "ventas", "row": 99, "column": "costos"}), call("zzz", map[string]any{"sheet": "ventas"})} {
		if _, err := s.Spreadsheet.Execute(ctx, c); err == nil {
			t.Errorf("%s %v: expected error", c.Action, c.Args)
		}
	}
}

func TestCalculator(t *testing.T) {
	ok := map[string]float64{
		"1+2*3": 7, "(1+2)*3": 9, "-4+10": 6, "2^3^2": 512, "10/4": 2.5, "10%4": 2, " 3 * ( 2 + -1 ) ": 3, "1.5+1.5": 3, "--2": 2, "+5": 5,
		"50000*(1-0.24)": 38000,
	}
	for in, want := range ok {
		got, err := EvalExpr(in)
		if err != nil || got != want {
			t.Errorf("%q: %v %v want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"", "1+", "(1", "1)", "a+1", "1/0", "1%0", "1..2+x", "2^10000", "os.Exit(1)", "1 2", strings.Repeat("(", 100) + "1" + strings.Repeat(")", 100), strings.Repeat("1+", 300) + "1"} {
		if _, err := EvalExpr(in); err == nil {
			t.Errorf("%q should fail", in)
		}
	}
	c := NewCalculator()
	r, err := c.Execute(context.Background(), call("margin", map[string]any{"revenue": 50000, "cost": 38000}))
	if err != nil || r.Data["margin_pct"].(float64) != 24 {
		t.Errorf("%+v %v", r, err)
	}
	r, err = c.Execute(context.Background(), call("percent_change", map[string]any{"from": 100, "to": 80}))
	if err != nil || r.Data["change_pct"].(float64) != -20 {
		t.Errorf("%+v %v", r, err)
	}
	for _, cc := range []Call{call("margin", map[string]any{"revenue": 0, "cost": 1}), call("percent_change", nil), call("calculate", nil), call("zz", nil)} {
		if _, err := c.Execute(context.Background(), cc); err == nil {
			t.Errorf("%s: expected error", cc.Action)
		}
	}
}

func TestRedactArgs(t *testing.T) {
	in := map[string]any{"Password": "x", "nested": map[string]any{"auth_token": "y", "ok": "v"}, "list": []any{map[string]any{"secret": "s"}}, "n": 3}
	out := RedactArgs(in)
	if out["Password"] != "[redacted]" || out["nested"].(map[string]any)["auth_token"] != "[redacted]" || out["nested"].(map[string]any)["ok"] != "v" {
		t.Errorf("%v", out)
	}
	if out["list"].([]any)[0].(map[string]any)["secret"] != "[redacted]" {
		t.Errorf("%v", out)
	}
	if in["Password"] != "x" {
		t.Error("input mutated")
	}
	if RedactArgs(nil) != nil {
		t.Error("nil")
	}
}

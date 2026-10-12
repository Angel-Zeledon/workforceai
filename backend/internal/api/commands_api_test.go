package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"aiworkforce/backend/internal/api"
	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/controls"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/drive"
	"aiworkforce/backend/internal/events"
	"aiworkforce/backend/internal/infrastructure/memory"
	"aiworkforce/backend/internal/objstore"
	"aiworkforce/backend/internal/rewards"
	"aiworkforce/backend/internal/roles"
	"aiworkforce/backend/internal/skills"
)

// K1: the slash commands over HTTP (docs/architecture/commands.md). The runtime is a
// double that records what the commands send it: no model is ever called.

type cmdRT struct {
	fakeRuntime
	mu    sync.Mutex
	plans []application.PlanRequest
	sums  []application.SummarizeRequest
}

func (r *cmdRT) Plan(ctx context.Context, in application.PlanRequest) (application.PlanResponse, error) {
	r.mu.Lock()
	r.plans = append(r.plans, in)
	r.mu.Unlock()
	return r.fakeRuntime.Plan(ctx, in)
}

func (r *cmdRT) Summarize(_ context.Context, in application.SummarizeRequest) (application.SummarizeResponse, error) {
	r.mu.Lock()
	r.sums = append(r.sums, in)
	r.mu.Unlock()
	return application.SummarizeResponse{Text: fmt.Sprintf("SUM[%s|%s] n=%d", in.Mode, in.Locale, len(in.Messages)),
		Usage: application.Usage{Model: "sim", InputTokens: 400, OutputTokens: 30, CostUSD: 0.001}}, nil
}

func (r *cmdRT) ContextSizes(_ context.Context, in application.RunTaskRequest) (application.ContextSizesResponse, error) {
	return application.ContextSizesResponse{TotalTokens: 1500, Segments: []application.ContextSegment{
		{Key: "profile", Tokens: 900}, {Key: "skills", Tokens: 200}, {Key: "memory", Tokens: 100}, {Key: "task", Tokens: 300}}}, nil
}

func (r *cmdRT) planTexts() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, p := range r.plans {
		out = append(out, p.RequestText)
	}
	return out
}

type cmdEnv struct {
	*env
	rt                           *cmdRT
	dr                           *drive.Service
	sk                           *skills.Service
	owner, admin, member, viewer string
	cfg                          application.Config
	org                          string
}

type cmdOpts struct {
	budget  float64
	limit   auth.Limit
	llmLim  auth.Limit
	models  api.ModelCatalog
	longMem api.LongMemory
}

func newCmdEnv(t *testing.T, o cmdOpts) *cmdEnv {
	t.Helper()
	cfg := application.DefaultConfig()
	cfg.IdleDelay = 0
	if o.budget > 0 {
		cfg.BudgetUSD = o.budget
	}
	mem := memory.New()
	if err := mem.Seed(context.Background(), domain.SeedOrg(cfg.BudgetUSD), roles.SeedAgents()); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	hub := events.NewHub(log)
	rec := &application.Recorder{OrgID: cfg.OrgID, Store: mem, Pub: events.LocalBus{Hub: hub}, Log: log}
	appr := application.NewApprovals(cfg, mem, rec)
	q := &application.Queries{Store: mem, Cfg: cfg}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	rt := &cmdRT{}
	orch := application.NewOrchestrator(ctx, cfg, mem, rt, memory.NewLocker(), rec, appr, q, log)
	orch.SetConvContext(mem)
	oc := &application.OrgConfig{Cfg: cfg, Store: mem, Core: mem, Orch: orch, Rec: rec, Log: log}
	orch.SetStyle(oc)

	d := api.Deps{Cfg: cfg, Queries: q, Orch: orch, Approvals: appr, Store: mem, Runtime: rt, Hub: hub, Log: log, Rec: rec, OrgConfig: oc, AuthEnabled: true,
		Commands: &api.CommandsDeps{RunLimit: o.limit, LLMRunLimit: o.llmLim, Models: o.models, LongMemory: o.longMem}}
	d.Chats = &application.ChatHistory{Store: mem, Core: mem, Rec: rec, Cfg: cfg}
	d.Audit = &application.AuditService{Store: mem, Rec: rec, Cfg: cfg}
	d.ModelPolicy = &application.ModelPolicyService{Store: mem, Rec: rec, Cfg: cfg}
	audit := func(c context.Context, org, actor, action, entity, id string, det map[string]any) {
		rec.Audit(application.WithOrg(c, org), domain.AuditLog{Actor: actor, Action: action, Entity: entity, EntityID: id, Details: det})
	}
	d.Controls = controls.New(controls.NewMemStore(), controls.AuditFunc(audit), controls.EmitFunc(func(context.Context, string, string, map[string]any) {}))
	attachWorkspaces(&d, mem, rec, appr, orch, log, ctx, nil)
	dr := drive.New(drive.Config{Store: drive.NewMemStore(), Objects: objstore.NewMemory(), OrgID: cfg.OrgID, Log: log,
		Audit: func(c context.Context, org string, e drive.AuditEntry) {
			rec.Audit(application.WithOrg(c, org), domain.AuditLog{Actor: e.Actor.ID, Action: e.Action, Entity: e.Entity, EntityID: e.EntityID, Details: e.Details})
		}})
	d.Drive = dr
	sk := skills.New(skills.Config{Store: skills.NewMemStore(), Drive: dr, OrgID: cfg.OrgID, Log: log,
		AgentRole: func(c context.Context, org, id string) (string, error) {
			a, err := mem.GetAgent(c, org, id)
			return a.Role, err
		}})
	d.Skills = sk
	pr := &application.ProfileService{Store: mem, Core: mem, Tones: mem, Rec: rec, Config: cfg}
	tr := &application.Tracer{Store: mem, Log: log, Retention: 30 * 24 * time.Hour}
	orch.SetV2(mem, pr, tr)
	d.Profiles, d.V2, d.Tracer = pr, mem, tr
	cat, err := rewards.LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	d.Rewards = &rewards.Service{Store: rewards.NewMemStore(), Catalog: cat, Rules: rewards.DefaultRules(), Rec: rec, Log: log,
		Agents: func(c context.Context, org, id string) bool { _, err := mem.GetAgent(c, org, id); return err == nil }}

	svc, err := auth.NewService(auth.Config{Store: auth.NewMemoryStore(), Secret: []byte("0123456789abcdef0123456789abcdef-test"),
		PasswordParams: auth.PasswordParams{Memory: 8, Time: 1, Threads: 1, SaltLen: 16, KeyLen: 32}})
	if err != nil {
		t.Fatal(err)
	}
	d.Auth = svc
	d.AuthRoutes = auth.NewHandler(svc, auth.HandlerConfig{}).Routes()
	ts := httptest.NewServer(api.NewRouter(d))
	t.Cleanup(ts.Close)
	e := &env{t: t, srv: ts, svc: svc, store: mem, hub: hub, appr: appr}
	ce := &cmdEnv{env: e, rt: rt, dr: dr, sk: sk, cfg: cfg}
	owner := e.register("owner-cmd@example.com", "OwnerCmd")
	ce.owner, ce.org = owner.AccessToken, owner.OrgID
	ce.admin = e.member(owner, "admin-cmd@example.com", auth.RoleAdmin)
	ce.member = e.member(owner, "member-cmd@example.com", auth.RoleMember)
	ce.viewer = e.member(owner, "viewer-cmd@example.com", auth.RoleViewer)
	return ce
}

// run posts /commands/run and returns the HTTP status and the decoded envelope.
func (e *cmdEnv) run(token string, body map[string]any) (int, map[string]any) {
	e.t.Helper()
	resp, raw := e.do("POST", api1+"/commands/run", token, body, nil)
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		e.t.Fatalf("run %v: %d %s", body, resp.StatusCode, raw)
	}
	return resp.StatusCode, out
}

func (e *cmdEnv) ok(token string, body map[string]any) map[string]any {
	e.t.Helper()
	st, out := e.run(token, body)
	if st != 200 || out["status"] == "error" {
		e.t.Fatalf("run %v = %d %v", body, st, out)
	}
	return out
}

func (e *cmdEnv) fails(token string, body map[string]any, status int, code string) map[string]any {
	e.t.Helper()
	st, out := e.run(token, body)
	er, _ := out["error"].(map[string]any)
	if st != status || out["status"] != "error" || er == nil || er["code"] != code {
		e.t.Fatalf("run %v = %d %v, want %d %s", body, st, out, status, code)
	}
	return out
}

func resultOf(out map[string]any) map[string]any { r, _ := out["result"].(map[string]any); return r }

func agentCtx(agent string) map[string]any {
	return map[string]any{"surface": "agent", "agent_id": agent}
}

var officeCtx = map[string]any{"surface": "office"}

func (e *cmdEnv) say(token, conv, text string, want int) {
	e.t.Helper()
	e.json("POST", api1+"/messages", token, map[string]string{"conversation": conv, "text": text}, 202)
	e.eventually("replies in "+conv, func() bool {
		return len(e.messagesOf(token, conv)) >= want
	})
}

func (e *cmdEnv) messagesOf(token, conv string) []domain.Message {
	resp, raw := e.do("GET", api1+"/conversations/"+conv+"/messages", token, nil, nil)
	if resp.StatusCode != 200 {
		e.t.Fatalf("messages %s = %d %s", conv, resp.StatusCode, raw)
	}
	var out []domain.Message
	_ = json.Unmarshal(raw, &out)
	return out
}

func (e *cmdEnv) auditOf(action string) []domain.AuditLog {
	page, err := e.store.QueryAudit(context.Background(), e.org, domain.AuditQuery{Action: action, Limit: 1000})
	if err != nil {
		e.t.Fatal(err)
	}
	return page.Items
}

// ---- catalog, RBAC, audit, idempotency, rate limit ----

func TestCommandsCatalogIsFlaggedByRole(t *testing.T) {
	e := newCmdEnv(t, cmdOpts{})
	if st := e.status("GET", api1+"/commands", "", nil); st != 401 {
		t.Fatalf("anonymous = %d", st)
	}
	cat := func(token string) map[string]map[string]any {
		out := map[string]map[string]any{}
		for _, c := range e.list(api1+"/commands", token) {
			out[c["id"].(string)] = c
		}
		return out
	}
	v, a := cat(e.viewer), cat(e.admin)
	if len(v) < 37 {
		t.Fatalf("only %d commands", len(v))
	}
	for id, c := range v {
		al := c["aliases"].(map[string]any)
		if len(al["es"].([]any)) == 0 || len(al["en"].([]any)) == 0 || c["title"].(map[string]any)["es"] == "" || c["permission"] == "" {
			t.Errorf("%s: incomplete %v", id, c)
		}
	}
	for id, want := range map[string][2]bool{"status": {true, true}, "clear": {false, true}, "pause": {false, true}, "hire": {false, true}, "memory": {true, true}, "language": {false, true}} {
		if v[id]["allowed"] != want[0] || a[id]["allowed"] != want[1] {
			t.Errorf("%s allowed: viewer=%v admin=%v", id, v[id]["allowed"], a[id]["allowed"])
		}
	}
	if v["stop"]["destructive"] != true || v["compact"]["uses_llm"] != true || v["shop"]["client_only"] != true {
		t.Errorf("flags: %v %v %v", v["stop"], v["compact"], v["shop"])
	}
	if a["compact"]["available"] != true {
		t.Errorf("compact must be available: %v", a["compact"])
	}
}

func TestCommandsRBACAuditAndNoSecretsInAudit(t *testing.T) {
	e := newCmdEnv(t, cmdOpts{})
	e.fails(e.viewer, map[string]any{"command": "clear", "context": officeCtx}, 403, "forbidden")
	e.fails(e.member, map[string]any{"command": "pause", "args": map[string]any{"agent": "sales"}, "context": officeCtx}, 403, "forbidden")
	e.fails(e.member, map[string]any{"command": "language", "args": map[string]any{"value": "en"}, "context": officeCtx}, 403, "forbidden")
	e.fails(e.member, map[string]any{"command": "hire", "args": map[string]any{"role": "legal"}, "context": officeCtx}, 403, "forbidden")
	e.fails(e.owner, map[string]any{"command": "nonsense", "context": officeCtx}, 404, "unknown_command")
	if n := len(e.auditOf("command.denied")); n != 5 {
		t.Fatalf("denied runs audited = %d, want 5", n)
	}

	// the viewer may read, the member may write memory
	e.ok(e.viewer, map[string]any{"command": "status", "context": officeCtx})
	secret := "mi-clave-secreta-XYZ123"
	e.ok(e.member, map[string]any{"command": "remember", "args": map[string]any{"text": secret, "agent": "sales"}, "context": officeCtx})
	runs := e.auditOf("command.run")
	if len(runs) == 0 {
		t.Fatal("runs must be audited")
	}
	for _, l := range append(runs, e.auditOf("command.denied")...) {
		raw, _ := json.Marshal(l)
		if strings.Contains(string(raw), "XYZ123") || !strings.Contains(string(raw), "arg_keys") {
			t.Fatalf("audit leaks an argument value or lacks arg_keys: %s", raw)
		}
		if l.Entity != "command" || l.Actor == "" || l.Actor == "system" {
			t.Fatalf("audit entry: %+v", l)
		}
	}
	last := runs[len(runs)-1]
	keys, _ := json.Marshal(last.Details.(map[string]any)["arg_keys"])
	if last.EntityID != "remember" || string(keys) != `["agent","text"]` {
		t.Fatalf("remember audit: %+v %s", last, keys)
	}
	// admin pauses an agent: the REST control shows it
	e.ok(e.admin, map[string]any{"command": "pause", "args": map[string]any{"agent": "sales"}, "context": officeCtx})
	found := false
	for _, ac := range e.json("GET", api1+"/org/controls", e.admin, nil, 200)["agents"].([]any) {
		if m := ac.(map[string]any); m["agent_id"] == "sales" && m["control"] == "paused" {
			found = true
		}
	}
	if !found {
		t.Fatal("pause did not pause the agent")
	}
	e.ok(e.admin, map[string]any{"command": "resume", "args": map[string]any{"agent": "sales"}, "context": officeCtx})
}

func TestCommandsIdempotencyAndRateLimit(t *testing.T) {
	e := newCmdEnv(t, cmdOpts{limit: auth.Limit{Max: 6, Window: time.Minute}})
	body := map[string]any{"command": "remember", "args": map[string]any{"text": "una sola vez", "agent": "sales"}, "context": officeCtx, "idempotency_key": "k-1"}
	first := e.ok(e.member, body)
	resp, raw := e.do("POST", api1+"/commands/run", e.member, body, nil)
	if resp.StatusCode != 200 || resp.Header.Get("Idempotent-Replay") != "true" {
		t.Fatalf("replay = %d %v %s", resp.StatusCode, resp.Header, raw)
	}
	ms, _ := e.store.ListMemory(context.Background(), e.org, "sales")
	n := 0
	for _, m := range ms {
		if m.Value == "una sola vez" {
			n++
		}
	}
	if n != 1 || first["status"] != "ok" {
		t.Fatalf("remembered %d times", n)
	}
	// 6 runs per minute per person
	var limited bool
	for i := 0; i < 8 && !limited; i++ {
		st, out := e.run(e.member, map[string]any{"command": "status", "context": officeCtx})
		if st == 429 {
			limited = true
			if out["error"].(map[string]any)["code"] != "rate_limited" {
				t.Fatalf("429 body: %v", out)
			}
		}
	}
	if !limited {
		t.Fatal("no rate limit")
	}
	if st, _ := e.run(e.admin, map[string]any{"command": "status", "context": officeCtx}); st != 200 { // another person is not affected
		t.Fatalf("admin was limited too: %d", st)
	}
}

// ---- language follows the alias ----

func TestCommandsAnswerInTheLanguageOfTheAlias(t *testing.T) {
	e := newCmdEnv(t, cmdOpts{})
	title := func(body map[string]any) (string, string) {
		out := e.ok(e.member, body)
		return resultOf(out)["title"].(string), out["lang"].(string)
	}
	if ti, l := title(map[string]any{"command": "uso", "context": officeCtx}); ti != "Uso y costo" || l != "es" {
		t.Fatalf("/uso: %q %q", ti, l)
	}
	if ti, l := title(map[string]any{"command": "usage", "context": officeCtx}); ti != "Usage and cost" || l != "en" {
		t.Fatalf("/usage: %q %q", ti, l)
	}
	if ti, l := title(map[string]any{"command": "/costo", "raw": "/costo", "context": officeCtx}); ti != "Uso y costo" || l != "es" {
		t.Fatalf("/costo: %q %q", ti, l)
	}
	// the UI sends lang from the alias; it wins over everything
	if ti, _ := title(map[string]any{"command": "usage", "lang": "es", "context": officeCtx}); ti != "Uso y costo" {
		t.Fatalf("explicit lang: %q", ti)
	}
	// the same word in both languages (/pdf): the lang field decides the error text
	for lang, want := range map[string]string{"es": "Falta el argumento", "en": "Missing argument"} {
		out := e.fails(e.member, map[string]any{"command": "pdf", "lang": lang, "context": officeCtx}, 400, "missing_arg")
		if msg := out["error"].(map[string]any)["message"].(string); !strings.Contains(msg, want) || out["lang"] != lang {
			t.Fatalf("/pdf %s: %q", lang, msg)
		}
	}
	// without lang: detected from the text typed
	e.ok(e.member, map[string]any{"raw": "/pdf resumen del cliente para la junta de ventas", "context": officeCtx})
	e.ok(e.member, map[string]any{"raw": "/pdf summary of the client for the sales board", "context": officeCtx})
	e.eventually("both plans", func() bool { return len(e.rt.planTexts()) >= 2 })
	texts := strings.Join(e.rt.planTexts(), "\n")
	if !strings.Contains(texts, "[[lang:es]] [[format:pdf]] resumen del cliente") || !strings.Contains(texts, "[[lang:en]] [[format:pdf]] summary of the client") {
		t.Fatalf("the requests do not carry the language of what was typed:\n%s", texts)
	}

	// /compact in a 1:1 chat: the LLM instruction language, the notice and the result follow the alias
	e.say(e.member, "agent:sales", "Hola, necesito el precio de la propuesta", 2)
	e.say(e.member, "agent:sales", "Y el plazo de entrega", 4)
	out := e.ok(e.member, map[string]any{"command": "resumir", "context": agentCtx("sales")})
	if r := resultOf(out); !strings.HasPrefix(r["text"].(string), "SUM[compact|es]") || out["lang"] != "es" {
		t.Fatalf("/resumir: %v", out)
	}
	e.say(e.member, "agent:sales", "Otro tema: el descuento", 7)
	e.say(e.member, "agent:sales", "Y la garantia", 9)
	out = e.ok(e.member, map[string]any{"command": "compact", "context": agentCtx("sales")})
	if r := resultOf(out); !strings.HasPrefix(r["text"].(string), "SUM[compact|en]") || r["title"] != "Conversation compacted" {
		t.Fatalf("/compact: %v", out)
	}
	var notices []string
	for _, m := range e.messagesOf(e.member, "agent:sales") {
		if m.From == "system" {
			notices = append(notices, m.Text)
		}
	}
	if len(notices) != 2 || !strings.Contains(notices[0], "Conversación resumida") || !strings.Contains(notices[1], "Conversation compacted") {
		t.Fatalf("notices: %v", notices)
	}
	e.rt.mu.Lock()
	defer e.rt.mu.Unlock()
	if len(e.rt.sums) != 2 || e.rt.sums[0].Locale != "es" || e.rt.sums[1].Locale != "en" || e.rt.sums[0].Mode != "compact" {
		t.Fatalf("the summarizer must get the language of the alias: %+v", e.rt.sums)
	}
}

// ---- control group ----

func TestCommandsClearCompactContextUsage(t *testing.T) {
	e := newCmdEnv(t, cmdOpts{})
	e.fails(e.member, map[string]any{"command": "compact", "context": agentCtx("sales")}, 422, "nothing_to_do")
	e.say(e.member, "agent:sales", "primer mensaje de la conversación", 2)
	e.say(e.member, "agent:sales", "segundo mensaje con más contenido para resumir", 4)

	out := e.ok(e.member, map[string]any{"command": "compact", "context": agentCtx("sales")})
	d := resultOf(out)["data"].(map[string]any)
	if d["tokens_before"].(float64) <= 0 || d["tokens_after"].(float64) <= 0 || d["messages"].(float64) < 2 {
		t.Fatalf("compact data: %v", d)
	}
	cc, _, _ := e.store.GetConvContext(context.Background(), e.org, "agent:sales")
	if !strings.HasPrefix(cc.Summary, "SUM[compact|") || cc.SummaryUpto.IsZero() || cc.SummaryTokens == 0 {
		t.Fatalf("the summary must be stored as the conversation context: %+v", cc)
	}
	if u, _ := e.store.ListUsage(context.Background(), e.org); len(u) == 0 {
		t.Fatal("the summary must be in the usage ledger")
	}

	// /clear starts a new epoch; the history stays
	before := len(e.messagesOf(e.member, "agent:sales"))
	out = e.ok(e.member, map[string]any{"command": "limpiar", "context": agentCtx("sales")})
	if resultOf(out)["data"].(map[string]any)["epoch"].(float64) != 1 {
		t.Fatalf("clear: %v", out)
	}
	cc, _, _ = e.store.GetConvContext(context.Background(), e.org, "agent:sales")
	if cc.Summary != "" || cc.Epoch != 1 || cc.ClearedAt.IsZero() {
		t.Fatalf("after clear: %+v", cc)
	}
	if after := len(e.messagesOf(e.member, "agent:sales")); after <= before {
		t.Fatalf("nothing may be deleted by /clear (%d -> %d)", before, after)
	}
	// the office conversation is independent
	e.ok(e.member, map[string]any{"command": "clear", "context": officeCtx})
	if cc, _, _ := e.store.GetConvContext(context.Background(), e.org, "office"); cc.Epoch != 1 {
		t.Fatalf("office: %+v", cc)
	}

	// /context: the runtime builder's segments + the conversation, with fill %
	out = e.ok(e.viewer, map[string]any{"raw": "/contexto @sales", "context": officeCtx})
	r := resultOf(out)
	rows := r["rows"].([]any)
	labels := ""
	for _, row := range rows {
		labels += row.([]any)[0].(string) + "|"
	}
	data := r["data"].(map[string]any)
	if !strings.Contains(labels, "Perfil y reglas del agente") || !strings.Contains(labels, "Conversación") || data["total_tokens"].(float64) < 1500 || data["fill_pct"].(float64) <= 0 || data["window_tokens"].(float64) < 100000 {
		t.Fatalf("context: %v", r)
	}
	if en := resultOf(e.ok(e.viewer, map[string]any{"raw": "/context @sales", "context": officeCtx})); en["columns"].([]any)[1] != "Approx. tokens" {
		t.Fatalf("context in English: %v", en)
	}

	// /usage: today, month, conversation, budget, coins
	_ = e.store.AddUsage(context.Background(), e.org, domain.UsageEntry{ID: "u1", AgentID: "sales", Kind: domain.UsageChat, CostUSD: 0.25, TS: time.Now().UTC()})
	out = e.ok(e.viewer, map[string]any{"command": "usage", "context": agentCtx("sales")})
	d = resultOf(out)["data"].(map[string]any)
	if d["today_usd"].(float64) < 0.25 || d["month_usd"].(float64) < 0.25 || d["conversation_usd"].(float64) < 0.25 || d["budget_left_usd"] == nil || d["coins_balance"] == nil {
		t.Fatalf("usage: %v", d)
	}
}

func TestCommandsModelAndMemory(t *testing.T) {
	e := newCmdEnv(t, cmdOpts{})
	// show: any reader
	if r := resultOf(e.ok(e.viewer, map[string]any{"command": "modelo", "context": officeCtx})); r["kind"] != "table" {
		t.Fatalf("show model: %v", r)
	}
	e.fails(e.member, map[string]any{"command": "model", "args": map[string]any{"value": "anthropic"}, "context": officeCtx}, 403, "forbidden")
	e.ok(e.admin, map[string]any{"raw": "/modelo anthropic", "context": officeCtx})
	pol := e.json("GET", api1+"/settings/model-policy", e.admin, nil, 200)["policy"].(map[string]any)
	if pref := pol["preferred_providers"].([]any); len(pref) == 0 || pref[0] != "anthropic" {
		t.Fatalf("policy: %v", pol)
	}
	e.ok(e.admin, map[string]any{"raw": "/model kimi @legal", "context": officeCtx})
	pol = e.json("GET", api1+"/settings/model-policy", e.admin, nil, 200)["policy"].(map[string]any)
	if rp := pol["role_providers"].(map[string]any); rp["legal"].([]any)[0] != "custom" {
		t.Fatalf("role providers: %v", pol)
	}
	// BYO seam not wired: a graceful "not available"
	e.fails(e.admin, map[string]any{"command": "model", "args": map[string]any{"value": "mi-modelo-propio"}, "context": officeCtx}, 501, "not_available")

	// memory
	e.ok(e.member, map[string]any{"raw": "/recordar el precio base es 100 @sales", "context": officeCtx})
	e.ok(e.member, map[string]any{"raw": "/recordar el cliente prefiere los lunes @sales", "context": officeCtx})
	r := resultOf(e.ok(e.viewer, map[string]any{"raw": "/memoria @sales", "context": officeCtx}))
	if len(r["rows"].([]any)) != 2 {
		t.Fatalf("view: %v", r)
	}
	e.fails(e.viewer, map[string]any{"raw": "/memoria olvidar precio @sales", "context": officeCtx}, 403, "forbidden")
	out := e.ok(e.member, map[string]any{"raw": "/memoria olvidar precio @sales", "context": officeCtx})
	if out["status"] != "needs_confirmation" {
		t.Fatalf("forgetting must ask: %v", out)
	}
	cm := out["confirm"].(map[string]any)["message"].(map[string]any)
	if !strings.Contains(cm["es"].(string), "1 recuerdos") || !strings.Contains(cm["en"].(string), "1 memories") {
		t.Fatalf("confirm message: %v", cm)
	}
	if ms, _ := e.store.ListMemory(context.Background(), e.org, "sales"); len(ms) != 2 {
		t.Fatalf("nothing may change before the confirmation: %v", ms)
	}
	e.ok(e.member, map[string]any{"raw": "/memoria olvidar precio @sales", "confirm": true, "context": officeCtx})
	if ms, _ := e.store.ListMemory(context.Background(), e.org, "sales"); len(ms) != 1 || !strings.Contains(ms[0].Value, "lunes") {
		t.Fatalf("after forgetting: %v", ms)
	}
	out = e.ok(e.member, map[string]any{"command": "memory", "args": map[string]any{"action": "delete", "agent": "sales"}, "context": officeCtx})
	if out["status"] != "needs_confirmation" {
		t.Fatalf("deleting all must ask: %v", out)
	}
	e.ok(e.member, map[string]any{"command": "memory", "args": map[string]any{"action": "delete", "agent": "sales"}, "confirm": true, "context": officeCtx})
	if ms, _ := e.store.ListMemory(context.Background(), e.org, "sales"); len(ms) != 0 {
		t.Fatalf("after delete: %v", ms)
	}
	e.fails(e.member, map[string]any{"raw": "/memoria olvidar nada @sales", "confirm": true, "context": officeCtx}, 422, "nothing_to_do")
}

type fakeLongMemory struct{ remembered, forgot []string }

func (f *fakeLongMemory) List(context.Context, string, string) ([]api.MemoryItem, error) {
	return []api.MemoryItem{{Scope: "long", Key: "k1", Value: "hecho largo"}}, nil
}
func (f *fakeLongMemory) Remember(_ context.Context, _, _, text string) error {
	f.remembered = append(f.remembered, text)
	return nil
}
func (f *fakeLongMemory) Forget(_ context.Context, _, _, text string) (int, error) {
	f.forgot = append(f.forgot, text)
	return 1, nil
}

func TestCommandsLongMemorySeam(t *testing.T) {
	lm := &fakeLongMemory{}
	e := newCmdEnv(t, cmdOpts{longMem: lm})
	e.ok(e.member, map[string]any{"raw": "/remember likes mondays @sales", "context": officeCtx})
	if len(lm.remembered) != 1 || lm.remembered[0] != "likes mondays" {
		t.Fatalf("remember went to the old store: %v", lm.remembered)
	}
	if r := resultOf(e.ok(e.viewer, map[string]any{"raw": "/memory @sales", "context": officeCtx})); len(r["rows"].([]any)) != 1 {
		t.Fatalf("view: %v", r)
	}
	e.ok(e.member, map[string]any{"raw": "/memory forget largo @sales", "confirm": true, "context": officeCtx})
	if len(lm.forgot) != 1 {
		t.Fatalf("forget: %v", lm.forgot)
	}
}

type fakeModels struct{}

func (fakeModels) Resolve(_ context.Context, _, name string) (string, bool) {
	return "custom", name == "mi-modelo-propio"
}

func TestCommandsModelBYOSeam(t *testing.T) {
	e := newCmdEnv(t, cmdOpts{models: fakeModels{}})
	e.ok(e.admin, map[string]any{"command": "model", "args": map[string]any{"value": "mi-modelo-propio"}, "context": officeCtx})
	e.fails(e.admin, map[string]any{"command": "model", "args": map[string]any{"value": "otro"}, "context": officeCtx}, 400, "invalid_args")
}

func TestCommandsPauseStopStatusTraceExport(t *testing.T) {
	e := newCmdEnv(t, cmdOpts{})
	e.fails(e.admin, map[string]any{"command": "pause", "context": officeCtx}, 400, "missing_arg")
	e.fails(e.admin, map[string]any{"command": "pause", "args": map[string]any{"agent": "nadie"}, "context": officeCtx}, 400, "unknown_agent")
	// the 1:1 chat names the agent
	e.ok(e.admin, map[string]any{"command": "pausar", "context": agentCtx("legal")})
	out := e.ok(e.admin, map[string]any{"command": "stop", "args": map[string]any{"agent": "legal"}, "context": officeCtx})
	if out["status"] != "needs_confirmation" || out["confirm"].(map[string]any)["message"].(map[string]any)["en"] == "" {
		t.Fatalf("stop must confirm: %v", out)
	}
	e.ok(e.admin, map[string]any{"command": "stop", "args": map[string]any{"agent": "legal"}, "confirm": true, "context": officeCtx})
	e.ok(e.admin, map[string]any{"command": "reanudar", "args": map[string]any{"agent": "legal"}, "context": officeCtx})

	r := resultOf(e.ok(e.viewer, map[string]any{"command": "estado", "context": officeCtx}))
	if len(r["rows"].([]any)) < 6 || r["data"].(map[string]any)["pending_approvals"] == nil {
		t.Fatalf("status: %v", r)
	}

	// trace: none yet, then the newest
	e.fails(e.admin, map[string]any{"command": "trace", "args": map[string]any{"agent": "sales"}, "context": officeCtx}, 422, "nothing_to_do")
	e.fails(e.member, map[string]any{"command": "trace", "args": map[string]any{"agent": "sales"}, "context": officeCtx}, 403, "forbidden") // audit:read
	_ = e.store.AddTrace(context.Background(), e.org, application.Trace{ID: "tr-1", AgentID: "sales", TaskID: "t-9", CreatedAt: time.Now().UTC()})
	r = resultOf(e.ok(e.admin, map[string]any{"command": "traza", "args": map[string]any{"agent": "sales"}, "context": officeCtx}))
	if r["kind"] != "navigate" || r["href"] != "/office?agent=sales&trace=tr-1" {
		t.Fatalf("trace: %v", r)
	}

	// export
	e.fails(e.member, map[string]any{"command": "export", "context": agentCtx("sales")}, 422, "nothing_to_do")
	e.say(e.member, "agent:sales", "texto exportable único 42", 2)
	e.fails(e.viewer, map[string]any{"command": "export", "context": agentCtx("sales")}, 403, "forbidden") // exporting writes a file
	r = resultOf(e.ok(e.member, map[string]any{"raw": "/exportar", "context": agentCtx("sales")}))
	if r["kind"] != "download" || !strings.HasSuffix(r["filename"].(string), ".md") {
		t.Fatalf("export md: %v", r)
	}
	resp, raw := e.do("GET", r["url"].(string), e.member, nil, nil)
	if resp.StatusCode != 200 || !strings.Contains(string(raw), "texto exportable único 42") {
		t.Fatalf("download = %d %s", resp.StatusCode, raw)
	}
	r = resultOf(e.ok(e.member, map[string]any{"raw": "/export pdf", "context": agentCtx("sales")}))
	resp, raw = e.do("GET", r["url"].(string), e.member, nil, nil)
	if resp.StatusCode != 200 || !strings.HasPrefix(string(raw), "%PDF") || !strings.HasSuffix(r["filename"].(string), ".pdf") {
		t.Fatalf("pdf download = %d %.20s", resp.StatusCode, raw)
	}
}

// ---- office group ----

func (e *cmdEnv) lastPlan(contains string) string {
	e.t.Helper()
	var found string
	e.eventually("a plan with "+contains, func() bool {
		for _, p := range e.rt.planTexts() {
			if strings.Contains(p, contains) {
				found = p
				return true
			}
		}
		return false
	})
	return found
}

func TestCommandsDeliverablesMeetingAssignDrafts(t *testing.T) {
	e := newCmdEnv(t, cmdOpts{})
	for cmd, mark := range map[string]string{"excel": "[[format:xlsx]]", "word": "[[format:docx]]", "pdf": "[[format:pdf]]", "presentation": "[[format:pdf]]"} {
		out := e.ok(e.member, map[string]any{"command": cmd, "args": map[string]any{"text": "tema-" + cmd}, "lang": "es", "context": officeCtx})
		r := resultOf(out)
		if out["status"] != "started" || r["kind"] != "request" || r["request_id"] == "" {
			t.Fatalf("%s: %v", cmd, out)
		}
		if p := e.lastPlan("tema-" + cmd); !strings.Contains(p, "[[lang:es]]") || !strings.Contains(p, mark) {
			t.Fatalf("%s plan text: %q", cmd, p)
		}
	}
	e.fails(e.viewer, map[string]any{"command": "excel", "args": map[string]any{"text": "x"}, "context": officeCtx}, 403, "forbidden")

	// meeting: agents before the colon, topic after; a conclusion by the assistant is the planner's job (markers)
	out := e.ok(e.member, map[string]any{"raw": "/reunion ventas legal contabilidad: precios 2027", "context": officeCtx})
	if out["status"] != "started" || out["lang"] != "es" {
		t.Fatalf("meeting: %v", out)
	}
	p := e.lastPlan("precios 2027")
	if !strings.Contains(p, "[[meeting:sales,legal,accounting]]") || !strings.Contains(p, "[[lang:es]]") || !strings.Contains(p, "la asistente redacta la conclusión") {
		t.Fatalf("meeting plan text: %q", p)
	}
	e.ok(e.member, map[string]any{"raw": "/meeting sales legal: pricing 2028", "context": officeCtx})
	if p := e.lastPlan("pricing 2028"); !strings.Contains(p, "[[lang:en]]") || !strings.Contains(p, "the assistant writes the conclusion") {
		t.Fatalf("meeting in English: %q", p)
	}
	e.fails(e.member, map[string]any{"raw": "/reunion ventas fantasma: tema", "context": officeCtx}, 400, "unknown_agent")
	e.fails(e.member, map[string]any{"raw": "/reunion ventas legal", "context": officeCtx}, 400, "missing_arg")

	// assign: a direct task, no planner involved
	n := len(e.rt.planTexts())
	out = e.ok(e.member, map[string]any{"raw": "/encargar @sales prepara la cotización de Acme", "context": officeCtx})
	id := resultOf(out)["request_id"].(string)
	e.eventually("assigned task", func() bool {
		d := e.json("GET", api1+"/requests/"+id, e.member, nil, 200)
		ts, _ := d["tasks"].([]any)
		return len(ts) == 1 && ts[0].(map[string]any)["agent_id"] == "sales"
	})
	if len(e.rt.planTexts()) != n {
		t.Fatal("/assign must not call the planner")
	}
	e.ok(e.member, map[string]any{"raw": "/assign @legal review the NDA", "context": officeCtx})

	// email / linkedin: a draft request that carries the instruction not to send
	e.ok(e.member, map[string]any{"raw": "/correo avisar al cliente del retraso", "context": officeCtx})
	if p := e.lastPlan("avisar al cliente del retraso"); !strings.Contains(p, "[[draft:email]]") || !strings.Contains(p, "NO lo envíes") {
		t.Fatalf("email plan text: %q", p)
	}
	e.ok(e.member, map[string]any{"raw": "/linkedin announce the launch", "context": officeCtx})
	if p := e.lastPlan("announce the launch"); !strings.Contains(p, "[[draft:linkedin]]") || !strings.Contains(p, "do NOT publish") {
		t.Fatalf("linkedin plan text: %q", p)
	}
	// nothing was sent: no email was ever executed by a command
	for _, l := range e.auditOf("tool.executed") {
		t.Fatalf("a command executed a tool: %+v", l)
	}
}

func TestCommandsNavigationSearchFilesAndSuggestions(t *testing.T) {
	e := newCmdEnv(t, cmdOpts{})
	if r := resultOf(e.ok(e.member, map[string]any{"command": "proyecto", "context": officeCtx})); r["kind"] != "navigate" || r["href"] != "/projects/new" {
		t.Fatalf("project: %v", r)
	}
	e.fails(e.member, map[string]any{"raw": "/project nope", "context": officeCtx}, 400, "not_found")
	for cmd, href := range map[string]string{"tienda": "/shop", "inventory": "/shop?tab=inventory"} {
		if r := resultOf(e.ok(e.viewer, map[string]any{"command": cmd, "context": officeCtx})); r["href"] != href {
			t.Fatalf("%s: %v", cmd, r)
		}
	}
	res, err := e.dr.Write(application.WithOrg(context.Background(), e.org), drive.Human("someone"), drive.WriteInput{Path: "/Shared/Proyectos/contrato-acme.txt", Data: []byte("Contrato con Acme por servicios"), Message: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	r := resultOf(e.ok(e.viewer, map[string]any{"raw": "/abrir /Shared/Proyectos/contrato-acme.txt", "context": officeCtx}))
	if r["href"] != "/files?open="+res.File.ID {
		t.Fatalf("open: %v", r)
	}
	e.fails(e.viewer, map[string]any{"raw": "/abrir /Shared/Proyectos/no-existe.txt", "context": officeCtx}, 404, "not_found")
	r = resultOf(e.ok(e.viewer, map[string]any{"raw": "/buscar Acme", "context": officeCtx}))
	hits := r["data"].(map[string]any)["hits"].([]any)
	if len(hits) == 0 || hits[0].(map[string]any)["href"] != "/files?open="+res.File.ID {
		t.Fatalf("search: %v", r)
	}
	e.fails(e.viewer, map[string]any{"raw": "/search zzzqqq", "context": officeCtx}, 422, "nothing_to_do")

	// autocompletion
	comp := func(kind, q string) []map[string]any {
		return e.list(api1+"/commands/complete?kind="+kind+"&q="+q, e.viewer)
	}
	if a := comp("agent", "sal"); len(a) != 1 || a[0]["value"] != "sales" {
		t.Fatalf("complete agent: %v", a)
	}
	if f := comp("file", "Acme"); len(f) == 0 || f[0]["value"] != "/Shared/Proyectos/contrato-acme.txt" {
		t.Fatalf("complete file: %v", f)
	}
	if s := comp("skill", ""); len(s) == 0 {
		t.Fatalf("complete skill: %v", s)
	}
	if st := e.status("GET", api1+"/commands/complete?kind=bogus", e.viewer, nil); st != 400 {
		t.Fatalf("bad kind = %d", st)
	}

	// suggestions: long conversation -> compact
	_ = e.store.CreateConversation(context.Background(), e.org, domain.Conversation{ID: "office", Title: "Office", LastMessageAt: time.Now()})
	for i := 0; i < 45; i++ {
		_ = e.store.AddMessage(context.Background(), e.org, domain.Message{ID: fmt.Sprintf("m%02d", i), ConversationID: "office", From: "user", To: "all", Kind: "chat", Text: fmt.Sprintf("mensaje %d", i), TS: time.Now().UTC().Add(-time.Hour + time.Duration(i)*time.Millisecond)})
	}
	sg := e.list(api1+"/commands/suggest?surface=office&conversation_id=office", e.member)
	if len(sg) == 0 || sg[0]["command"] != "compact" || sg[0]["reason"].(map[string]any)["en"] == "" {
		t.Fatalf("suggest: %v", sg)
	}
	e.ok(e.member, map[string]any{"command": "clear", "context": officeCtx})
	if sg := e.list(api1+"/commands/suggest?surface=office&conversation_id=office", e.member); len(sg) != 0 {
		t.Fatalf("after /clear there is nothing to compact: %v", sg)
	}
}

func TestCommandsBudgetSuggestionAndLLMCommandsRespectIt(t *testing.T) {
	e := newCmdEnv(t, cmdOpts{budget: 1})
	ctx := context.Background()
	_ = e.store.CreateRequest(ctx, e.org, domain.Request{ID: "r1", Text: "x", Status: domain.RequestRunning, CreatedAt: time.Now()})
	_ = e.store.AddCost(ctx, e.org, "r1", "", 0.85)
	sg := e.list(api1+"/commands/suggest?surface=office", e.member)
	if len(sg) == 0 || sg[0]["command"] != "usage" {
		t.Fatalf("suggest: %v", sg)
	}
	// no money left: /compact is refused before any model call, like a chat reply
	e.say(e.member, "agent:sales", "hola, un mensaje para la conversación", 1)
	_ = e.store.AddCost(ctx, e.org, "r1", "", 0.5)
	_ = e.store.CreateConversation(ctx, e.org, domain.Conversation{ID: "agent:legal", LastMessageAt: time.Now()})
	for i := 0; i < 3; i++ {
		_ = e.store.AddMessage(ctx, e.org, domain.Message{ID: fmt.Sprintf("l%d", i), ConversationID: "agent:legal", From: "user", To: "legal", Kind: "chat", Text: "texto", TS: time.Now().UTC().Add(time.Duration(i) * time.Millisecond)})
	}
	e.fails(e.member, map[string]any{"command": "compact", "context": agentCtx("legal")}, 409, "budget_exceeded")
	if len(e.rt.sums) != 0 {
		t.Fatal("the model must not be called without budget")
	}
}

func TestCommandsProfileTemplatesSkillsUndoDaily(t *testing.T) {
	e := newCmdEnv(t, cmdOpts{})
	ctx := application.WithOrg(context.Background(), e.org)

	// tone: one agent (agents:write) or the organization (org:manage)
	e.ok(e.admin, map[string]any{"raw": "/tono mx @sales", "context": officeCtx})
	if p := e.json("GET", api1+"/agents/sales/profile", e.admin, nil, 200); p["tone"] != "mx" {
		t.Fatalf("profile: %v", p)
	}
	e.fails(e.admin, map[string]any{"raw": "/tone zz @sales", "context": officeCtx}, 400, "invalid_args")
	e.ok(e.admin, map[string]any{"raw": "/tone ar", "context": officeCtx})
	if s := e.json("GET", api1+"/org/settings", e.admin, nil, 200); s["tone"] != "ar" {
		t.Fatalf("org settings: %v", s)
	}
	e.fails(e.member, map[string]any{"raw": "/tono co", "context": officeCtx}, 403, "forbidden")
	out := e.ok(e.admin, map[string]any{"raw": "/language en", "context": officeCtx})
	if s := e.json("GET", api1+"/org/settings", e.admin, nil, 200); s["locale"] != "en" || resultOf(out)["title"] != "Language changed" {
		t.Fatalf("language: %v %v", s, out)
	}

	// save-template: the last request becomes an organization template
	e.fails(e.member, map[string]any{"raw": "/plantilla guardar Mi plantilla", "context": officeCtx}, 422, "nothing_to_do")
	e.ok(e.member, map[string]any{"raw": "/encargar @sales prepara la cotización mensual", "context": officeCtx})
	out = e.ok(e.member, map[string]any{"raw": "/plantilla guardar Cotización mensual", "context": officeCtx})
	found := false
	for _, tpl := range e.list(api1+"/project-templates", e.member) {
		if tpl["name"] == "Cotización mensual" {
			found = true
		}
	}
	if !found {
		t.Fatalf("template not saved: %v", out)
	}

	// learn: the last finished task becomes a skill DRAFT; skill: assign an active library skill
	fin := time.Now().UTC()
	_ = e.store.CreateTask(ctx, e.org, domain.Task{ID: "tk1", RequestID: "r-none", Title: "Cotización de Acme", AgentID: "sales", Status: domain.TaskDone, CreatedAt: fin, FinishedAt: &fin,
		Output: &domain.StructuredOutput{Summary: "Se cotizó con 5% de descuento", Findings: []string{"el margen es 30%"}}})
	e.ok(e.member, map[string]any{"raw": "/aprender cotizacion-acme @sales", "context": officeCtx})
	var learned map[string]any
	for _, s := range e.list(api1+"/skills", e.member) {
		if s["name"] == "cotizacion-acme" {
			learned = s
		}
	}
	if learned == nil || learned["status"] == "active" || learned["source"] != "from_work" {
		t.Fatalf("learned skill: %v", learned)
	}
	var active string
	for _, s := range e.list(api1+"/skills", e.member) {
		if s["status"] == "active" {
			active = s["name"].(string)
		}
	}
	if active == "" {
		t.Fatal("the library has no active skill")
	}
	e.fails(e.member, map[string]any{"command": "skill", "args": map[string]any{"skill": active, "agent": "sales"}, "context": officeCtx}, 403, "forbidden")
	e.ok(e.admin, map[string]any{"command": "skill", "args": map[string]any{"skill": active, "agent": "sales"}, "context": officeCtx})
	as := e.list(api1+"/agents/sales/skills", e.admin)
	if len(as) == 0 {
		t.Fatal("the skill was not assigned")
	}
	e.fails(e.admin, map[string]any{"command": "skill", "args": map[string]any{"skill": "nope", "agent": "sales"}, "context": officeCtx}, 404, "not_found")

	// undo: the agent's last drive change goes back one version, after a confirmation
	e.fails(e.member, map[string]any{"command": "undo", "args": map[string]any{"agent": "sales"}, "context": officeCtx}, 422, "nothing_to_do")
	ag := drive.AgentP("sales")
	if _, err := e.dr.Write(ctx, ag, drive.WriteInput{Path: "/Agents/sales/desk/nota.md", Data: []byte("version uno"), Message: "v1"}); err != nil {
		t.Fatal(err)
	}
	res, err := e.dr.Write(ctx, ag, drive.WriteInput{Path: "/Agents/sales/desk/nota.md", Data: []byte("version dos"), Message: "v2"})
	if err != nil {
		t.Fatal(err)
	}
	out = e.ok(e.member, map[string]any{"command": "deshacer", "args": map[string]any{"agent": "sales"}, "context": officeCtx})
	if out["status"] != "needs_confirmation" {
		t.Fatalf("undo must ask: %v", out)
	}
	e.ok(e.member, map[string]any{"command": "undo", "args": map[string]any{"agent": "sales"}, "confirm": true, "context": officeCtx})
	f, _ := e.dr.GetFile(ctx, res.File.ID)
	rc, _, err := e.dr.Open(ctx, f, 0)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(rc)
	rc.Close()
	if f.CurrentVersion != 3 || string(b) != "version uno" {
		t.Fatalf("after undo: v%d %q", f.CurrentVersion, b)
	}

	// daily summary: nothing happened -> no model call; with work done -> the summary in the alias language
	n := len(e.rt.sums)
	if r := resultOf(e.ok(e.member, map[string]any{"command": "resumen-del-dia", "context": officeCtx})); !strings.Contains(r["text"].(string), "todavía no hubo actividad") && !strings.Contains(r["text"].(string), "SUM[daily|es]") {
		t.Fatalf("daily: %v", r)
	}
	_ = n
	r := resultOf(e.ok(e.member, map[string]any{"command": "daily", "context": officeCtx}))
	if !strings.HasPrefix(r["text"].(string), "SUM[daily|en]") {
		t.Fatalf("daily in English: %v", r)
	}
	pend := resultOf(e.ok(e.viewer, map[string]any{"command": "pendientes", "context": officeCtx}))
	if pend["kind"] != "table" {
		t.Fatalf("pending: %v", pend)
	}
}

// ---- org group ----

func TestCommandsOrgGroup(t *testing.T) {
	e := newCmdEnv(t, cmdOpts{})
	r := resultOf(e.ok(e.viewer, map[string]any{"command": "equipo", "context": officeCtx}))
	if len(r["rows"].([]any)) < 6 {
		t.Fatalf("team: %v", r)
	}
	before := len(e.list(api1+"/agents", e.admin))
	e.fails(e.member, map[string]any{"raw": "/contratar legal", "context": officeCtx}, 403, "forbidden")
	e.fails(e.admin, map[string]any{"raw": "/hire astronaut", "context": officeCtx}, 404, "not_found")
	out := e.ok(e.admin, map[string]any{"raw": "/hire legal", "context": officeCtx})
	if len(e.list(api1+"/agents", e.admin)) != before+1 || resultOf(out)["data"].(map[string]any)["agent_id"] == "" {
		t.Fatalf("hire: %v", out)
	}
	p := resultOf(e.ok(e.viewer, map[string]any{"command": "permisos", "context": officeCtx}))
	labels := ""
	for _, row := range p["rows"].([]any) {
		labels += row.([]any)[0].(string) + "|"
	}
	if !strings.Contains(labels, "Quién decide aprobaciones") || !strings.Contains(labels, "Autonomía de") {
		t.Fatalf("permissions: %v", labels)
	}
	if en := resultOf(e.ok(e.viewer, map[string]any{"command": "permissions", "context": officeCtx})); en["title"] != "Who approves what" {
		t.Fatalf("permissions in English: %v", en)
	}
	e.fails(e.owner, map[string]any{"command": "status", "context": map[string]any{"surface": "mars"}}, 400, "surface_not_supported")
	e.fails(e.owner, map[string]any{"command": "status", "context": map[string]any{"surface": "agent", "agent_id": "ghost"}}, 400, "unknown_agent")
}

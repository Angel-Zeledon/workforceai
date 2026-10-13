package tenantdata_test

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"aiworkforce/backend/internal/api"
	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/artifacts"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/connections"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/drive"
	"aiworkforce/backend/internal/infrastructure/postgres"
	"aiworkforce/backend/internal/push"
	"aiworkforce/backend/internal/rewards"
	"aiworkforce/backend/internal/roles"
	"aiworkforce/backend/internal/tenantdata"
	"aiworkforce/backend/internal/vault"
)

// Postgres integration tests: they need TEST_DATABASE_URL pointing to a database
// where the user may CREATE SCHEMA and CREATE ROLE (a throwaway container):
//
//	docker run --rm -d -p 55433:5432 -e POSTGRES_PASSWORD=pw postgres:16
//	TEST_DATABASE_URL=postgres://postgres:pw@127.0.0.1:55433/postgres?sslmode=disable go test ./internal/tenantdata/ -run PG -v
//
// Each test applies ALL the embedded migrations in a temporary schema through
// postgres.Open (exactly like the server: the pool runs as app_user, RLS on) and
// uses a second pool with the owner credentials for the purge, like main does.

type env struct {
	st      *postgres.Store
	owner   *pgxpool.Pool
	auth    *auth.Service
	td      *tenantdata.Service
	purger  *tenantdata.Purger
	janitor *tenantdata.Janitor
	router  http.Handler
	limiter *auth.MemoryLimiter
}

func setup(t *testing.T) *env {
	t.Helper()
	raw := os.Getenv("TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	schema := "tdtest_" + hex.EncodeToString(b)
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(raw)
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	st, err := postgres.Open(ctx, postgres.Options{URL: u.String(), AppRole: "app_user"})
	if err != nil {
		t.Fatal(err)
	}
	owner, err := pgxpool.New(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		st.Close()
		owner.Close()
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
		admin.Close(ctx)
	})
	authStore := auth.NewPGStore(st.Pool())
	limiter := auth.NewMemoryLimiter()
	svc, err := auth.NewService(auth.Config{Store: authStore, Secret: []byte("0123456789abcdef0123456789abcdef-test"),
		PasswordParams: auth.PasswordParams{Memory: 8, Time: 1, Threads: 1, SaltLen: 16, KeyLen: 32}, TrialEnabled: true, LoginLimiter: limiter})
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	td := &tenantdata.Service{App: st.Pool(), Accounts: authStore, Limiter: limiter}
	purger := &tenantdata.Purger{Owner: owner, Log: log}
	rec := &application.Recorder{OrgID: domain.DemoOrgID, Store: st, Log: log}
	router := api.NewRouter(api.Deps{Cfg: application.Config{OrgID: domain.DemoOrgID, BudgetUSD: 10}, Store: st, Log: log, Rec: rec,
		AuthEnabled: true, Auth: svc, TenantData: td})
	return &env{st: st, owner: owner, auth: svc, td: td, purger: purger, router: router, limiter: limiter,
		janitor: &tenantdata.Janitor{Pool: owner, Purger: purger, Log: log}}
}

const password = "Correct-Horse-9"

type tenant struct {
	sess  *auth.Session
	org   string
	user  string
	email string
}

func (e *env) register(t *testing.T, email, orgName string) tenant {
	t.Helper()
	s, err := e.auth.Register(context.Background(), auth.RegisterInput{Email: email, Password: password, Name: "N " + email, OrgName: orgName}, auth.ClientMeta{UserAgent: "UA-" + email, IP: "10.0.0.1"})
	if err != nil {
		t.Fatalf("register %s: %v", email, err)
	}
	return tenant{sess: s, org: s.OrgID, user: s.User.ID, email: email}
}

func principal(tn tenant) auth.Principal {
	return auth.Principal{UserID: tn.user, OrgID: tn.org, Role: tn.sess.Role}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// populate writes real data through the real stores and services into many tables.
func (e *env) populate(t *testing.T, tn tenant) {
	t.Helper()
	ctx := context.Background()
	org, st := tn.org, e.st
	octx := application.WithOrg(ctx, org)
	now := time.Now().UTC()
	must(t, st.Seed(ctx, domain.Organization{ID: org, Name: "ignored", Slug: "ignored", BudgetUSD: 10}, roles.SeedAgents()))
	req, task := "rq-"+org, "tk-"+org
	must(t, st.CreateRequest(ctx, org, domain.Request{ID: req, Text: "secret of " + org, Status: domain.RequestRunning, CreatedAt: now}))
	must(t, st.CreateTask(ctx, org, domain.Task{ID: task, RequestID: req, Title: "task " + org, AgentID: "sales", Status: domain.TaskPending, CreatedAt: now, Depth: 1}))
	must(t, st.CreateConversation(ctx, org, domain.Conversation{ID: "cv-" + org, Title: "c", Participants: []string{"user"}, LastMessageAt: now}))
	must(t, st.AddMessage(ctx, org, domain.Message{ID: "ms-" + org, ConversationID: "cv-" + org, From: "user", To: "assistant", Kind: "chat", Text: "hola " + org, TS: now}))
	must(t, st.CreateApproval(ctx, org, domain.Approval{ID: "ap-" + org, TaskID: task, AgentID: "sales", Action: "send_proposal", Title: "t", Risk: "high", Status: domain.ApprovalPending, CreatedAt: now}))
	must(t, st.CreateReport(ctx, org, domain.Report{ID: "rp-" + org, RequestID: req, Title: "r", CreatedAt: now}))
	must(t, st.SetMemory(ctx, org, "sales", domain.Memory{Scope: "agent", Key: "k", Value: "v-" + org}))
	must(t, st.SaveEvent(ctx, org, domain.Event{ID: "ev-" + org, Type: "x", TS: now}))
	must(t, st.AddActivity(ctx, org, domain.ActivityItem{ID: "ac-" + org, TS: now, Kind: "k", Text: "t"}))
	must(t, st.AddAudit(ctx, org, domain.AuditLog{ID: "au1-" + org, Actor: tn.user, Action: "test.one", TS: now}))
	must(t, st.AddAudit(ctx, org, domain.AuditLog{ID: "au2-" + org, Actor: tn.user, Action: "test.two", TS: now.Add(time.Millisecond)}))
	must(t, st.AddCost(ctx, org, req, task, 1.5))
	must(t, st.AddUsage(ctx, org, domain.UsageEntry{ID: "us-" + org, RequestID: req, TaskID: task, AgentID: "sales", Kind: domain.UsageRunTask, CostUSD: 1.5, Tools: []string{"email.send"}}))
	must(t, st.SetBudgetCap(ctx, org, domain.ScopeAgent, "sales", 5))
	must(t, st.PutOrgSettings(ctx, org, application.OrgSettings{Configured: true, Locale: "es", Tone: "neutral"}))
	must(t, st.SetAgentTone(ctx, org, "sales", "mx"))
	must(t, st.PutSchedule(ctx, org, application.Schedule{ID: "sc-" + org, TemplateKey: "daily", Hour: 8, Timezone: "UTC", Enabled: true, CreatedAt: now}))
	must(t, st.SaveCounters(ctx, org, "policy", []byte(`{"n":1}`)))
	must(t, st.AddSteerNote(ctx, org, application.SteerNote{ID: "sn-" + org, TaskID: task, AgentID: "sales", Note: "go", Author: tn.user, CreatedAt: now}))
	must(t, st.AddTrace(ctx, org, application.Trace{ID: "tr-" + org, AgentID: "sales", TaskID: task, RequestID: req, Kind: "run-task", Attempt: 1, CreatedAt: now}))
	must(t, st.SetTraceStorePrompts(ctx, org, true))
	if _, _, _, err := st.Earn(ctx, org, rewards.Entry{Kind: rewards.KindEarn, Amount: 10, Reason: rewards.ReasonTaskDone, RefKind: rewards.RefTask, RefID: task,
		AgentID: "sales", Actor: "system", Title: "T", IdemKey: "earn:task:" + task}); err != nil {
		t.Fatal(err)
	}
	must(t, st.PutLLMCredential(ctx, org, application.LLMCredentialRecord{Provider: "openai", Model: "gpt-x", Sealed: []byte("SEALED-KEY-" + org), Last4: "wxyz", CreatedAt: now, UpdatedAt: now, UpdatedBy: tn.user}))

	// Drive (files in the Postgres object store: drive_objects) and skills (versions hang from skills).
	dsvc := drive.New(drive.Config{Store: &postgres.DriveStore{S: st}, Objects: &postgres.ObjectStore{S: st}, OrgID: org})
	if _, err := dsvc.Write(octx, drive.Human(tn.user), drive.WriteInput{Path: "/Shared/contrato.txt", Data: []byte("contenido de " + org)}); err != nil {
		t.Fatal(err)
	}
	if _, err := dsvc.Write(octx, drive.Human(tn.user), drive.WriteInput{Path: "/Shared/contrato.txt", Data: []byte("contenido v2 de " + org)}); err != nil {
		t.Fatal(err)
	}
	// Skills: written as the application would (skills.Service.Create also installs every starter skill,
	// hundreds of round trips through the test tunnel), so the rows go in directly, through RLS.
	must(t, st.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		for _, q := range []string{
			`INSERT INTO skills (org_id, id, name, description, status, source, current_version, created_by) VALUES ($1, 'sk1', 'skill-uno', 'd', 'active', 'created', 1, $2)`,
			`INSERT INTO skill_versions (org_id, skill_id, version, author, description) VALUES ($1, 'sk1', 1, $2, 'd')`,
			`INSERT INTO skill_assignments (org_id, agent_id, skill_id, assigned_by) VALUES ($1, 'sales', 'sk1', $2)`,
		} {
			if _, err := tx.Exec(ctx, q, org, tn.user); err != nil {
				return err
			}
		}
		return nil
	}))

	// Artifacts with a version and a PDF blob (bytea).
	as := &postgres.ArtifactStore{S: st}
	content := []byte(`{"schema":"aiw.doc/1","doc":{"type":"doc","content":[]}}`)
	m := artifacts.Meta{ID: "art-" + org, Kind: artifacts.KindDoc, Title: "Doc " + org, Status: "draft", HeadVersion: 1, CreatedBy: artifacts.ActorRef{Kind: "user", ID: tn.user},
		LastAuthor: artifacts.ActorRef{Kind: "user", ID: tn.user}, Progress: 100, BuildState: "done", Locale: "es", CreatedAt: now, UpdatedAt: now, SizeBytes: len(content)}
	must(t, as.Create(ctx, org, artifacts.Stored{Meta: m, Content: content}, artifacts.Version{VersionInfo: artifacts.VersionInfo{ArtifactID: m.ID, Version: 1, Author: m.CreatedBy, Source: "create", CreatedAt: now}, Content: content}))
	pdf := []byte("%PDF-1.4 blob of " + org)
	must(t, as.PutBlob(ctx, org, artifacts.Blob{ID: "blob-" + org, ArtifactID: m.ID, MIME: "application/pdf", Filename: "a.pdf", SHA256: "00", Size: len(pdf), CreatedBy: m.CreatedBy, CreatedAt: now, Data: pdf}))

	// Connections + vault (credential ciphertext, wrapped data key): secrets that must never be exported.
	cs := &postgres.ConnStore{S: st}
	must(t, cs.CreateConnection(ctx, connections.Connection{ID: "cn-" + org, OrgID: org, Provider: "google_gmail", Kind: "oauth2", Label: "Gmail", Mode: "live", Status: "active",
		Class: "read", RequestedCapabilities: []string{"mail.read"}, GrantedCapabilities: []string{"mail.read"}, ProviderScopes: []string{}, CreatedAt: now, UpdatedAt: now}))
	kw, err := vault.NewEnvKeyWrapper(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{5}, 32)))
	must(t, err)
	if _, err := vault.New(&postgres.VaultRepo{S: st}, kw).Put(ctx, org, "cn-"+org, vault.KindAPIKey, vault.SecretFromString("CANARY_PLAINTEXT_"+org), tn.user, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := (&postgres.PushStore{S: st}).SavePushSubscription(ctx, push.Subscription{ID: "ps-" + org, OrgID: org, UserID: tn.user, Endpoint: "https://push.example/ENDPOINT-" + org,
		P256dh: "P256DH-" + org, Auth: "PUSHAUTH-" + org, UserAgent: "ua", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.auth.CreateInvitation(ctx, principal(tn), "invitee+"+tn.email, auth.RoleMember); err != nil {
		t.Fatal(err)
	}
	// Rows no store writes in this setup: a pending OAuth handshake and an e-mail verification of the owner.
	if _, err := e.owner.Exec(ctx, `INSERT INTO oauth_states (state_hash, org_id, connection_id, user_id, pkce_verifier_enc, redirect_uri, expires_at)
		VALUES ($1, $2, $3, $4, $5, 'https://x', now() + interval '1 hour')`, "STATEHASH-"+org, org, "cn-"+org, tn.user, []byte("PKCE-"+org)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.owner.Exec(ctx, `INSERT INTO email_verifications (token_hash, user_id, expires_at) VALUES ($1, $2, now() + interval '1 day')`, "EVHASH-"+tn.user, tn.user); err != nil {
		t.Fatal(err)
	}
}

func (e *env) do(t *testing.T, method, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	r := httptest.NewRequest(method, path, rd)
	r.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	e.router.ServeHTTP(w, r)
	return w
}

// orgTables lists every table of the live schema that has an org_id column (independent of the package's plan).
func (e *env) orgTables(t *testing.T) []string {
	t.Helper()
	rows, err := e.owner.Query(context.Background(), `SELECT c.relname FROM pg_class c JOIN pg_attribute a ON a.attrelid = c.oid AND a.attname = 'org_id' AND NOT a.attisdropped
		WHERE c.relnamespace = (SELECT oid FROM pg_namespace WHERE nspname = current_schema()) AND c.relkind IN ('r','p') ORDER BY 1`)
	must(t, err)
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		must(t, rows.Scan(&n))
		out = append(out, n)
	}
	return out
}

func (e *env) count(t *testing.T, sql string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := e.owner.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatalf("%v\n%s", err, sql)
	}
	return n
}

// rowsOf counts the rows of org in every table carrying organization data (raw org_id plus the plan's child scopes).
func (e *env) rowsOf(t *testing.T, org string) map[string]int64 {
	t.Helper()
	sc, err := tenantdata.Discover(context.Background(), e.owner)
	must(t, err)
	out := map[string]int64{}
	for _, n := range e.orgTables(t) {
		out[n] = e.count(t, `SELECT count(*) FROM `+pgx.Identifier{n}.Sanitize()+` WHERE org_id = $1`, org)
	}
	for _, n := range sc.OfKind(tenantdata.KindChild) {
		pred, _ := sc.OrgScope(n, "t")
		out[n] = e.count(t, `SELECT count(*) FROM `+pgx.Identifier{n}.Sanitize()+` AS t WHERE `+pred, org)
	}
	return out
}

// snapshot is the exact content of everything org owns (plus its member accounts), to compare before/after.
func (e *env) snapshot(t *testing.T, org string) map[string]string {
	t.Helper()
	ctx := context.Background()
	sc, err := tenantdata.Discover(ctx, e.owner)
	must(t, err)
	out := map[string]string{}
	add := func(name, pred string) {
		var s string
		if err := e.owner.QueryRow(ctx, `SELECT coalesce(string_agg(to_jsonb(t)::text, E'\n' ORDER BY to_jsonb(t)::text), '') FROM `+pgx.Identifier{name}.Sanitize()+` AS t WHERE `+pred, org).Scan(&s); err != nil {
			t.Fatalf("snapshot %s: %v", name, err)
		}
		out[name] = s
	}
	for _, n := range sc.OfKind(tenantdata.KindOrg, tenantdata.KindChild) {
		pred, _ := sc.OrgScope(n, "t")
		add(n, pred)
	}
	add("users", tenantdata.MemberScope("t"))
	add("email_verifications", `t.user_id IN (SELECT m.user_id FROM memberships m WHERE m.org_id = $1)`)
	return out
}

func TestPGGuardEveryOrgTableIsCovered(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	sc, err := tenantdata.Discover(ctx, e.owner)
	must(t, err)
	if u := sc.Unclassified(); len(u) != 0 {
		t.Fatalf("tables neither organization data, account data nor declared global: %v (classify them: internal/tenantdata/README.md)", u)
	}
	exported := map[string]bool{}
	for _, x := range sc.ExportPlan() {
		exported[x.Name] = true
	}
	scopes, err := sc.PurgeScopes()
	must(t, err)
	purged := map[string]bool{tenantdata.OrgTable: true}
	for _, s := range scopes {
		purged[s.Name] = true
	}
	tables := e.orgTables(t)
	if len(tables) < 50 {
		t.Fatalf("only %d tables with org_id: migrations not applied?", len(tables))
	}
	for _, n := range tables {
		if _, excluded := tenantdata.ExcludedFromExport[n]; !exported[n] && !excluded {
			t.Errorf("table %s has org_id but is not exported", n)
		}
		if !purged[n] {
			t.Errorf("table %s has org_id but is not purged", n)
		}
	}
	for _, n := range sc.OfKind(tenantdata.KindChild) {
		if !purged[n] {
			t.Errorf("child table %s is not purged", n)
		}
	}
	if _, err := sc.UserDependents(); err != nil {
		t.Fatal(err)
	}
	t.Logf("%d tables with org_id, %d child tables, export plan %d tables, purge order %d tables", len(tables), len(sc.OfKind(tenantdata.KindChild)), len(exported), len(scopes))
}

func TestPGAppendOnlyStillHoldsForTheApplication(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	a := e.register(t, "a@example.com", "Org A")
	must(t, e.st.AddAudit(ctx, a.org, domain.AuditLog{ID: "x1", Actor: "u", Action: "a"}))
	// The app role cannot use the purge door: it has no DELETE privilege.
	err := e.st.WithOrgTx(ctx, a.org, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('app.purge_org', $1, true)`, a.org); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `DELETE FROM audit_logs`)
		return err
	})
	if err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("app_user delete of audit_logs with app.purge_org: %v", err)
	}
	// The owner without the purge marker still hits the trigger.
	if _, err := e.owner.Exec(ctx, `DELETE FROM audit_logs WHERE org_id = $1`, a.org); err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("owner delete without purge marker: %v", err)
	}
	// Tombstones are not readable by the application.
	err = e.st.WithOrgTx(ctx, a.org, func(tx pgx.Tx) error { _, err := tx.Exec(ctx, `SELECT * FROM org_tombstones`); return err })
	if err == nil {
		t.Fatal("app_user must not read org_tombstones")
	}
}

func TestPGExportDeletePurgeIsolation(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	a := e.register(t, "alice@example.com", "Acme Uno")
	b := e.register(t, "bob@example.com", "Beta Dos")
	c := e.register(t, "carol@example.com", "Carol Tres")
	e.populate(t, a)
	e.populate(t, b)
	// Carol is also a member of A (and keeps her own organization C).
	if _, err := e.auth.AddMember(ctx, principal(a), c.email, auth.RoleMember); err != nil {
		t.Fatal(err)
	}
	cInA, err := e.auth.SwitchOrg(ctx, principal(c), a.org, auth.ClientMeta{})
	must(t, err)

	before := e.rowsOf(t, a.org)
	populated := 0
	for _, n := range before {
		if n > 0 {
			populated++
		}
	}
	t.Logf("org A has rows in %d tables before the purge", populated)
	if populated < 40 {
		t.Fatalf("test data too thin: org A has rows in only %d tables", populated)
	}
	snapB := e.snapshot(t, b.org)

	// ---- export (real HTTP requests) ----
	if w := e.do(t, "GET", "/api/v1/org/export", cInA.AccessToken, nil); w.Code != http.StatusForbidden {
		t.Fatalf("member export: %d %s", w.Code, w.Body)
	}
	w := e.do(t, "GET", "/api/v1/org/export", a.sess.AccessToken, nil)
	if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "application/zip" {
		t.Fatalf("export: %d %s", w.Code, w.Body)
	}
	zipBytes := w.Body.Bytes()
	if w := e.do(t, "GET", "/api/v1/org/export", a.sess.AccessToken, nil); w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") == "" {
		t.Fatalf("second export within the hour: %d", w.Code)
	}
	e.checkExport(t, zipBytes, a, b, c)
	if n := e.count(t, `SELECT count(*) FROM audit_logs WHERE org_id = $1 AND action = 'org.exported'`, a.org); n != 1 {
		t.Fatalf("org.exported audit entries = %d", n)
	}

	// ---- deletion request (real HTTP requests) ----
	if w := e.do(t, "POST", "/api/v1/org/deletion", cInA.AccessToken, map[string]string{"confirm_name": "Acme Uno", "password": password}); w.Code != http.StatusForbidden {
		t.Fatalf("member deletion: %d", w.Code)
	}
	if w := e.do(t, "POST", "/api/v1/org/deletion", a.sess.AccessToken, map[string]string{"confirm_name": "Acme Uno", "password": "wrong-password-1"}); w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "invalid_password") {
		t.Fatalf("wrong password: %d %s", w.Code, w.Body)
	}
	if w := e.do(t, "POST", "/api/v1/org/deletion", a.sess.AccessToken, map[string]string{"confirm_name": "acme uno", "password": password}); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "name_mismatch") {
		t.Fatalf("wrong name: %d %s", w.Code, w.Body)
	}
	var st tenantdata.DeletionStatus
	w = e.do(t, "POST", "/api/v1/org/deletion", a.sess.AccessToken, map[string]string{"confirm_name": "Acme Uno", "password": password})
	if w.Code != http.StatusAccepted || json.Unmarshal(w.Body.Bytes(), &st) != nil || !st.Pending || st.DeletionAt == nil {
		t.Fatalf("schedule: %d %s", w.Code, w.Body)
	}
	if d := st.DeletionAt.Sub(st.Now); d < 7*24*time.Hour-time.Minute || d > 7*24*time.Hour+time.Minute {
		t.Fatalf("grace = %v, want 7 days", d)
	}
	// While pending everything keeps working; every member sees the banner state.
	w = e.do(t, "GET", "/api/v1/org/deletion", cInA.AccessToken, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"pending":true`) {
		t.Fatalf("status as member: %d %s", w.Code, w.Body)
	}
	if _, err := e.st.ListAgents(ctx, a.org); err != nil {
		t.Fatal(err)
	}
	// Not due yet: the janitor leaves it alone.
	res, err := e.janitor.RunOnce(ctx)
	must(t, err)
	if !res.Locked || len(res.PurgedOrgs) != 0 {
		t.Fatalf("janitor before the grace period: %+v", res)
	}
	if w := e.do(t, "DELETE", "/api/v1/org/deletion", a.sess.AccessToken, nil); w.Code != 200 || !strings.Contains(w.Body.String(), `"pending":false`) {
		t.Fatalf("cancel: %d %s", w.Code, w.Body)
	}
	if w := e.do(t, "POST", "/api/v1/org/deletion", a.sess.AccessToken, map[string]string{"confirm_name": "Acme Uno", "password": password}); w.Code != http.StatusAccepted {
		t.Fatalf("schedule again: %d %s", w.Code, w.Body)
	}
	for _, act := range []string{"org.deletion_scheduled", "org.deletion_cancelled", "org.deletion_denied"} {
		if n := e.count(t, `SELECT count(*) FROM audit_logs WHERE org_id = $1 AND action = $2`, a.org, act); n == 0 {
			t.Errorf("no %s audit entry", act)
		}
	}

	// ---- the grace period passes; the janitor purges A ----
	_, err = e.owner.Exec(ctx, `UPDATE organizations SET deletion_at = now() - interval '1 minute' WHERE id = $1`, a.org)
	must(t, err)
	res, err = e.janitor.RunOnce(ctx)
	must(t, err)
	if len(res.PurgedOrgs) != 1 || res.PurgedOrgs[0] != tenantdata.OrgHash(a.org) || res.Errors != 0 {
		t.Fatalf("janitor: %+v", res)
	}

	// EVERY table has zero rows of A.
	for n, k := range e.rowsOf(t, a.org) {
		if k != 0 {
			t.Errorf("after purge %s still has %d rows of org A", n, k)
		}
	}
	if n := e.count(t, `SELECT count(*) FROM users WHERE id = $1`, a.user); n != 0 {
		t.Error("A's owner account (no membership left) must be deleted")
	}
	for _, q := range []string{`SELECT count(*) FROM refresh_tokens WHERE user_id = $1`, `SELECT count(*) FROM email_verifications WHERE user_id = $1`, `SELECT count(*) FROM memberships WHERE user_id = $1`} {
		if n := e.count(t, q, a.user); n != 0 {
			t.Errorf("%s = %d", q, n)
		}
	}
	if n := e.count(t, `SELECT count(*) FROM users WHERE id = $1`, c.user); n != 1 {
		t.Error("Carol still belongs to her own organization: her account must stay")
	}
	if n := e.count(t, `SELECT count(*) FROM org_tombstones WHERE org_hash = $1 AND reason = 'requested'`, tenantdata.OrgHash(a.org)); n != 1 {
		t.Error("tombstone missing")
	}
	if n := e.count(t, `SELECT count(*) FROM org_tombstones WHERE org_hash LIKE '%' || $1 || '%'`, a.org); n != 0 {
		t.Error("the tombstone must not contain the organization id")
	}
	// B is byte-identical.
	after := e.snapshot(t, b.org)
	for n, v := range snapB {
		if after[n] != v {
			t.Errorf("org B changed in %s:\nbefore: %.300s\nafter:  %.300s", n, v, after[n])
		}
	}
	if len(snapB) != len(after) {
		t.Errorf("snapshot tables %d -> %d", len(snapB), len(after))
	}
	// A purge is idempotent and B keeps working.
	if _, err := e.purger.PurgeOrg(ctx, a.org, tenantdata.PurgeOptions{}); err != nil {
		t.Fatalf("second purge: %v", err)
	}
	if ags, err := e.st.ListAgents(ctx, b.org); err != nil || len(ags) == 0 {
		t.Fatalf("org B agents after A's purge: %d %v", len(ags), err)
	}
	if _, err := e.auth.Login(ctx, auth.LoginInput{Email: b.email, Password: password}, auth.ClientMeta{}); err != nil {
		t.Fatalf("B login after A's purge: %v", err)
	}
	if _, err := e.auth.Login(ctx, auth.LoginInput{Email: a.email, Password: password}, auth.ClientMeta{}); err == nil {
		t.Fatal("A's owner can still log in")
	}
}

// checkExport verifies the ZIP: every planned table present, only A's rows, no secret, README and MANIFEST.
func (e *env) checkExport(t *testing.T, zipBytes []byte, a, b, c tenant) {
	t.Helper()
	ctx := context.Background()
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	must(t, err)
	files := map[string][]byte{}
	var all bytes.Buffer
	for _, f := range zr.File {
		rc, err := f.Open()
		must(t, err)
		data, err := io.ReadAll(rc)
		rc.Close()
		must(t, err)
		files[f.Name] = data
		all.Write(data)
		all.WriteByte('\n')
	}
	for _, n := range []string{"README.txt", "MANIFEST.json", "users.jsonl", "requests.jsonl", "drive_objects.jsonl", "credentials.jsonl", "audit_logs.jsonl"} {
		if _, ok := files[n]; !ok {
			t.Errorf("export misses %s", n)
		}
	}
	sc, err := tenantdata.Discover(ctx, e.owner)
	must(t, err)
	for _, x := range sc.ExportPlan() {
		if _, ok := files[x.Name+".jsonl"]; !ok {
			t.Errorf("export misses %s.jsonl", x.Name)
		}
	}
	var man tenantdata.Manifest
	must(t, json.Unmarshal(files["MANIFEST.json"], &man))
	if man.Organization.ID != a.org || len(man.Tables) != len(sc.ExportPlan()) || man.Files == 0 {
		t.Errorf("manifest: org %s, %d tables, %d files", man.Organization.ID, len(man.Tables), man.Files)
	}
	if !strings.Contains(string(files["README.txt"]), "requests") || !strings.Contains(string(files["README.txt"]), "[REDACTED]") {
		t.Error("README must list the tables and explain the redaction")
	}
	// Every row with an org_id belongs to A; users are exactly A's members.
	for name, data := range files {
		if !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			if line == "" {
				continue
			}
			var row map[string]any
			if err := json.Unmarshal([]byte(line), &row); err != nil {
				t.Fatalf("%s: invalid json line: %v", name, err)
			}
			if v, ok := row["org_id"]; ok && v != a.org {
				t.Errorf("%s: row of org %v", name, v)
			}
		}
	}
	var emails []string
	for _, line := range strings.Split(strings.TrimSpace(string(files["users.jsonl"])), "\n") {
		var u map[string]any
		must(t, json.Unmarshal([]byte(line), &u))
		emails = append(emails, u["email"].(string))
		if u["password_hash"] != tenantdata.Redacted {
			t.Errorf("users.password_hash = %v", u["password_hash"])
		}
	}
	sort.Strings(emails)
	if strings.Join(emails, ",") != a.email+","+c.email {
		t.Errorf("exported users = %v", emails)
	}
	// File contents are in the ZIP.
	text := all.String()
	for _, want := range []string{"secret of " + a.org, "contenido de " + a.org, "contenido v2 de " + a.org, "%PDF-1.4 blob of " + a.org, a.email, tenantdata.Redacted} {
		if !strings.Contains(text, want) {
			t.Errorf("export misses %q", want)
		}
	}
	// Nothing of B.
	for _, bad := range []string{b.org, b.email, "secret of " + b.org, "contenido de " + b.org} {
		if strings.Contains(text, bad) {
			t.Errorf("export of A contains B data %q", bad)
		}
	}
	// No secret, in any encoding.
	secrets := []string{"SEALED-KEY-", "P256DH-", "PUSHAUTH-", "ENDPOINT-", "EVHASH-", "STATEHASH-", "PKCE-", "CANARY_PLAINTEXT_"}
	var strs []string
	collect := func(sql string, args ...any) {
		rows, err := e.owner.Query(ctx, sql, args...)
		must(t, err)
		defer rows.Close()
		for rows.Next() {
			var v []byte
			must(t, rows.Scan(&v))
			strs = append(strs, string(v), hex.EncodeToString(v), base64.StdEncoding.EncodeToString(v))
		}
	}
	collect(`SELECT convert_to(password_hash, 'UTF8') FROM users WHERE id = ANY($1)`, []string{a.user, c.user})
	collect(`SELECT convert_to(token_hash, 'UTF8') FROM refresh_tokens WHERE org_id = $1`, a.org)
	collect(`SELECT convert_to(token_hash, 'UTF8') FROM invitations WHERE org_id = $1`, a.org)
	collect(`SELECT ciphertext FROM credentials WHERE org_id = $1 AND ciphertext IS NOT NULL`, a.org)
	collect(`SELECT nonce FROM credentials WHERE org_id = $1 AND nonce IS NOT NULL`, a.org)
	collect(`SELECT wrapped_dek FROM org_data_keys WHERE org_id = $1`, a.org)
	if len(strs) < 3*6 {
		t.Fatalf("secret fixtures missing: %d", len(strs))
	}
	secrets = append(secrets, strs...)
	for _, s := range secrets {
		if len(s) >= 8 && strings.Contains(text, s) {
			t.Errorf("export leaks a secret: %.24s...", s)
		}
	}
}

func TestPGDemoOrganizationCannotBePurged(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	must(t, e.st.Seed(ctx, domain.SeedOrg(10), roles.SeedAgents()))
	must(t, e.st.CreateRequest(ctx, domain.DemoOrgID, domain.Request{ID: "rq-demo", Text: "demo", Status: domain.RequestRunning, CreatedAt: time.Now()}))
	beforeRows := e.rowsOf(t, domain.DemoOrgID)
	// Even with a due deletion written behind the API's back.
	_, err := e.owner.Exec(ctx, `UPDATE organizations SET pending_deletion_at = now(), deletion_at = now() - interval '1 day' WHERE id = $1`, domain.DemoOrgID)
	must(t, err)
	res, err := e.janitor.RunOnce(ctx)
	must(t, err)
	if len(res.PurgedOrgs) != 0 {
		t.Fatalf("janitor purged %v", res.PurgedOrgs)
	}
	if _, err := e.purger.PurgeOrg(ctx, domain.DemoOrgID, tenantdata.PurgeOptions{}); !errors.Is(err, tenantdata.ErrProtected) {
		t.Fatalf("PurgeOrg(demo) = %v", err)
	}
	if _, err := e.td.ScheduleDeletion(ctx, domain.DemoOrgID, "u", "Demo", password); !errors.Is(err, tenantdata.ErrProtected) {
		t.Fatalf("ScheduleDeletion(demo) = %v", err)
	}
	// The database refuses it too, whoever connects.
	if _, err := e.owner.Exec(ctx, `DELETE FROM organizations WHERE id = $1`, domain.DemoOrgID); err == nil || !strings.Contains(err.Error(), "demo organization") {
		t.Fatalf("raw delete of the demo org: %v", err)
	}
	after := e.rowsOf(t, domain.DemoOrgID)
	for n, k := range beforeRows {
		if after[n] != k {
			t.Errorf("demo %s: %d -> %d rows", n, k, after[n])
		}
	}
	if after["agents"] == 0 || after["organizations"] != 1 {
		t.Fatalf("demo data lost: %v", after)
	}
}

func TestPGJanitorPurgesExpiredTrials(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	trial := func() (user, org string) {
		ta, err := e.auth.CreateTrial(ctx, "es")
		must(t, err)
		must(t, e.owner.QueryRow(ctx, `SELECT u.id, m.org_id FROM users u JOIN memberships m ON m.user_id = u.id WHERE u.email = $1`, ta.Email).Scan(&user, &org))
		sess, err := e.auth.Login(ctx, auth.LoginInput{Email: ta.Email, Password: ta.Password}, auth.ClientMeta{})
		must(t, err)
		e.populate(t, tenant{sess: sess, org: org, user: user, email: ta.Email})
		return user, org
	}
	oldU, oldOrg := trial()
	newU, newOrg := trial()
	normal := e.register(t, "keep@example.com", "Keep")
	snap := e.snapshot(t, normal.org)
	_, err := e.owner.Exec(ctx, `UPDATE users SET trial_expires_at = now() - interval '8 days' WHERE id = $1`, oldU)
	must(t, err)
	_, err = e.owner.Exec(ctx, `UPDATE users SET trial_expires_at = now() - interval '3 days' WHERE id = $1`, newU)
	must(t, err)

	res, err := e.janitor.RunOnce(ctx)
	must(t, err)
	if res.Errors != 0 || len(res.PurgedOrgs) != 1 || res.PurgedOrgs[0] != tenantdata.OrgHash(oldOrg) || res.PurgedTrials != 1 {
		t.Fatalf("janitor: %+v", res)
	}
	for n, k := range e.rowsOf(t, oldOrg) {
		if k != 0 {
			t.Errorf("expired trial org: %s keeps %d rows", n, k)
		}
	}
	if n := e.count(t, `SELECT count(*) FROM users WHERE id = $1`, oldU); n != 0 {
		t.Error("expired trial account must be deleted")
	}
	if n := e.count(t, `SELECT count(*) FROM org_tombstones WHERE org_hash = $1 AND reason = 'trial_expired'`, tenantdata.OrgHash(oldOrg)); n != 1 {
		t.Error("trial tombstone missing")
	}
	// Expired 3 days ago (within the 7 days) and normal accounts stay.
	if n := e.count(t, `SELECT count(*) FROM users WHERE id = $1`, newU); n != 1 {
		t.Error("recent trial must stay")
	}
	if n := e.count(t, `SELECT count(*) FROM agents WHERE org_id = $1`, newOrg); n == 0 {
		t.Error("recent trial org must stay")
	}
	after := e.snapshot(t, normal.org)
	for n, v := range snap {
		if after[n] != v {
			t.Errorf("normal org changed in %s", n)
		}
	}
	// A second pass has nothing to do.
	res, err = e.janitor.RunOnce(ctx)
	must(t, err)
	if len(res.PurgedOrgs) != 0 || res.PurgedTrials != 0 {
		t.Fatalf("second pass: %+v", res)
	}
}

func TestPGJanitorRunsOnOneReplicaAtATime(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	conn, err := e.owner.Acquire(ctx)
	must(t, err)
	defer conn.Release()
	var got bool
	must(t, conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, int64(0x7E4A_0650)).Scan(&got))
	if !got {
		t.Fatal("could not take the janitor lock")
	}
	res, err := e.janitor.RunOnce(ctx)
	must(t, err)
	if res.Locked {
		t.Fatal("a second janitor must skip while another replica holds the lock")
	}
	_, _ = conn.Exec(ctx, `SELECT pg_advisory_unlock($1)`, int64(0x7E4A_0650))
	res, err = e.janitor.RunOnce(ctx)
	must(t, err)
	if !res.Locked {
		t.Fatal("janitor must run once the lock is free")
	}
}

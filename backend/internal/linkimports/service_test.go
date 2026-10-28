package linkimports_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/drive"
	"aiworkforce/backend/internal/linkimports"
	"aiworkforce/backend/internal/objstore"
	"aiworkforce/backend/internal/vault"
)

type rtFunc func(*http.Request) (*http.Response, error)

func (f rtFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type fakeCustomers map[string]string // id -> folder ("" = archived)

func (f fakeCustomers) CustomerFolder(_ context.Context, _, id string) (string, error) {
	p, ok := f[id]
	if !ok {
		return "", domain.ErrNotFound
	}
	if p == "" {
		return "", domain.ErrConflict
	}
	return p, nil
}

type auditRec struct {
	org, actor, action, id string
	d                      map[string]any
}

type env struct {
	svc    *linkimports.Service
	drive  *drive.Service
	store  *linkimports.MemStore
	ctx    context.Context
	now    time.Time
	mu     sync.Mutex
	body   string // what every link serves
	ctype  string
	gate   error
	audits []auditRec
	events []string
	reqs   []*http.Request
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{ctx: application.WithOrg(context.Background(), "o1"), store: linkimports.NewMemStore(), body: "id,total\n1,10\n", ctype: "text/csv",
		now: time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)}
	e.drive = drive.New(drive.Config{Store: drive.NewMemStore(), Objects: objstore.NewMemory(), OrgID: "o1"})
	kw, err := vault.NewEnvKeyWrapper(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)))
	if err != nil {
		t.Fatal(err)
	}
	fetcher := &linkimports.Fetcher{Transport: rtFunc(func(r *http.Request) (*http.Response, error) {
		e.mu.Lock()
		e.reqs = append(e.reqs, r)
		body, ct := e.body, e.ctype
		e.mu.Unlock()
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {ct}}, Body: io.NopCloser(strings.NewReader(body)),
			ContentLength: int64(len(body))}, nil
	})}
	e.svc = linkimports.New(linkimports.Config{Store: e.store, Drive: e.drive, Fetcher: fetcher, OrgID: "o1",
		Customers: fakeCustomers{"c1": "/Clients/Acme", "old": ""},
		Sealer:    vault.New(vault.NewMemRepo(), kw),
		Now:       func() time.Time { return e.now },
		Gate:      func(context.Context, string) error { return e.gate },
		Audit: func(_ context.Context, org, actor, action, id string, d map[string]any) {
			e.mu.Lock()
			e.audits = append(e.audits, auditRec{org, actor, action, id, d})
			e.mu.Unlock()
		},
		Emit: func(_ context.Context, org, typ string, _ map[string]any) {
			e.mu.Lock()
			e.events = append(e.events, org+"|"+typ)
			e.mu.Unlock()
		}})
	return e
}

func (e *env) create(t *testing.T, in linkimports.Input) linkimports.View {
	t.Helper()
	v, err := e.svc.Create(e.ctx, "u_admin", in)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	return v
}

func (e *env) run(t *testing.T, id string) linkimports.Run {
	t.Helper()
	r, err := e.svc.RunNow(e.ctx, "u_admin", id)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	return r
}

func TestRunWritesAVersionOnlyWhenTheContentChanged(t *testing.T) {
	e := newEnv(t)
	v := e.create(t, linkimports.Input{Name: "Ventas diarias", URL: "https://sheets.example.com/pub?output=csv"})
	if v.Folder != "/Shared/Imports/Links/ventas-diarias" || v.Schedule != linkimports.ScheduleOnce || v.NextRunAt != nil {
		t.Fatalf("defaults: %+v", v)
	}
	r := e.run(t, v.ID)
	if r.Status != linkimports.RunOK || r.Path != "/Shared/Imports/Links/ventas-diarias/ventas-diarias.csv" || r.Version != 1 {
		t.Fatalf("first run: %+v", r)
	}
	f, err := e.drive.FileByPath(e.ctx, r.Path)
	if err != nil {
		t.Fatal(err)
	}
	if f.CreatedBy != drive.LinkImportP(v.ID) || f.UpdatedBy.Kind != drive.PrincipalLinkImport {
		t.Fatalf("the file is attributed to the link import principal (tainted for agents): %+v", f.CreatedBy)
	}
	r2 := e.run(t, v.ID)
	if r2.Status != linkimports.RunUnchanged || r2.Version != 1 {
		t.Fatalf("same content: %+v", r2)
	}
	if f, _ := e.drive.FileByPath(e.ctx, r.Path); f.CurrentVersion != 1 {
		t.Fatalf("no new version for unchanged content: %d", f.CurrentVersion)
	}
	e.body = "id,total\n1,10\n2,20\n"
	r3 := e.run(t, v.ID)
	if r3.Status != linkimports.RunOK || r3.Version != 2 {
		t.Fatalf("changed content: %+v", r3)
	}
	runs, _ := e.svc.Runs(e.ctx, v.ID, 10)
	if len(runs) != 3 || runs[0].ID != r3.ID || runs[1].Status != linkimports.RunUnchanged {
		t.Fatalf("history newest first: %+v", runs)
	}
	if n := strings.Count(strings.Join(e.events, ","), linkimports.EventDataLanded); n != 2 {
		t.Fatalf("an event only when new data landed: %v", e.events)
	}
	got, _ := e.svc.Get(e.ctx, v.ID)
	if got.LastStatus != linkimports.RunOK || got.LastVersion != 2 || got.RunCount != 3 || got.LastDataAt == nil {
		t.Fatalf("summary: %+v", got)
	}
	for _, a := range e.audits {
		b, _ := json.Marshal(a.d)
		if strings.Contains(string(b), "output=csv") || strings.Contains(string(b), "1,10") {
			t.Fatalf("the audit carries metadata only (no query string, no content): %s %s", a.action, b)
		}
	}
}

func TestSealedHeaderIsNeverReturned(t *testing.T) {
	e := newEnv(t)
	v := e.create(t, linkimports.Input{Name: "ERP", URL: "https://erp.example.com/export.json", Schedule: "daily",
		Header: &linkimports.HeaderInput{Name: "x-api-key", Value: "super-secret-value-9876"}})
	b, _ := json.Marshal(v)
	if strings.Contains(string(b), "super-secret") || !v.HeaderSet || v.HeaderHint != "9876" || v.HeaderName != "X-Api-Key" {
		t.Fatalf("view: %s", b)
	}
	stored, _ := e.store.Get(e.ctx, "o1", v.ID)
	if len(stored.HeaderSealed) == 0 || bytes.Contains(stored.HeaderSealed, []byte("super-secret")) {
		t.Fatal("the header is stored sealed")
	}
	list, _ := e.svc.List(e.ctx)
	b, _ = json.Marshal(list)
	if strings.Contains(string(b), "super-secret") || strings.Contains(string(b), "header_sealed") {
		t.Fatalf("list: %s", b)
	}
	e.ctype, e.body = "application/json", `[{"id":1}]`
	if r := e.run(t, v.ID); r.Status != linkimports.RunOK {
		t.Fatalf("%+v", r)
	}
	if got := e.reqs[len(e.reqs)-1].Header.Get("X-Api-Key"); got != "super-secret-value-9876" {
		t.Fatalf("the header is sent: %q", got)
	}
	// Renaming keeps the sealed value; clearing removes it.
	v2, err := e.svc.Update(e.ctx, "u_admin", v.ID, linkimports.Input{Header: &linkimports.HeaderInput{Name: "Authorization"}})
	if err != nil || !v2.HeaderSet || v2.HeaderName != "Authorization" || v2.HeaderHint != "9876" {
		t.Fatalf("rename: %+v %v", v2, err)
	}
	v3, err := e.svc.Update(e.ctx, "u_admin", v.ID, linkimports.Input{Header: &linkimports.HeaderInput{Clear: true}})
	if err != nil || v3.HeaderSet || v3.HeaderName != "" {
		t.Fatalf("clear: %+v %v", v3, err)
	}
	for _, a := range e.audits {
		b, _ := json.Marshal(a.d)
		if strings.Contains(string(b), "super-secret") {
			t.Fatal("never in the audit")
		}
	}
	for _, bad := range []string{"Host", "Content-Length", "Bad Name", "Proxy-Authorization"} {
		if _, err := e.svc.Create(e.ctx, "u", linkimports.Input{Name: "x" + bad, URL: "https://a.example.com/x.csv",
			Header: &linkimports.HeaderInput{Name: bad, Value: "v"}}); !errors.Is(err, domain.ErrInvalid) {
			t.Fatalf("%s: %v", bad, err)
		}
	}
	if _, err := e.svc.Create(e.ctx, "u", linkimports.Input{Name: "crlf", URL: "https://a.example.com/x.csv",
		Header: &linkimports.HeaderInput{Name: "X-Key", Value: "a\r\nX-Evil: 1"}}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("header injection: %v", err)
	}
}

func TestHeaderNeedsTheVault(t *testing.T) {
	e := newEnv(t)
	svc := linkimports.New(linkimports.Config{Store: e.store, Drive: e.drive, OrgID: "o1"})
	_, err := svc.Create(e.ctx, "u", linkimports.Input{Name: "x", URL: "https://a.example.com/x.csv", Header: &linkimports.HeaderInput{Name: "X-Key", Value: "abc"}})
	if !errors.Is(err, domain.ErrInvalid) || !strings.Contains(err.Error(), "CONNECTIONS_KEK") {
		t.Fatalf("no KEK, no header: %v", err)
	}
}

func TestCreateValidatesTheLink(t *testing.T) {
	e := newEnv(t)
	for _, u := range []string{"http://a.example.com/x.csv", "https://127.0.0.1/x.csv", "https://localhost/x", "javascript:alert(1)", "", "https://a.example.com:9000/x"} {
		if _, err := e.svc.Create(e.ctx, "u", linkimports.Input{Name: "x", URL: u}); !errors.Is(err, domain.ErrInvalid) {
			t.Fatalf("%q: %v", u, err)
		}
	}
	if _, err := e.svc.Create(e.ctx, "u", linkimports.Input{Name: "x", URL: "https://a.example.com/x.csv", Folder: "/Agents/a1"}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("an agent desk is not a destination: %v", err)
	}
	if _, err := e.svc.Create(e.ctx, "u", linkimports.Input{Name: "x", URL: "https://a.example.com/x.csv", Schedule: "every-second"}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("schedule: %v", err)
	}
}

func TestReadOnlyAndKillSwitchSkipScheduledRunsAndRecordWhy(t *testing.T) {
	e := newEnv(t)
	v := e.create(t, linkimports.Input{Name: "Hourly feed", URL: "https://feed.example.com/data.json", Schedule: "hourly"})
	if v.NextRunAt == nil || !v.NextRunAt.Equal(e.now) {
		t.Fatalf("a scheduled import is due at once: %+v", v.NextRunAt)
	}
	e.gate = linkimports.ErrReadOnly
	e.svc.RunDue(context.Background(), []string{"o1"})
	runs, _ := e.svc.Runs(e.ctx, v.ID, 10)
	if len(runs) != 1 || runs[0].Status != linkimports.RunSkipped || runs[0].Code != linkimports.CodeReadOnly || runs[0].Trigger != linkimports.TriggerSchedule {
		t.Fatalf("skipped and recorded: %+v", runs)
	}
	if len(e.reqs) != 0 {
		t.Fatal("nothing is downloaded in read-only mode")
	}
	// The period was consumed: the next tick in the same hour does nothing.
	e.svc.RunDue(context.Background(), []string{"o1"})
	if runs, _ := e.svc.Runs(e.ctx, v.ID, 10); len(runs) != 1 {
		t.Fatalf("one skipped run per period: %d", len(runs))
	}
	e.now = e.now.Add(time.Hour)
	e.gate = linkimports.ErrKillSwitch
	e.svc.RunDue(context.Background(), []string{"o1"})
	runs, _ = e.svc.Runs(e.ctx, v.ID, 10)
	if len(runs) != 2 || runs[0].Code != linkimports.CodeKillSwitch {
		t.Fatalf("kill switch: %+v", runs)
	}
	e.gate = errors.New("controls unreadable")
	if r := e.run(t, v.ID); r.Status != linkimports.RunSkipped || r.Code != linkimports.CodeReadOnly {
		t.Fatalf("fail closed: %+v", r)
	}
	skipped := 0
	for _, a := range e.audits {
		if a.action == "link_import.run_skipped" {
			skipped++
		}
	}
	if skipped != 3 {
		t.Fatalf("every skipped run is audited: %d", skipped)
	}
	e.gate = nil
	e.now = e.now.Add(time.Hour)
	e.svc.RunDue(context.Background(), []string{"o1"})
	runs, _ = e.svc.Runs(e.ctx, v.ID, 10)
	if runs[0].Status != linkimports.RunOK || runs[0].Trigger != linkimports.TriggerSchedule {
		t.Fatalf("back to normal: %+v", runs[0])
	}
	got, _ := e.svc.Get(e.ctx, v.ID)
	if got.NextRunAt == nil || !got.NextRunAt.Equal(e.now.Add(time.Hour)) {
		t.Fatalf("next period: %v", got.NextRunAt)
	}
	// A paused import is not run by the scheduler.
	off := false
	if _, err := e.svc.Update(e.ctx, "u", v.ID, linkimports.Input{Enabled: &off}); err != nil {
		t.Fatal(err)
	}
	e.now = e.now.Add(2 * time.Hour)
	e.svc.RunDue(context.Background(), []string{"o1"})
	if runs2, _ := e.svc.Runs(e.ctx, v.ID, 10); len(runs2) != len(runs) {
		t.Fatal("paused imports do not run")
	}
}

func TestCrossOrgIsolation(t *testing.T) {
	e := newEnv(t)
	v := e.create(t, linkimports.Input{Name: "Orders", URL: "https://a.example.com/orders.csv", Schedule: "daily"})
	other := application.WithOrg(context.Background(), "o2")
	if _, err := e.svc.Get(other, v.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("o2 cannot see o1's import: %v", err)
	}
	if _, err := e.svc.RunNow(other, "u", v.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("nor run it: %v", err)
	}
	if _, err := e.svc.Runs(other, v.ID, 5); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("nor read its history: %v", err)
	}
	if err := e.svc.Delete(other, "u", v.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("nor delete it: %v", err)
	}
	if l, _ := e.svc.List(other); len(l) != 0 {
		t.Fatal("o2 lists nothing")
	}
	e.svc.RunDue(context.Background(), []string{"o2"})
	if len(e.reqs) != 0 {
		t.Fatal("o2's scheduler tick never runs o1's imports")
	}
}

func TestCustomerScoping(t *testing.T) {
	e := newEnv(t)
	v := e.create(t, linkimports.Input{Name: "Acme stock", URL: "https://acme.example.com/stock.csv", CustomerID: "c1"})
	if v.Folder != "Imports/Links/acme-stock" || v.FolderPath != "/Clients/Acme/Imports/Links/acme-stock" {
		t.Fatalf("relative to the customer's folder: %+v", v)
	}
	r := e.run(t, v.ID)
	if r.Status != linkimports.RunOK || !strings.HasPrefix(r.Path, "/Clients/Acme/Imports/Links/acme-stock/") {
		t.Fatalf("%+v", r)
	}
	for _, f := range []string{"/Shared/x", "/Clients/Beta/x", "../../Beta"} {
		if _, err := e.svc.Create(e.ctx, "u", linkimports.Input{Name: "bad" + f, URL: "https://a.example.com/x.csv", CustomerID: "c1", Folder: f}); !errors.Is(err, domain.ErrInvalid) {
			t.Fatalf("%s must stay inside the customer's folder: %v", f, err)
		}
	}
	if _, err := e.svc.Create(e.ctx, "u", linkimports.Input{Name: "y", URL: "https://a.example.com/x.csv", CustomerID: "old"}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("archived customer: %v", err)
	}
}

func TestArchivedCustomerRunFails(t *testing.T) {
	e := newEnv(t)
	cust := fakeCustomers{"c1": "/Clients/Acme"}
	svc := linkimports.New(linkimports.Config{Store: e.store, Drive: e.drive, OrgID: "o1", Customers: cust,
		Fetcher: &linkimports.Fetcher{Transport: rtFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/csv"}}, Body: io.NopCloser(strings.NewReader("a\n1\n"))}, nil
		})}})
	v, err := svc.Create(e.ctx, "u", linkimports.Input{Name: "Acme", URL: "https://a.example.com/x.csv", CustomerID: "c1"})
	if err != nil {
		t.Fatal(err)
	}
	cust["c1"] = ""
	r, err := svc.RunNow(e.ctx, "u", v.ID)
	if err != nil || r.Status != linkimports.RunError || r.Code != linkimports.CodeCustomerUnavailable {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestNeverOverwritesAPersonsFile(t *testing.T) {
	e := newEnv(t)
	v := e.create(t, linkimports.Input{Name: "Report", URL: "https://a.example.com/report.csv"})
	if _, err := e.drive.Write(e.ctx, drive.Human("u_ana"), drive.WriteInput{Path: "/Shared/Imports/Links/report/report.csv", Data: []byte("mine\n")}); err != nil {
		t.Fatal(err)
	}
	r := e.run(t, v.ID)
	if r.Status != linkimports.RunOK || r.Path == "/Shared/Imports/Links/report/report.csv" || !strings.HasSuffix(r.Path, "-"+v.ID[len(v.ID)-6:]+".csv") {
		t.Fatalf("a suffixed name instead of the person's file: %+v", r)
	}
	f, _ := e.drive.FileByPath(e.ctx, "/Shared/Imports/Links/report/report.csv")
	if f.CurrentVersion != 1 || f.UpdatedBy != drive.Human("u_ana") {
		t.Fatal("the person's file is untouched")
	}
}

func TestErrorsAreRecordedRuns(t *testing.T) {
	e := newEnv(t)
	v := e.create(t, linkimports.Input{Name: "Page", URL: "https://a.example.com/sheet"})
	e.ctype, e.body = "text/html", "<html>sign in</html>"
	r := e.run(t, v.ID)
	if r.Status != linkimports.RunError || r.Code != linkimports.CodeWebPage {
		t.Fatalf("%+v", r)
	}
	// The content does not match the announced type: the drive refuses it.
	e.ctype, e.body = "application/pdf", "not a pdf"
	if r := e.run(t, v.ID); r.Status != linkimports.RunError || r.Code != linkimports.CodeContentMismatch {
		t.Fatalf("%+v", r)
	}
	got, _ := e.svc.Get(e.ctx, v.ID)
	if got.LastStatus != linkimports.RunError || got.LastHash != "" {
		t.Fatalf("%+v", got)
	}
}

func TestDeleteKeepsTheFiles(t *testing.T) {
	e := newEnv(t)
	v := e.create(t, linkimports.Input{Name: "Keep", URL: "https://a.example.com/k.csv"})
	r := e.run(t, v.ID)
	if err := e.svc.Delete(e.ctx, "u", v.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Get(e.ctx, v.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := e.drive.FileByPath(e.ctx, r.Path); err != nil {
		t.Fatalf("the imported file stays: %v", err)
	}
}

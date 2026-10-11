package drive_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/drive"
	"aiworkforce/backend/internal/drive/drivetest"
	"aiworkforce/backend/internal/objstore"
)

type auditLog struct {
	mu sync.Mutex
	es []drive.AuditEntry
}

func (a *auditLog) add(_ context.Context, _ string, e drive.AuditEntry) {
	a.mu.Lock()
	a.es = append(a.es, e)
	a.mu.Unlock()
}

func (a *auditLog) has(action string, actor drive.Principal) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, e := range a.es {
		if e.Action == action && e.Actor == actor {
			return true
		}
	}
	return false
}

func newSvc(lim drive.Limits) (*drive.Service, *auditLog, *objstore.Memory) {
	al := &auditLog{}
	mem := objstore.NewMemory()
	return drive.New(drive.Config{Store: drive.NewMemStore(), Objects: mem, OrgID: "o1", Limits: lim, Audit: al.add}), al, mem
}

func TestMemorySuite(t *testing.T) {
	svc, _, _ := newSvc(drive.Limits{})
	drivetest.Run(t, svc, "orgA", "orgB", drivetest.Options{})
}

func TestPathValidation(t *testing.T) {
	ok := []string{"/Shared/a.txt", "/Projects/Plan Q1/notas.md", "/Shared/ñandú/año.csv"}
	for _, p := range ok {
		if _, err := drive.Segments(p); err != nil {
			t.Fatalf("%q: %v", p, err)
		}
	}
	bad := []string{"", "Shared/a", "/Shared/../etc/passwd", "/Shared/./a", "/Shared//a", "/Shared/a\\b", "/Shared/a\x00b", "/Shared/ a",
		"/Shared/a ", "/Shared/a.", "/Shared/" + strings.Repeat("x", 121), "/" + strings.Repeat("a/", 13) + "z", "/Shared/a:b", "/Shared/a|b",
		"/Shared/‮b", "/Shared/​hidden", "/Shared/..", "/Shared/...", "/Shared/\nx"}
	for _, p := range bad {
		if _, err := drive.Segments(p); err == nil {
			t.Fatalf("%q accepted", p)
		}
	}
	long := "/" + strings.Repeat(strings.Repeat("d", 100)+"/", 10)
	if _, err := drive.Segments(long); err == nil {
		t.Fatal("over-long path accepted")
	}
	// literal percent-encoding is just a name, never decoded
	if s, err := drive.Segments("/Shared/%2e%2e"); err != nil || s[1] != "%2e%2e" {
		t.Fatalf("%v %v", s, err)
	}
	// NFC normalization: composed and decomposed forms are the same name
	a, _ := drive.CleanPath("/Shared/é.txt")
	b, _ := drive.CleanPath("/Shared/é.txt")
	if a != b {
		t.Fatalf("not normalized: %q %q", a, b)
	}
}

func TestWriteRules(t *testing.T) {
	svc, _, _ := newSvc(drive.Limits{})
	ctx := application.WithOrg(context.Background(), "o1")
	h := drive.Human("u")
	cases := []struct {
		path string
		data string
		code string
	}{
		{"/Elsewhere/a.txt", "x", "root_not_allowed"},
		{"/a.txt", "x", "path_invalid"},
		{"/Shared/run.exe", "MZ", "unsupported_type"},
		{"/Shared/page.html", "<script>", "unsupported_type"},
		{"/Shared/pic.svg", "<svg/>", "unsupported_type"},
		{"/Shared/a.zip", "PK", "unsupported_type"},
		{"/Shared/noext", "x", "unsupported_type"},
		{"/Shared/fake.pdf", "not a pdf", "content_mismatch"},
		{"/Shared/fake.png", "GIF89a", "content_mismatch"},
		{"/Shared/fake.docx", "PK\x03\x04junk", "content_mismatch"},
		{"/Shared/bin.txt", "a\x00b", "content_mismatch"},
		{"/Shared/bad.md", "\xff\xfe\xfd", "content_mismatch"},
		{"/Shared/empty.txt", "", "empty_file"},
	}
	for _, c := range cases {
		_, err := svc.Write(ctx, h, drive.WriteInput{Path: c.path, Data: []byte(c.data)})
		var e *drive.Error
		if !errors.As(err, &e) || e.Code != c.code {
			t.Fatalf("%s: want %s got %v", c.path, c.code, err)
		}
	}
	if _, err := svc.Write(ctx, drive.Principal{}, drive.WriteInput{Path: "/Shared/a.txt", Data: []byte("x")}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("principal required: %v", err)
	}
	if _, err := svc.Write(ctx, h, drive.WriteInput{Path: "/Shared/ok.txt", Data: []byte("x"), Expected: intp(3)}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("expected version on a new file: %v", err)
	}
}

func intp(n int) *int { return &n }

func TestSizeAndQuota(t *testing.T) {
	svc, _, mem := newSvc(drive.Limits{MaxFileBytes: 100, QuotaBytes: 250})
	ctx := application.WithOrg(context.Background(), "o1")
	h := drive.Human("u")
	if _, err := svc.Write(ctx, h, drive.WriteInput{Path: "/Shared/big.txt", Data: []byte(strings.Repeat("x", 101))}); !errors.Is(err, drive.ErrTooLarge) {
		t.Fatalf("max file size: %v", err)
	}
	for i, n := range []string{"a", "b"} {
		if _, err := svc.Write(ctx, h, drive.WriteInput{Path: "/Shared/" + n + ".txt", Data: []byte(strings.Repeat(n, 100))}); err != nil {
			t.Fatalf("file %d: %v", i, err)
		}
	}
	// 200 used; the 100 byte file would exceed the 250 quota. Versions count too.
	_, err := svc.Write(ctx, h, drive.WriteInput{Path: "/Shared/c.txt", Data: []byte(strings.Repeat("c", 100))})
	if !errors.Is(err, drive.ErrQuota) {
		t.Fatalf("quota: %v", err)
	}
	before := mem.Len()
	_, err = svc.Write(ctx, h, drive.WriteInput{Path: "/Shared/a.txt", Data: []byte(strings.Repeat("z", 100))})
	if !errors.Is(err, drive.ErrQuota) {
		t.Fatalf("new versions count against the quota: %v", err)
	}
	if mem.Len() != before {
		t.Fatal("a refused write must not leave objects behind")
	}
	u, _ := svc.Usage(ctx)
	if u.UsedBytes != 200 || u.QuotaBytes != 250 {
		t.Fatalf("usage: %+v", u)
	}
	// quotas are per organization
	other := application.WithOrg(context.Background(), "o2")
	if _, err := svc.Write(other, h, drive.WriteInput{Path: "/Shared/a.txt", Data: []byte(strings.Repeat("a", 100))}); err != nil {
		t.Fatalf("another org shares the quota: %v", err)
	}
}

func TestReadOnlyModeBlocksEveryWrite(t *testing.T) {
	ro := false
	al := &auditLog{}
	svc := drive.New(drive.Config{Store: drive.NewMemStore(), Objects: objstore.NewMemory(), OrgID: "o1", Audit: al.add,
		WriteGate: func(context.Context, string) error {
			if ro {
				return drive.ErrReadOnly
			}
			return nil
		}})
	ctx := application.WithOrg(context.Background(), "o1")
	h := drive.Human("u")
	w, err := svc.Write(ctx, h, drive.WriteInput{Path: "/Shared/a.txt", Data: []byte("a")})
	if err != nil {
		t.Fatal(err)
	}
	f, _ := svc.Mkdir(ctx, h, "/Shared/sub")
	ro = true
	checks := map[string]error{}
	_, checks["write"] = svc.Write(ctx, h, drive.WriteInput{Path: "/Shared/b.txt", Data: []byte("b")})
	_, checks["version"] = svc.NewVersion(ctx, h, w.File.ID, []byte("z"), "", nil, "t")
	_, checks["mkdir"] = svc.Mkdir(ctx, h, "/Shared/new")
	_, checks["move"] = svc.MoveFile(ctx, h, w.File.ID, "/Shared/c.txt")
	checks["delete"] = svc.DeleteFile(ctx, h, w.File.ID)
	_, checks["restore"] = svc.RestoreFile(ctx, h, w.File.ID)
	_, checks["restore_version"] = svc.RestoreVersion(ctx, h, w.File.ID, 1, nil)
	_, checks["move_folder"] = svc.MoveFolder(ctx, h, f.ID, "/Shared/sub2")
	checks["delete_folder"] = svc.DeleteFolder(ctx, h, f.ID)
	_, checks["grant"] = svc.AddGrant(ctx, h, "sales", "/Clients/x")
	_, checks["tags"] = svc.SetTags(ctx, h, w.File.ID, []string{"a"})
	for name, err := range checks {
		if !errors.Is(err, drive.ErrReadOnly) {
			t.Fatalf("%s was not blocked by read-only mode: %v", name, err)
		}
	}
	// reads keep working
	if _, err := svc.GetFile(ctx, w.File.ID); err != nil {
		t.Fatal(err)
	}
	if rc, _, err := svc.Open(ctx, w.File, 0); err != nil {
		t.Fatal(err)
	} else {
		rc.Close()
	}
	// the gate failing (controls unreadable) also refuses: fail closed
	svc2 := drive.New(drive.Config{Store: drive.NewMemStore(), Objects: objstore.NewMemory(), OrgID: "o1",
		WriteGate: func(context.Context, string) error { return errors.New("controls down") }})
	if _, err := svc2.Write(ctx, h, drive.WriteInput{Path: "/Shared/a.txt", Data: []byte("a")}); !errors.Is(err, drive.ErrReadOnly) {
		t.Fatalf("fail closed: %v", err)
	}
}

func TestAuditEveryWrite(t *testing.T) {
	svc, al, _ := newSvc(drive.Limits{})
	ctx := application.WithOrg(context.Background(), "o1")
	h, ag := drive.Human("u"), drive.AgentP("sales")
	w, _ := svc.Write(ctx, h, drive.WriteInput{Path: "/Shared/a.txt", Data: []byte("a"), Source: "upload"})
	_, _ = svc.NewVersion(ctx, ag, w.File.ID, []byte("b"), "m", nil, "agent_tool")
	_, _ = svc.MoveFile(ctx, ag, w.File.ID, "/Shared/b.txt")
	_ = svc.DeleteFile(ctx, ag, w.File.ID)
	_, _ = svc.RestoreFile(ctx, h, w.File.ID)
	rc, _, _ := svc.Download(ctx, h, w.File, 0)
	rc.Close()
	for _, c := range []struct {
		a string
		p drive.Principal
	}{{"drive.folder.created", h}, {"drive.file.created", h}, {"drive.file.version_created", ag}, {"drive.file.moved", ag},
		{"drive.file.deleted", ag}, {"drive.file.restored", h}, {"drive.file.downloaded", h}} {
		if !al.has(c.a, c.p) {
			t.Fatalf("missing audit %s by %v: %+v", c.a, c.p, al.es)
		}
	}
	for _, e := range al.es { // metadata only: never the content
		for _, v := range e.Details {
			if s, ok := v.(string); ok && (s == "a" || s == "b") {
				t.Fatalf("content in audit: %+v", e)
			}
		}
	}
}

func TestConcurrentWritersOnlyOneWins(t *testing.T) {
	svc, _, _ := newSvc(drive.Limits{})
	ctx := application.WithOrg(context.Background(), "o1")
	h := drive.Human("u")
	w, _ := svc.Write(ctx, h, drive.WriteInput{Path: "/Shared/c.txt", Data: []byte("base")})
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins, conflicts := 0, 0
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := svc.NewVersion(ctx, h, w.File.ID, []byte(strings.Repeat("x", i+2)), "", intp(1), "t")
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				wins++
			} else if errors.Is(err, domain.ErrConflict) {
				conflicts++
			}
		}(i)
	}
	wg.Wait()
	if wins != 1 || conflicts != 7 {
		t.Fatalf("optimistic locking: %d wins, %d conflicts", wins, conflicts)
	}
}

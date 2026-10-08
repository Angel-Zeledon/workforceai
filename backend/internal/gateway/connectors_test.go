package gateway

import (
	"context"
	"strings"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/connections"
	"aiworkforce/backend/internal/connections/calendar"
	"aiworkforce/backend/internal/connections/drive"
	"aiworkforce/backend/internal/connections/gmail"
)

// Connector tests: every new provider goes through the same gateway as Gmail,
// so reads come back as delimited untrusted data (injection samples flagged,
// never obeyed), writes always need a human and are held before they run, and
// resource allowlists cannot be widened by arguments.

type connectors struct {
	*env
	cal *calendar.Provider
	drv *drive.Provider
}

func newConnectors(t *testing.T) *connectors {
	t.Helper()
	e := newEnv(t, gmail.Config{})
	cal, err := calendar.New(calendar.Config{})
	if err != nil {
		t.Fatal(err)
	}
	drv, err := drive.New(drive.Config{})
	if err != nil {
		t.Fatal(err)
	}
	e.cs.Providers["google_calendar"], e.cs.Providers["google_drive"] = cal, drv
	return &connectors{env: e, cal: cal, drv: drv}
}

func (c *connectors) conn(provider, label string, scope connections.ResourceScope, caps ...string) connections.Connection {
	c.t.Helper()
	conn, err := c.cs.Create(context.Background(), org, connections.CreateInput{Provider: provider, Label: label, Capabilities: caps,
		Mode: connections.ModeSimulated, ResourceScope: scope, Actor: "u1"})
	if err != nil {
		c.t.Fatal(err)
	}
	c.grant(conn.ID, "assistant", connections.GrantInput{Capabilities: caps})
	return conn
}

// readsAreUntrustedData checks the common read contract of a connector.
func readsAreUntrustedData(t *testing.T, out application.GatewayOutcome, wantInjection bool) {
	t.Helper()
	if out.Decision != "allowed" || out.Status != "succeeded" || len(out.Blocks) == 0 || !out.Tainted {
		t.Fatalf("read outcome %+v", out)
	}
	for _, b := range out.Blocks {
		if !strings.HasPrefix(b, "<untrusted_data ") || !strings.Contains(b, `trust="external_untrusted"`) {
			t.Fatalf("block not delimited: %s", b)
		}
	}
	if out.InjectionSuspected != wantInjection {
		t.Fatalf("injection suspected = %v, want %v", out.InjectionSuspected, wantInjection)
	}
}

// writeNeedsApprovalThenHold checks the common write contract: the first call
// only produces an approval; the approved call is held, then runs once.
func (c *connectors) writeNeedsApprovalThenHold(cl application.GatewayCall, created func() int) application.GatewayOutcome {
	c.t.Helper()
	out := c.exec(cl)
	if out.Decision != "needs_approval" || out.Pending == nil || !contains(out.Pending.Flags, "always_approval") {
		c.t.Fatalf("write without approval: %+v", out)
	}
	if created() != 0 {
		c.t.Fatal("a write ran before any human approved it")
	}
	cl.ApprovedArgsHash, cl.ApprovalID = out.Pending.ArgsHash, "ap-"+cl.Tool
	sched := c.exec(cl)
	if sched.Decision != "allowed" || sched.Status != "scheduled" || sched.HoldID == "" {
		c.t.Fatalf("approved write must be held first: %+v", sched)
	}
	if created() != 0 {
		c.t.Fatal("the held write ran before its window ended")
	}
	c.clk.Add(2 * time.Minute)
	if n := c.g.ProcessDue(context.Background()); n != 1 || created() != 1 {
		c.t.Fatalf("hold processed %d, created %d", n, created())
	}
	return out
}

// ---- Google Calendar ----

func TestCalendarReadsAreDelimitedAndFlagInjection(t *testing.T) {
	c := newConnectors(t)
	c.conn("google_calendar", "Agenda", connections.ResourceScope{}, "calendar.read")
	out := c.exec(call("calendar", "list_events", map[string]any{}))
	readsAreUntrustedData(t, out, true)
	if len(out.Blocks) != 3 {
		t.Fatalf("events = %d", len(out.Blocks))
	}
	one := c.exec(call("calendar", "read_event", map[string]any{"event_id": "ev-001"}))
	readsAreUntrustedData(t, one, false)
}

func TestCalendarAllowlistCannotBeWidened(t *testing.T) {
	c := newConnectors(t)
	c.conn("google_calendar", "Agenda", connections.ResourceScope{}, "calendar.read")
	// Without a filter only "primary" is reachable (least privilege).
	out := c.exec(call("calendar", "list_events", map[string]any{"calendar_id": "board@group.calendar.google.com"}))
	if out.Decision == "allowed" && out.Status == "succeeded" {
		t.Fatalf("a calendar outside the allowlist was read: %+v", out)
	}
	if out.DenyReason != connections.CodeOutOfScope {
		t.Fatalf("deny reason = %q", out.DenyReason)
	}
}

func TestCalendarCreateEventNeedsApprovalAndHold(t *testing.T) {
	c := newConnectors(t)
	conn := c.conn("google_calendar", "Agenda escritura", connections.ResourceScope{}, "calendar.create_event")
	fake := c.cal.FakeFor(conn.ID)
	start := time.Date(2026, 10, 9, 15, 0, 0, 0, time.UTC)
	args := map[string]any{"title": "Revisión con Acme", "start": start.Format(time.RFC3339), "end": start.Add(time.Hour).Format(time.RFC3339),
		"attendees": []any{"laura@acme.com"}}
	out := c.writeNeedsApprovalThenHold(call("calendar", "create_event", args), func() int { return len(fake.CreatedEvents()) })
	if len(out.Pending.Recipients) != 1 || out.Pending.Recipients[0] != "laura@acme.com" {
		t.Fatalf("attendees must appear as recipients on the approval card: %+v", out.Pending)
	}
	if !strings.Contains(out.Pending.Details, "Revisión con Acme") {
		t.Fatalf("the card must show the event title: %s", out.Pending.Details)
	}
}

func TestCalendarReadConnectionCannotCreate(t *testing.T) {
	c := newConnectors(t)
	conn := c.conn("google_calendar", "Agenda", connections.ResourceScope{}, "calendar.read")
	out := c.exec(call("calendar", "create_event", map[string]any{"title": "x", "start": "2026-10-09T10:00:00Z", "end": "2026-10-09T11:00:00Z"}))
	if out.Decision != "denied" || out.DenyReason != connections.CodeScopeNotGranted {
		t.Fatalf("create on a read connection: %+v", out)
	}
	if len(c.cal.FakeFor(conn.ID).CreatedEvents()) != 0 {
		t.Fatal("event created through a read connection")
	}
}

// ---- Google Drive (read-only) ----

func TestDriveReadsOnlyAllowedFoldersAsData(t *testing.T) {
	c := newConnectors(t)
	c.conn("google_drive", "Drive clientes", connections.ResourceScope{Folders: []string{"fld-clientes"}}, "drive.read")
	list := c.exec(call("drive", "search", map[string]any{}))
	readsAreUntrustedData(t, list, false)
	if len(list.Blocks) != 2 {
		t.Fatalf("files = %d, want only the 2 of the allowed folder", len(list.Blocks))
	}
	doc := c.exec(call("drive", "read", map[string]any{"file_id": "doc-002"}))
	readsAreUntrustedData(t, doc, true) // the hostile document is flagged, never obeyed
	if !strings.Contains(doc.Blocks[0], "exfil@evil.com") && !strings.Contains(doc.Blocks[0], "redacted") {
		t.Fatalf("the document text must reach the runtime only inside the data block: %s", doc.Blocks[0])
	}
	for _, args := range []map[string]any{{"file_id": "doc-900"}, {"folder_id": "fld-privado"}} {
		action := "read"
		if _, ok := args["folder_id"]; ok {
			action = "list"
		}
		out := c.exec(call("drive", action, args))
		if out.DenyReason != connections.CodeOutOfScope {
			t.Fatalf("%s %v outside the allowlist: %+v", action, args, out)
		}
	}
}

func TestDriveWithoutFoldersReadsNothing(t *testing.T) {
	c := newConnectors(t)
	c.conn("google_drive", "Drive", connections.ResourceScope{}, "drive.read")
	if out := c.exec(call("drive", "search", map[string]any{"q": "propuesta"})); out.DenyReason != connections.CodeOutOfScope {
		t.Fatalf("an empty folder allowlist must fail closed: %+v", out)
	}
}

func TestDriveHasNoWriteTools(t *testing.T) {
	c := newConnectors(t)
	c.conn("google_drive", "Drive", connections.ResourceScope{Folders: []string{"fld-clientes"}}, "drive.read")
	for _, action := range []string{"create_file", "share", "delete"} {
		if c.g.IsSideEffect("drive", action) == false {
			t.Fatalf("drive.%s must count as a side effect", action)
		}
		if out := c.exec(call("drive", action, map[string]any{"file_id": "doc-001"})); out.Decision != "denied" {
			t.Fatalf("drive.%s must be denied: %+v", action, out)
		}
	}
}

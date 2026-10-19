package meetings

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestExtractSpanishNotes(t *testing.T) {
	ex := ExtractMinutes(`Reunión de ventas

Acuerdos:
- Subir el precio 5% en enero
- Congelar la contratación

Tareas:
- Marcos: preparar el reporte de rrhh para el 2026-11-05
- @elena revisar el contrato (legal)

Acción: Tomás enviará la factura
Decidimos posponer el lanzamiento.`)
	if len(ex.Decisions) != 3 {
		t.Fatalf("decisions: %v", ex.Decisions)
	}
	if ex.Decisions[2] != "Decidimos posponer el lanzamiento." {
		t.Fatalf("phrase decision keeps the sentence: %q", ex.Decisions[2])
	}
	if len(ex.Actions) != 3 {
		t.Fatalf("actions: %+v", ex.Actions)
	}
	if a := ex.Actions[0]; a.Owner != "Marcos" || a.Due != "2026-11-05" || a.Text != "preparar el reporte de rrhh" {
		t.Fatalf("name: prefix + due date: %+v", a)
	}
	if a := ex.Actions[1]; a.Owner != "elena" && a.Owner != "legal" {
		t.Fatalf("owner: %+v", a)
	}
}

func TestExtractPortugueseAndEnglishLabels(t *testing.T) {
	ex := ExtractMinutes("Decisões:\n- Aprovar o orçamento\nAções:\n- Eu: enviar a proposta até 2026-12-01\nTODO: renew the domain\nAction item: call the bank\nDecision: use Postgres\nactionable insight, not an item")
	if len(ex.Decisions) != 2 || len(ex.Actions) != 3 {
		t.Fatalf("%+v", ex)
	}
	for _, a := range ex.Actions {
		if strings.Contains(a.Text, "actionable") {
			t.Fatalf("the word-boundary rule must stop 'actionable': %+v", a)
		}
	}
}

func TestExtractTranscriptCommitments(t *testing.T) {
	notes := `[00:01] Ana: Let's start with the budget.
[00:02] Bob: We decided to keep the current vendor.
[00:03] Ana: I will send the revised budget tomorrow.
[00:04] Bob: Voy a llamar al banco el lunes.
[00:05] Ana: Thanks everyone.
[00:06] Carlos: Eu vou preparar a ata.`
	msgs, transcript := SplitNotes(notes)
	if !transcript || len(msgs) != 6 || msgs[0][0] != "Ana" {
		t.Fatalf("transcript detection: %v %v", transcript, msgs)
	}
	ex := ExtractMinutes(notes)
	if len(ex.Decisions) != 1 || !strings.Contains(ex.Decisions[0], "keep the current vendor") {
		t.Fatalf("decisions: %+v", ex.Decisions)
	}
	if len(ex.Actions) != 3 || ex.Actions[0].Owner != "Ana" || ex.Actions[1].Owner != "Bob" || ex.Actions[2].Owner != "Carlos" {
		t.Fatalf("first-person commitments are owned by the speaker: %+v", ex.Actions)
	}
}

func TestExtractIgnoresProseAndCapsItems(t *testing.T) {
	if ex := ExtractMinutes("We talked about many things. todo el equipo estuvo de acuerdo con el plan.\nThe action plan was long."); len(ex.Actions) != 0 {
		t.Fatalf("'todo el equipo' is Spanish prose, not a TODO: %+v", ex.Actions)
	}
	var b strings.Builder
	b.WriteString("Action items:\n")
	for i := 0; i < 120; i++ {
		b.WriteString("- task number ")
		b.WriteString(strings.Repeat("x", i%7+1))
		b.WriteString(string(rune('a' + i%26)))
		b.WriteString(strings.Repeat("y", i/26))
		b.WriteString("\n")
	}
	if ex := ExtractMinutes(b.String()); len(ex.Actions) > maxItems {
		t.Fatalf("items are capped at %d, got %d", maxItems, len(ex.Actions))
	}
	long := "Decision: " + strings.Repeat("a", 900)
	if ex := ExtractMinutes(long); len([]rune(ex.Decisions[0])) > maxItemChar {
		t.Fatal("item text is capped")
	}
}

func TestMatchAttendee(t *testing.T) {
	att := []Attendee{{Kind: KindPerson, ID: "u1", Name: "Ana Pérez"}, {Kind: KindPerson, ID: "u2", Name: "Ana López"}, {Kind: KindAgent, ID: "legal", Name: "Elena Castro"}}
	if a, ok := MatchAttendee("elena", att); !ok || a.ID != "legal" {
		t.Fatalf("first name: %+v", a)
	}
	if _, ok := MatchAttendee("Ana", att); ok {
		t.Fatal("an ambiguous first name must not match")
	}
	if a, ok := MatchAttendee("ana pérez", att); !ok || a.ID != "u1" {
		t.Fatalf("full name, accents ignored: %+v", a)
	}
	if a, ok := MatchAttendee("ANA PEREZ", att); !ok || a.ID != "u1" {
		t.Fatalf("%+v", a)
	}
	if _, ok := MatchAttendee("", att); ok {
		t.Fatal("empty")
	}
}

func TestLocalSummarySkipsItemSections(t *testing.T) {
	s := LocalSummary("Reviewed Q4 numbers and the launch plan.\nRevenue is up.\nDecisions:\n- ship it\nAction items:\n- do x")
	if !strings.Contains(s, "Reviewed Q4") || strings.Contains(s, "ship it") || strings.Contains(s, "do x") {
		t.Fatalf("%q", s)
	}
}

func TestRenderMarkdownEscapesThirdPartyText(t *testing.T) {
	m := Meeting{ID: "mt_abcdefghijklmnop", Title: "<script>alert(1)</script>", Start: time.Date(2026, 10, 20, 15, 0, 0, 0, time.UTC)}
	md := RenderMarkdown(m, Minutes{Lang: "pt", Summary: "line1\nline2 <b>x</b>", Decisions: []string{"a\n# injected heading"}, CreatedAt: m.Start})
	if strings.Contains(md, "<script>") || strings.Contains(md, "<b>") || strings.Contains(md, "\n# injected") {
		t.Fatalf("markdown must neutralize html and heading injection:\n%s", md)
	}
	if !strings.Contains(md, "# Ata da reunião") || !strings.Contains(md, "Decisões") {
		t.Fatalf("pt headings:\n%s", md)
	}
	if name := FileName(m); name != "2026-10-20 script-alert-1-script (ijklmnop).md" && !strings.HasSuffix(name, "(ijklmnop).md") {
		t.Fatalf("file name: %q", name)
	}
}

func TestLinkValidation(t *testing.T) {
	ok := map[string]string{
		ProviderMeet:  "https://meet.google.com/abc-defg-hij",
		ProviderZoom:  "https://acme.zoom.us/j/12345?pwd=x",
		ProviderTeams: "https://teams.microsoft.com/l/meetup-join/19%3a",
	}
	for p, l := range ok {
		if _, err := ValidateLink(p, l, ""); err != nil {
			t.Errorf("%s %s: %v", p, l, err)
		}
	}
	bad := [][2]string{{ProviderMeet, "https://meet.google.com.evil.io/x"}, {ProviderMeet, "javascript:alert(1)"}, {ProviderZoom, "https://zoom.us@evil.com/j/1"},
		{ProviderTeams, "ftp://teams.microsoft.com/x"}, {ProviderNone, "https://x.com"}, {ProviderCustom, "//x.com"}, {ProviderJitsi, "https://meet.jit.si/room"}}
	for _, c := range bad {
		if _, err := ValidateLink(c[0], c[1], "https://jitsi.example.org"); !errors.Is(err, ErrInvalid) {
			t.Errorf("%v must be refused: %v", c, err)
		}
	}
	if !Embeddable(ProviderJitsi, "https://jitsi.example.org/r-1", "https://jitsi.example.org") || Embeddable(ProviderZoom, "https://zoom.us/j/1", "") {
		t.Error("only Jitsi on the configured base is embeddable")
	}
}

func TestFeedTokenAndICS(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	mine := r.create(t, CreateInput{Title: "Mine", Attendees: []Attendee{{Kind: KindPerson, ID: "u1", Name: "Ana", Email: "ana@x.com"}, {Kind: KindAgent, ID: "sales"}}})
	r.now = base // "now" == meeting start; the feed window covers it
	_, err := r.svc.Store.GetMeeting(ctx, "org", mine.ID)
	if err != nil {
		t.Fatal(err)
	}
	other, err := r.svc.Create(ctx, "org", "someone-else", CreateInput{Title: "Not mine", Start: base.Add(2 * time.Hour), End: base.Add(3 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	cancelled, _ := r.svc.Create(ctx, "org", "ana", CreateInput{Title: "Dropped", Start: base.Add(4 * time.Hour), End: base.Add(5 * time.Hour)})
	if _, err := r.svc.Cancel(ctx, "org", "ana", cancelled.ID); err != nil {
		t.Fatal(err)
	}

	token, feed, err := r.svc.CreateFeed(ctx, "org", "ana", "Phone")
	if err != nil || !strings.HasPrefix(token, "org.") || len(token) < 40 {
		t.Fatalf("%q %v", token, err)
	}
	if feed.TokenHash == "" || strings.Contains(feed.TokenHash, token) {
		t.Fatal("only a hash is stored")
	}
	ics, err := r.svc.FeedICS(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ics, "SUMMARY:Mine") || !strings.Contains(ics, "STATUS:CANCELLED") || !strings.Contains(ics, "SUMMARY:Dropped") ||
		strings.Contains(ics, "Not mine") || !strings.Contains(ics, "ATTENDEE;CN=\"Ana\";ROLE=REQ-PARTICIPANT:mailto:ana@x.com") ||
		!strings.Contains(ics, "X-WR-CALNAME:Meetings - Phone") || !strings.Contains(ics, "URL:https://jitsi.example.org/") {
		t.Fatalf("feed content:\n%s", ics)
	}
	if strings.Contains(ics, "mailto:sales") {
		t.Fatal("agents have no e-mail")
	}
	_ = other

	// A wrong secret, another org's token and a malformed token all look the same: not found.
	for _, bad := range []string{"org.wrongsecretwrongsecretwrongsecretwrongsecret", "other." + strings.SplitN(token, ".", 2)[1], "nodot", ".x", "org.", ""} {
		if _, err := r.svc.FeedICS(ctx, bad); !errors.Is(err, ErrNotFound) {
			t.Errorf("token %q: %v", bad, err)
		}
	}
	// Revocation kills the URL at once and only its owner can revoke.
	if err := r.svc.RevokeFeed(ctx, "org", "mallory", feed.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoking someone else's feed: %v", err)
	}
	if err := r.svc.RevokeFeed(ctx, "org", "ana", feed.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.FeedICS(ctx, token); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked feed must stop working: %v", err)
	}
	if fs, _ := r.svc.ListFeeds(ctx, "org", "ana"); len(fs) != 0 {
		t.Fatal("revoked feeds are not listed")
	}
}

func TestFeedLimitAndSingleMeetingExport(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	for i := 0; i < maxFeeds; i++ {
		if _, _, err := r.svc.CreateFeed(ctx, "org", "ana", ""); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := r.svc.CreateFeed(ctx, "org", "ana", ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("feed cap: %v", err)
	}
	m := r.create(t, CreateInput{Title: "Board, Q4; plan"})
	body, name, err := r.svc.ExportICS(ctx, "org", m.ID)
	if err != nil || name != "meeting-board-q4-plan.ics" || !strings.Contains(body, `SUMMARY:Board\, Q4\; plan`) || !strings.HasSuffix(body, "END:VCALENDAR\r\n") {
		t.Fatalf("%q %q %v", name, body, err)
	}
}

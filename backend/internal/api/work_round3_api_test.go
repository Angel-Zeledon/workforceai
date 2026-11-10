package api_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"aiworkforce/backend/internal/api"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/infrastructure/memory"
)

// legacyLegal seeds the 1:1 history the organization shared with legal before private threads existed
// (agent:legal, no owner): the newest chat messages of the organization.
func (p *privateWork) legacyLegal(t *testing.T) {
	t.Helper()
	ctx, org, at := context.Background(), p.owner.OrgID, time.Now().UTC().Add(time.Hour)
	st := p.e.store
	if err := st.CreateConversation(ctx, org, domain.Conversation{ID: "agent:legal", Title: "legal", Participants: []string{"user", "legal"}, LastMessageAt: at}); err != nil {
		t.Fatal(err)
	}
	for i, m := range []domain.Message{
		{ID: "leg-1", From: "user", To: "legal", Text: "historial antiguo secreto: el despido de Pérez", TurnID: "TL1", AuthorID: p.e.userID(p.ana)},
		{ID: "leg-2", From: "legal", To: "user", Text: "respuesta antigua secreta sobre el despido", TurnID: "TL1"},
	} {
		m.ConversationID, m.Kind, m.TS = "agent:legal", domain.MsgChat, at.Add(time.Duration(i)*time.Second)
		if err := st.AddMessage(ctx, org, m); err != nil {
			t.Fatal(err)
		}
	}
}

type threadEntry struct {
	Key          string `json:"key"`
	Preview      string `json:"preview"`
	MessageCount int    `json:"message_count"`
	Unread       int    `json:"unread"`
}

func threadEntries(t *testing.T, raw string) map[string]threadEntry {
	t.Helper()
	var ts []threadEntry
	if err := json.Unmarshal([]byte(raw), &ts); err != nil {
		t.Fatal(err)
	}
	out := map[string]threadEntry{}
	for _, x := range ts {
		out[x.Key] = x
	}
	return out
}

// Ronda 3, hallazgo 1: the thread list does not summarize the earlier shared history of a private office's agent
// (preview, count, unread) for whoever does not see its work: not in the agent's entry (beto uses it through the
// directory) nor in General.
func TestThreadListLeavesOutTheSharedLegacyOfPrivateAgents(t *testing.T) {
	p := newPrivateWork(t)
	p.legacyLegal(t)

	ana := threadEntries(t, p.body("GET", "/chats/threads", p.ana, 200))
	if l := ana["legal"]; l.MessageCount != 2 || !strings.Contains(l.Preview, "secreta") {
		t.Fatalf("ana's legal thread = %+v", l)
	}
	check := func(when string) {
		t.Helper()
		got := threadEntries(t, p.body("GET", "/chats/threads", p.beto, 200))
		for key, e := range got {
			if strings.Contains(e.Preview, "secret") {
				t.Fatalf("%s: beto's %s thread previews the shared history: %+v", when, key, e)
			}
		}
		if l, ok := got["legal"]; ok && (l.MessageCount != 0 || l.Unread != 0) {
			t.Fatalf("%s: beto's legal thread counts the shared history: %+v", when, l)
		}
		if body := p.body("GET", "/chats/threads/general/messages", p.beto, 200); strings.Contains(body, "secret") {
			t.Fatalf("%s: beto's General shows the shared history: %s", when, body)
		}
	}
	check("office agent")
	p.e.json("PUT", api1+"/agents/legal/access", p.ana, map[string]any{"visibility": "directory"}, 200)
	got := threadEntries(t, p.body("GET", "/chats/threads", p.beto, 200))
	if _, ok := got["legal"]; !ok {
		t.Fatalf("beto uses legal through the directory: its thread must be listed: %v", got)
	}
	check("directory agent")
}

// withConvContext lets /compact store its summary (the orchestrator's conversation context).
func withConvContext(o *opts) {
	prev := o.deps
	o.deps = func(d *api.Deps) {
		prev(d)
		d.Orch.SetConvContext(d.Store.(*orgSpy).Store.(*memory.Store))
	}
}

func withSkills(o *opts) {
	o.skills = true
	prev := o.deps
	o.deps = func(d *api.Deps) {
		if prev != nil {
			prev(d)
		}
		if d.Offices != nil && d.Skills != nil {
			d.Skills.SetOffices(d.Offices.Model) // D3: a department's skills follow its visibility
		}
	}
}

// cmdRaw runs a command and returns the status code and body.
func (p *privateWork) cmdRaw(token string, body map[string]any) (int, string) {
	p.e.t.Helper()
	resp, raw := p.e.do("POST", api1+"/commands/run", token, body, nil)
	return resp.StatusCode, string(raw)
}

// Ronda 3, hallazgo 2: /learn, /save-template and /compact do not read the work of a private office's agent.
func TestRemainingCommandsLeaveOutPrivateWork(t *testing.T) {
	p := newPrivateWork(t, withSkills, withConvContext)
	ctx, org, later := context.Background(), p.owner.OrgID, time.Now().UTC().Add(time.Hour)
	st := p.e.store
	if err := st.CreateRequest(ctx, org, domain.Request{ID: "req-legal-new", Text: "pedido secreto de legal", Status: domain.RequestDone, CreatedAt: later}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateTask(ctx, org, domain.Task{ID: "task-legal-new", RequestID: "req-legal-new", Title: "tarea secreta de legal", AgentID: "legal",
		Status: domain.TaskDone, DependsOn: []string{}, CreatedAt: later, FinishedAt: &later, Output: &domain.StructuredOutput{Summary: "resumen secreto de legal"}}); err != nil {
		t.Fatal(err)
	}
	p.legacyLegal(t)
	office := map[string]any{"surface": "office"}

	// /learn naming legal answers like an unknown agent; without an agent it learns from the newest visible task
	code, body := p.cmdRaw(p.beto, map[string]any{"command": "learn", "args": map[string]any{"agent": "legal"}, "context": office})
	if code == 200 || !strings.Contains(body, "unknown_agent") {
		t.Fatalf("beto /learn @legal = %d %s", code, body)
	}
	if got := p.command(p.beto, map[string]any{"command": "learn", "context": office}); strings.Contains(got, "legal") || !strings.Contains(got, "task-sales") {
		t.Fatalf("beto /learn = %s", got)
	}
	if got := p.command(p.ana, map[string]any{"command": "learn", "args": map[string]any{"agent": "legal"}, "context": office}); !strings.Contains(got, "task-legal-new") {
		t.Fatalf("ana /learn @legal = %s", got)
	}
	// what legal's agent taught stays in Legal: the skill belongs to the department and beto does not see it
	hasLearned := func(token string) (found bool, office string) {
		for _, s := range p.e.list(api1+"/skills", token) {
			if strings.Contains(str(s, "name"), "secreta") {
				return true, str(s, "office_id")
			}
		}
		return false, ""
	}
	if found, office := hasLearned(p.ana); !found || office == "" {
		t.Fatalf("ana's learned skill: found=%v office=%q", found, office)
	}
	if found, _ := hasLearned(p.beto); found {
		t.Fatal("the skill learned from a private department's task leaked to beto")
	}

	// /save-template without a project: the newest request the person sees
	if got := p.command(p.beto, map[string]any{"command": "save-template", "context": office}); strings.Contains(got, "secreto") || !strings.Contains(got, "pedido sales") {
		t.Fatalf("beto /save-template = %s", got)
	}
	if got := p.command(p.ana, map[string]any{"command": "save-template", "context": office}); !strings.Contains(got, "pedido secreto de legal") {
		t.Fatalf("ana /save-template = %s", got)
	}

	// /compact on the chat with legal: beto neither uses it nor has a thread with it
	compact := map[string]any{"command": "compact", "context": map[string]any{"surface": "office", "conversation_id": "agent:legal"}}
	if code, body := p.cmdRaw(p.beto, compact); code == 200 || !strings.Contains(body, "unknown_agent") {
		t.Fatalf("beto /compact agent:legal = %d %s", code, body)
	}
	// through the directory beto may use legal: his own thread only, never the earlier shared history
	p.e.json("PUT", api1+"/agents/legal/access", p.ana, map[string]any{"visibility": "directory"}, 200)
	if code, body := p.cmdRaw(p.beto, compact); code == 200 || strings.Contains(body, "secret") {
		t.Fatalf("beto /compact agent:legal (directory) = %d %s", code, body)
	}
}

// Ronda 3, hallazgo 3: a template copies every node whole (title, agent, criteria), so a project with work the person
// does not see cannot be saved as a template by them (403); a project that is only that work answers 404.
func TestProjectTemplateNeedsTheWholeProject(t *testing.T) {
	p := newPrivateWork(t)
	only := p.project(t, "solo legal", func(int) string { return "legal" })
	mixed := p.project(t, "mixto", func(i int) string {
		if i == 0 {
			return "legal"
		}
		return "sales"
	})
	general := p.project(t, "solo ventas", func(int) string { return "sales" })

	p.body("POST", "/projects/"+mixed+"/save-as-template", p.beto, 403)
	p.body("POST", "/projects/"+only+"/save-as-template", p.beto, 404)
	p.body("POST", "/projects/"+general+"/save-as-template", p.beto, 201)
	p.body("POST", "/projects/"+mixed+"/save-as-template", p.ana, 201)

	// the same through /save-template with the project in context
	run := func(project string) (int, string) {
		return p.cmdRaw(p.beto, map[string]any{"command": "save-template", "context": map[string]any{"surface": "office", "project_id": project}})
	}
	if code, body := run(mixed); code != 403 {
		t.Fatalf("beto /save-template on the mixed project = %d %s", code, body)
	}
	if code, body := run(only); code != 404 {
		t.Fatalf("beto /save-template on legal's project = %d %s", code, body)
	}
	if code, body := run(general); code != 200 {
		t.Fatalf("beto /save-template on sales' project = %d %s", code, body)
	}
}

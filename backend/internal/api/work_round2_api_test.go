package api_test

import (
	"strings"
	"testing"
)

// project creates a draft from the generic template and gives each agent node the agent agentOf(i) (i counts the
// agent nodes in order). It returns the project id.
func (p *privateWork) project(t *testing.T, goal string, agentOf func(i int) string) string {
	t.Helper()
	id := str(p.e.json("POST", api1+"/projects/draft", p.owner.AccessToken, map[string]any{"goal": goal, "template_id": "tpl-generic"}, 201), "project_id")
	d := p.e.json("GET", api1+"/projects/"+id, p.owner.AccessToken, nil, 200)
	ops, i := []map[string]any{}, 0
	for _, x := range d["nodes"].([]any) {
		n := x.(map[string]any)
		if a, _ := n["agent_id"].(string); a == "" {
			continue
		}
		ops = append(ops, map[string]any{"op": "update", "id": n["id"], "fields": map[string]any{"agent_id": agentOf(i)}})
		i++
	}
	if i == 0 {
		t.Fatalf("template without agent nodes: %v", d)
	}
	p.e.json("PATCH", api1+"/projects/"+id+"/plan", p.owner.AccessToken, map[string]any{"ops": ops}, 200)
	return id
}

// Ronda 2: a project that is only a private office's work is left out of the list and its detail routes; a mixed
// one stays, redacted.
func TestProjectsListLeavesOutPrivateProjects(t *testing.T) {
	p := newPrivateWork(t)
	only := p.project(t, "solo legal", func(int) string { return "legal" })
	general := p.project(t, "solo ventas", func(int) string { return "sales" })
	mixed := p.project(t, "mixto", func(i int) string {
		if i == 0 {
			return "legal"
		}
		return "sales"
	})
	if got := p.body("GET", "/projects", p.beto, 200); strings.Contains(got, only) || !strings.Contains(got, general) || !strings.Contains(got, mixed) {
		t.Fatalf("beto's projects = %s", got)
	}
	if got := p.body("GET", "/projects", p.ana, 200); !strings.Contains(got, only) || !strings.Contains(got, mixed) {
		t.Fatalf("ana's projects = %s", got)
	}
	for _, path := range []string{"", "/snapshot", "/health", "/plan-changes", "/approvals", "/workspace"} {
		p.body("GET", "/projects/"+only+path, p.beto, 404)
		if c := p.e.status("GET", api1+"/projects/"+only+path, p.ana, nil); c == 404 {
			t.Fatalf("ana: GET /projects/%s%s = 404", only, path)
		}
	}
	if got := p.body("GET", "/projects/"+mixed, p.beto, 200); strings.Contains(got, `"legal"`) {
		t.Fatalf("beto's mixed project shows legal's node: %s", got)
	}
	p.body("GET", "/projects/"+mixed+"/health", p.beto, 200)
	p.body("GET", "/projects/"+mixed+"/plan-changes", p.beto, 200)
}

func TestQualityAndTrustOverviewsLeaveOutPrivateAgents(t *testing.T) {
	p := newPrivateWork(t)
	got := p.body("GET", "/quality/agents", p.beto, 200)
	if strings.Contains(got, `"agent_id":"legal"`) || !strings.Contains(got, `"agent_id":"sales"`) {
		t.Fatalf("beto's quality overview = %.400s", got)
	}
	if got := p.body("GET", "/quality/agents", p.ana, 200); !strings.Contains(got, `"agent_id":"legal"`) {
		t.Fatalf("ana's quality overview lacks legal: %.400s", got)
	}
	if got := p.body("GET", "/trust/agents", p.beto, 200); strings.Contains(got, `"agent_id":"legal"`) || !strings.Contains(got, `"agent_id":"sales"`) {
		t.Fatalf("beto's trust overview = %.400s", got)
	}
	if got := p.body("GET", "/trust/agents", p.ana, 200); !strings.Contains(got, `"agent_id":"legal"`) {
		t.Fatalf("ana's trust overview lacks legal: %.400s", got)
	}
	// a golden case of legal: by its own id it answers like a missing one
	c := p.e.json("POST", api1+"/agents/legal/golden-cases", p.ana, map[string]any{"name": "caso secreto", "input": "revisa el contrato",
		"checks": []map[string]any{{"kind": "contains", "value": "cláusula"}}}, 201)
	id := str(c, "id")
	for _, x := range []struct{ method, path string }{
		{"GET", "/golden-cases/" + id + "/runs"}, {"PUT", "/golden-cases/" + id}, {"DELETE", "/golden-cases/" + id}, {"POST", "/golden-cases/" + id + "/runs"},
	} {
		if code := p.e.status(x.method, api1+x.path, p.beto, map[string]any{"name": "x", "output": "x"}); code != 404 {
			t.Fatalf("beto %s %s = %d, want 404", x.method, x.path, code)
		}
	}
	p.body("GET", "/golden-cases/"+id+"/runs", p.ana, 200)
}

// Ronda 2 decision: a person's own messages with an agent they can no longer use stay readable to them (read only).
func TestOwnThreadStaysReadableAfterLosingTheAgent(t *testing.T) {
	p := newPrivateWork(t)
	p.e.json("PUT", api1+"/agents/legal/access", p.ana, map[string]any{"visibility": "directory"}, 200)
	if c := p.e.status("POST", api1+"/messages", p.beto, map[string]string{"conversation": "agent:legal", "text": "consulta propia de beto"}); c != 202 {
		t.Fatalf("beto chats with the directory agent = %d", c)
	}
	p.e.json("PUT", api1+"/agents/legal/access", p.ana, map[string]any{"visibility": "office"}, 200)

	if c := p.e.status("POST", api1+"/messages", p.beto, map[string]string{"conversation": "agent:legal", "text": "otra"}); c != 403 {
		t.Fatalf("beto still sends = %d", c)
	}
	if keys := threadKeys(t, p.body("GET", "/chats/threads", p.beto, 200)); !has(keys, "legal") {
		t.Fatalf("beto's threads = %v", keys)
	}
	for _, path := range []string{"/chats/threads/legal/messages", "/conversations/agent:legal/messages"} {
		if got := p.body("GET", path, p.beto, 200); !strings.Contains(got, "consulta propia de beto") {
			t.Fatalf("beto: GET %s = %s", path, got)
		}
	}
	if got := p.body("GET", "/conversations", p.beto, 200); !strings.Contains(got, `"agent:legal"`) {
		t.Fatalf("beto's conversations = %s", got)
	}
	// the rest of the office's work stays hidden
	p.body("GET", "/agents/legal/memory", p.beto, 404)
	p.body("GET", "/conversations/conv-legal/messages", p.beto, 404)
}

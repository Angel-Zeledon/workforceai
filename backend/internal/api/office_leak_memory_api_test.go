package api_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"aiworkforce/backend/internal/api"
	"aiworkforce/backend/internal/domain"
	longmem "aiworkforce/backend/internal/memory"
)

// What an agent remembers for a private office (a long-term document of the office scope) is the office's: the
// Memory tab, its search and its delete answer like it does not exist to who is not a member (an admin included).
func TestOfficeLeakAgentMemoryAPI(t *testing.T) {
	e, owner, ana, marta, luis, _, soporte, org, docs := officesWorldSealedDocs(t)
	e.json("PUT", api1+"/offices/"+soporte, owner, map[string]any{"visibility": "private"}, 200)
	now := time.Now().UTC()
	for _, d := range []longmem.Doc{
		{ID: "d-secret", Kind: longmem.KindTask, Ref: "task:t-secret", Title: "SECRETO-memoria", Body: "SECRETO-memoria cuerpo", AgentID: "assistant", Scope: longmem.ScopeOffice, OfficeID: soporte},
		{ID: "d-open", Kind: longmem.KindTask, Ref: "task:t-open", Title: "ABIERTO-memoria", Body: "ABIERTO-memoria cuerpo", AgentID: "assistant", Scope: longmem.ScopeOrg},
	} {
		d.CreatedAt, d.UpdatedAt = now, now
		if err := docs.Upsert(context.Background(), org, d); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []string{"/agents/assistant/memory", "/agents/assistant/memory?q=memoria"} {
		for who, tok := range map[string]string{"admin outside": owner, "other office": ana, "nobody": luis} {
			_, raw := e.do("GET", api1+p, tok, nil, nil)
			if strings.Contains(string(raw), "SECRETO") || !strings.Contains(string(raw), "ABIERTO-memoria") {
				t.Fatalf("%s: GET %s = %.400s", who, p, raw)
			}
			if resp, _ := e.do("DELETE", api1+"/agents/assistant/memory/d-secret", tok, nil, nil); resp.StatusCode != 404 {
				t.Fatalf("%s: DELETE of the office's memory = %d", who, resp.StatusCode)
			}
		}
		// (a search is the agent's own reading: a General agent never recalls an office's document, so only the list is checked)
		if _, raw := e.do("GET", api1+p, marta, nil, nil); !strings.Contains(p, "?q=") && !strings.Contains(string(raw), "SECRETO-memoria") {
			t.Fatalf("the member: GET %s lacks the office's memory: %.400s", p, raw)
		}
	}
	if d, err := docs.Get(context.Background(), org, "d-secret"); err != nil || d.Suppressed {
		t.Fatalf("a refused delete still removed the memory: %v", err)
	}
	if resp, _ := e.do("DELETE", api1+"/agents/assistant/memory/d-secret", marta, nil, nil); resp.StatusCode != 204 {
		t.Fatalf("the member could not delete: %d", resp.StatusCode)
	}
}

// testLongMemory is the adapter of cmd/server (commands_memory.go) over the memory service.
type testLongMemory struct{ svc *longmem.Service }

func (m testLongMemory) List(ctx context.Context, org, agentID string) ([]api.MemoryItem, error) {
	items, err := m.svc.List(ctx, org, agentID, 50)
	out := make([]api.MemoryItem, 0, len(items))
	for _, it := range items {
		out = append(out, api.MemoryItem{Scope: it.Kind, Key: it.Ref, Value: it.Title + ": " + it.Snippet})
	}
	return out, err
}
func (m testLongMemory) Remember(ctx context.Context, org, agentID, text string) error {
	return m.svc.RememberNote(ctx, org, agentID, text)
}
func (m testLongMemory) Forget(ctx context.Context, org, agentID, text string) (int, error) {
	items, err := m.svc.Search(ctx, org, agentID, text, 10)
	n := 0
	for _, it := range items {
		if it.ID != "" && m.svc.Delete(ctx, org, agentID, it.ID, "user") == nil {
			n++
		}
	}
	return n, err
}

// The slash commands that show, search or forget what an agent remembers: the key/value memory kept for a private
// office (scope agent@<office>) and the long-term documents of that office are the office's too.
func TestOfficeLeakMemoryCommands(t *testing.T) {
	for _, long := range []bool{false, true} {
		name := map[bool]string{false: "store", true: "long-term"}[long]
		t.Run(name, func(t *testing.T) {
			var tweak []func(*api.Deps)
			if long {
				tweak = append(tweak, func(x *api.Deps) { x.Commands = &api.CommandsDeps{LongMemory: testLongMemory{x.Memory}} })
			}
			e, owner, ana, marta, luis, _, soporte, org, docs := officesWorldSealedDocs(t, tweak...)
			e.json("PUT", api1+"/offices/"+soporte, owner, map[string]any{"visibility": "private"}, 200)
			ctx, now := context.Background(), time.Now().UTC()
			if err := e.store.SetMemory(ctx, org, "assistant", domain.Memory{Scope: "agent@" + soporte, Key: "SECRETO-clave", Value: "SECRETO-valor"}); err != nil {
				t.Fatal(err)
			}
			if err := e.store.SetMemory(ctx, org, "assistant", domain.Memory{Scope: "agent", Key: "ABIERTO-clave", Value: "ABIERTO-valor"}); err != nil {
				t.Fatal(err)
			}
			for _, d := range []longmem.Doc{
				{ID: "d-secret", Kind: longmem.KindTask, Ref: "task:t-secret", Title: "SECRETO-valor", Body: "SECRETO-valor cuerpo", AgentID: "assistant", Scope: longmem.ScopeOffice, OfficeID: soporte},
				{ID: "d-open", Kind: longmem.KindTask, Ref: "task:t-open", Title: "ABIERTO-valor", Body: "ABIERTO-valor cuerpo", AgentID: "assistant", Scope: longmem.ScopeOrg},
			} {
				d.CreatedAt, d.UpdatedAt = now, now
				if err := docs.Upsert(ctx, org, d); err != nil {
					t.Fatal(err)
				}
			}
			office := map[string]any{"surface": "office"}
			run := func(tok string, body map[string]any) string {
				body["context"] = office
				_, raw := e.do("POST", api1+"/commands/run", tok, body, nil)
				return string(raw)
			}
			view := map[string]any{"command": "memory", "args": map[string]any{"agent": "assistant"}}
			search := map[string]any{"command": "search", "args": map[string]any{"query": "valor"}}
			del := map[string]any{"command": "memory", "args": map[string]any{"agent": "assistant", "action": "delete"}}
			for who, tok := range map[string]string{"admin outside": owner, "other office": ana, "nobody": luis} {
				for _, b := range []map[string]any{view, search, del} {
					if raw := run(tok, b); strings.Contains(raw, "SECRETO") {
						t.Fatalf("%s: /%v shows the private office's memory: %.500s", who, b["command"], raw)
					}
				}
			}
			if raw := run(marta, view); !strings.Contains(raw, "SECRETO") {
				t.Fatalf("the member: /memory lacks the office's memory: %.500s", raw)
			}
			// the count to confirm is of what the person sees: the office's memory is not counted for who is not in it
			n := map[bool]string{false: "1 memories", true: "1 memories"}[long]
			if raw := run(owner, del); !strings.Contains(raw, n) {
				t.Fatalf("the admin outside the office must be asked to confirm only what they see (%s): %.400s", n, raw)
			}
			if raw := run(marta, del); !strings.Contains(raw, "2 memories") {
				t.Fatalf("the member confirms the office's memory too: %.400s", raw)
			}
		})
	}
}

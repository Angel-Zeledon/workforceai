package api_test

import (
	"context"
	"strings"
	"testing"
	"time"

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

package api_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"aiworkforce/backend/internal/domain"
)

// The temporary helpers of a request asked from a private office are that office's work: the group (its parts, its
// cost) is not in GET /requests/{id} for who is not in the office (an admin who is not a member included), and the
// routes to accept, decline, stop or retry answer 404 as for a request that does not exist and change nothing. A member
// of the office sees the group and steers it.
func TestOfficeLeakHelpersAPI(t *testing.T) {
	e, owner, ana, marta, luis, _, soporte, org := officesWorldSealedCfg(t, helpersCfg)
	e.json("PUT", api1+"/offices/"+soporte, owner, map[string]any{"visibility": "private"}, 200)
	ctx, now := context.Background(), time.Now().UTC()
	if err := e.store.CreateRequest(ctx, org, domain.Request{ID: "req-H", Text: "SECRETO-pedido revisa los contratos", Status: domain.RequestDone, CreatedAt: now, OfficeID: soporte}); err != nil {
		t.Fatal(err)
	}
	if err := e.store.CreateTask(ctx, org, domain.Task{ID: "task-H", RequestID: "req-H", Title: "Revisar", AgentID: "assistant", OfficeID: soporte, Status: domain.TaskDone,
		DependsOn: []string{}, CreatedAt: now, FinishedAt: &now, Output: &domain.StructuredOutput{Summary: "ok"}}); err != nil {
		t.Fatal(err)
	}
	g := seedProposal(t, e, org, "req-H", "assistant")

	for name, tok := range map[string]string{"admin not in the office": owner, "member of another office": ana, "nobody's": luis} {
		if resp, raw := e.do("GET", api1+"/requests/req-H", tok, nil, nil); resp.StatusCode != 404 || strings.Contains(string(raw), g.ID) {
			t.Fatalf("%s: GET /requests/req-H = %d: %.300s", name, resp.StatusCode, raw)
		}
		if _, raw := e.do("GET", api1+"/requests", tok, nil, nil); strings.Contains(string(raw), "req-H") || strings.Contains(string(raw), g.ID) {
			t.Fatalf("%s: GET /requests lists the private office's request: %.300s", name, raw)
		}
		for _, c := range []struct {
			path string
			body any
		}{{"/helpers", map[string]any{"count": 3, "cap_usd": 1}}, {"/helpers/decline", map[string]any{}}, {"/helpers/stop", map[string]any{}}, {"/helpers/retry", map[string]any{"part": 1}}} {
			if resp, raw := e.do("POST", api1+"/requests/req-H"+c.path, tok, c.body, nil); resp.StatusCode != 404 {
				t.Fatalf("%s: POST %s = %d, want 404: %.300s", name, c.path, resp.StatusCode, raw)
			}
		}
	}
	if cur, err := e.spy.GetHelperGroupByRequest(ctx, org, "req-H"); err != nil || cur.Status != domain.HelperProposed {
		t.Fatalf("a refused request changed the group: %+v %v", cur, err)
	}
	// the member of the office sees the group in its request and steers it
	if resp, raw := e.do("GET", api1+"/requests/req-H", marta, nil, nil); resp.StatusCode != 200 || !strings.Contains(string(raw), g.ID) {
		t.Fatalf("the member lacks the group: %d %.300s", resp.StatusCode, raw)
	}
	if resp, raw := e.do("POST", api1+"/requests/req-H/helpers/decline", marta, map[string]any{}, nil); resp.StatusCode != 200 {
		t.Fatalf("the member cannot decline the helpers: %d %.300s", resp.StatusCode, raw)
	}
}

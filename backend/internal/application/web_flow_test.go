package application_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/controls"
	"aiworkforce/backend/internal/domain"
)

const webAgent = "researcher"

type fakeWeb struct {
	mu    sync.Mutex
	calls []application.WebCall
	res   application.WebResult
}

func (f *fakeWeb) Run(_ context.Context, c application.WebCall) application.WebResult {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, c)
	return f.res
}

func (f *fakeWeb) n() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func webRuntime() *spyRuntime {
	rt := &fakeRuntime{plan: onePlan(webAgent)}
	rt.runTask = func(in application.RunTaskRequest) (application.RunTaskResponse, error) {
		res := okResult("hecho: " + in.Task.Title)
		if len(in.ExternalContent) == 0 {
			res.ToolRequests = []application.ToolRequest{{Tool: "web", Action: "search", Args: map[string]any{"query": "go"}}}
		} else {
			res.ToolRequests = nil
		}
		return res, nil
	}
	return &spyRuntime{fakeRuntime: rt}
}

func okWeb() *fakeWeb {
	return &fakeWeb{res: application.WebResult{Status: "ok", Tainted: true, CostUSD: 0.005,
		Block: `<untrusted_data id="search" source="web.search" from="web" trust="external_untrusted">` + "\nresult\n</untrusted_data>"}}
}

func newWebEnv(t *testing.T, granted bool) (*wired, *fakeWeb, *spyRuntime) {
	t.Helper()
	spy := webRuntime()
	w := wire(t, spy)
	tools := []string{"calculator"}
	if granted {
		tools = append(tools, "web")
	}
	if err := w.store.CreateAgent(context.Background(), org, domain.Agent{ID: webAgent, Name: "R", Role: "analyst", Title: "R", Tools: tools,
		Autonomy: "autonomous", State: domain.StateIdle}); err != nil {
		t.Fatal(err)
	}
	fw := okWeb()
	w.orch.SetWebRunner(fw)
	return w, fw, spy
}

func TestWebSearchResultReachesAgentAsUntrustedData(t *testing.T) {
	w, fw, spy := newWebEnv(t, true)
	reqID, _ := w.orch.Submit(context.Background(), "investiga")
	w.waitFor("request done", func() bool { return w.request(reqID).Status == domain.RequestDone })
	if fw.n() != 1 || fw.calls[0].Action != "search" || fw.calls[0].Org != org {
		t.Fatalf("calls %+v", fw.calls)
	}
	if !strings.Contains(spy.seen(), "untrusted_data") {
		t.Fatal("the agent did not get the result as delimited data")
	}
	if _, ok := w.pendingApproval(); ok {
		t.Fatal("a web read needs no approval")
	}
	if !w.hasAudit("tool.executed") {
		t.Fatal("tool.executed audit missing")
	}
}

func TestWebDeniedWithoutGrant(t *testing.T) {
	w, fw, _ := newWebEnv(t, false)
	reqID, _ := w.orch.Submit(context.Background(), "investiga")
	w.waitFor("request done", func() bool { return w.request(reqID).Status == domain.RequestDone })
	if fw.n() != 0 || !w.hasAudit("tool.denied") {
		t.Fatalf("an agent without the grant must not reach the web (calls %d)", fw.n())
	}
}

func TestWebStoppedByReadOnlyMode(t *testing.T) {
	w, fw, _ := newWebEnv(t, true)
	if _, err := w.ctl.SetMode(context.Background(), org, controls.ModeReadOnly, "admin", "audit"); err != nil {
		t.Fatal(err)
	}
	reqID, _ := w.orch.Submit(context.Background(), "investiga")
	w.waitFor("request done", func() bool { return w.request(reqID).Status == domain.RequestDone })
	if fw.n() != 0 {
		t.Fatal("read-only mode must stop web calls")
	}
}

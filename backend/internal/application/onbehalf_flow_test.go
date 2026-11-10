package application_test

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/drive"
	"aiworkforce/backend/internal/objstore"
)

// spyFS is the real fs tool that records on whose behalf every call came (GatewayCall.OnBehalfOf).
type spyFS struct {
	*drive.GatewayTool
	mu   sync.Mutex
	seen []string
}

func (s *spyFS) note(stage string, c application.GatewayCall) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seen = append(s.seen, stage+" "+c.Action+" by="+c.OnBehalfOf)
}

func (s *spyFS) calls() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.seen...)
}

func (s *spyFS) Assess(ctx context.Context, c application.GatewayCall) application.NativeAssessment {
	s.note("assess", c)
	return s.GatewayTool.Assess(ctx, c)
}

func (s *spyFS) Run(ctx context.Context, c application.GatewayCall, approved bool) application.NativeResult {
	s.note("run", c)
	return s.GatewayTool.Run(ctx, c, approved)
}

// onBehalfWorld is a runtime whose agent asks for one fs call on its first turn, behind a spying fs tool.
func onBehalfWorld(t *testing.T, tr application.ToolRequest) (*wired, *spyFS) {
	t.Helper()
	rt := &fakeRuntime{plan: onePlan("sales")}
	var mu sync.Mutex
	n := 0
	rt.runTask = func(in application.RunTaskRequest) (application.RunTaskResponse, error) {
		mu.Lock()
		n++
		first := n == 1
		mu.Unlock()
		res := okResult("hecho")
		if first {
			res.ToolRequests = []application.ToolRequest{tr}
		}
		return res, nil
	}
	w := wire(t, &spyRuntime{fakeRuntime: rt})
	drv := drive.New(drive.Config{Store: drive.NewMemStore(), Objects: objstore.NewMemory(), OrgID: org, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if _, err := drv.Write(application.WithOrg(context.Background(), org), drive.Human("u"), drive.WriteInput{Path: "/Agents/sales/nota.md", Data: []byte("nota")}); err != nil {
		t.Fatal(err)
	}
	spy := &spyFS{GatewayTool: &drive.GatewayTool{FS: &drive.AgentFS{Svc: drv}}}
	w.gw.AddNative(spy)
	return w, spy
}

func allBy(t *testing.T, calls []string, who string) {
	t.Helper()
	if len(calls) == 0 {
		t.Fatal("the fs tool was never called")
	}
	for _, c := range calls {
		if !strings.HasSuffix(c, " by="+who) {
			t.Fatalf("every fs call must work for %q: %v", who, calls)
		}
	}
}

// The person who submitted the request reaches the drive on every fs call (the drive narrows /Offices to them), even
// though the work runs on a background context that carries no actor.
func TestFsCallsCarryTheRequester(t *testing.T) {
	w, spy := onBehalfWorld(t, application.ToolRequest{Tool: "fs", Action: "read", Args: map[string]any{"path": "/Agents/sales/nota.md"}, Risk: "low"})
	reqID, err := w.orch.Submit(application.WithActor(context.Background(), "beto"), "lee mi nota")
	if err != nil {
		t.Fatal(err)
	}
	w.waitFor("task done", func() bool { return w.tasks(reqID)["Responder correos"].Status == domain.TaskDone })
	allBy(t, spy.calls(), "beto")
}

// After a human approves, the resumed call still works for the person who asked (not for the approver, nor nobody).
func TestFsCallAfterApprovalCarriesTheRequester(t *testing.T) {
	w, spy := onBehalfWorld(t, application.ToolRequest{Tool: "fs", Action: "write", Args: map[string]any{"path": "/Shared/x.md", "content": "hola"}, Risk: "medium"})
	reqID, err := w.orch.Submit(application.WithActor(context.Background(), "beto"), "escribe en compartido")
	if err != nil {
		t.Fatal(err)
	}
	var ap domain.Approval
	w.waitFor("approval", func() bool {
		if _, err := w.gw.PlanGet(org, reqID); err == nil {
			_, _ = w.gw.PlanDecide(context.Background(), org, reqID, "u1", true)
		}
		var ok bool
		ap, ok = w.pendingApproval()
		return ok
	})
	if _, err := w.appr.Decide(context.Background(), ap.ID, "approve", ""); err != nil {
		t.Fatal(err)
	}
	w.waitFor("request done", func() bool { return w.request(reqID).Status == domain.RequestDone })
	calls := spy.calls()
	ran := false
	for _, c := range calls {
		ran = ran || c == "run write by=beto"
	}
	if !ran {
		t.Fatalf("the approved write ran for beto: %v", calls)
	}
	allBy(t, calls, "beto")
}

// spyRecall records the actor of every Recall (the memory recalls drive files only as far as that person sees).
type spyRecall struct {
	mu     sync.Mutex
	actors []string
}

func (s *spyRecall) Recall(ctx context.Context, _ string, _ application.MemoryRecall) []application.RecalledMemory {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.actors = append(s.actors, application.ActorFrom(ctx, ""))
	return nil
}
func (s *spyRecall) RememberTask(context.Context, string, domain.Task, string) {}
func (s *spyRecall) RememberReport(context.Context, string, domain.Report)     {}

func TestRecallCarriesTheRequester(t *testing.T) {
	for _, who := range []string{"beto", ""} {
		rt := &fakeRuntime{plan: onePlan("sales")}
		rt.runTask = func(application.RunTaskRequest) (application.RunTaskResponse, error) { return okResult("hecho"), nil }
		w := wire(t, &spyRuntime{fakeRuntime: rt})
		spy := &spyRecall{}
		w.orch.SetMemorySource(spy)
		ctx := context.Background()
		if who != "" {
			ctx = application.WithActor(ctx, who)
		}
		reqID, err := w.orch.Submit(ctx, "algo")
		if err != nil {
			t.Fatal(err)
		}
		w.waitFor("task done", func() bool { return w.tasks(reqID)["Responder correos"].Status == domain.TaskDone })
		spy.mu.Lock()
		got := append([]string(nil), spy.actors...)
		spy.mu.Unlock()
		if len(got) == 0 {
			t.Fatalf("%q: no recall", who)
		}
		for _, a := range got {
			if a != who {
				t.Fatalf("%q: recall ran as %v", who, got)
			}
		}
	}
}

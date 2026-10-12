package application_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"net/http/httptest"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/controls"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/drive"
	"aiworkforce/backend/internal/gateway"
	"aiworkforce/backend/internal/objstore"
	"aiworkforce/backend/internal/sandbox"
	"aiworkforce/backend/internal/sandbox/runnerd"
)

// End-to-end test of the code sandbox with REAL Docker, the real runner daemon,
// the real HTTP client, the real drive (inputs and outputs) and the real
// orchestrator with approvals and the kill switch. Skipped (never faked) when
// the docker CLI, a reachable daemon or the sandbox image is missing:
//
//	docker build -t wfai-sandbox-python:1 sandbox/

func dockerSandboxEnv(t *testing.T) *runnerd.Server {
	t.Helper()
	img := os.Getenv("SANDBOX_IMAGE")
	if img == "" {
		img = "wfai-sandbox-python:1"
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker CLI not available")
	}
	if err := exec.Command("docker", "image", "inspect", img).Run(); err != nil {
		t.Skipf("sandbox image %s not built: %v", img, err)
	}
	srv, err := runnerd.New(runnerd.Config{Token: "e2e-token-0123456789abcdef", Image: img, Runtime: os.Getenv("SANDBOX_RUNTIME"), MaxConcurrent: 2})
	if err != nil {
		t.Fatal(err)
	}
	hs := httptest.NewServer(srv.Handler())
	t.Cleanup(hs.Close)
	t.Cleanup(func() { srv.AbortAll("") })
	t.Setenv("E2E_RUNNER_URL", hs.URL)
	return srv
}

func sandboxContainersRunning() string {
	out, _ := exec.Command("docker", "ps", "-q", "--filter", "label=wfai.sandbox=1", "--filter", "status=running").Output()
	return strings.TrimSpace(string(out))
}

func TestCodeSandboxEndToEndWithRealDocker(t *testing.T) {
	dockerSandboxEnv(t)
	var args atomic.Pointer[map[string]any]
	set := func(m map[string]any) { args.Store(&m) }
	const code1 = `import pandas as pd
import matplotlib
matplotlib.use("Agg")
import matplotlib.pyplot as plt
df = pd.read_csv("in/ventas.csv")
res = df.groupby("region", as_index=False)["monto"].sum()
res.to_csv("out/resumen.csv", index=False)
res.plot.bar(x="region", y="monto")
plt.savefig("out/grafico.png")
print("total", int(df["monto"].sum()))
`
	set(map[string]any{"language": "python", "code": code1, "purpose": "totales por region",
		"input_files": []any{"/Agents/" + codeAgent + "/ventas.csv"}, "expected_outputs": []any{"resumen.csv", "grafico.png"}})
	spy := &spyRuntime{fakeRuntime: &fakeRuntime{plan: onePlan(codeAgent)}}
	spy.fakeRuntime.runTask = func(in application.RunTaskRequest) (application.RunTaskResponse, error) {
		res := okResult("hecho: " + in.Task.Title)
		if len(in.ExternalContent) == 0 {
			res.ToolRequests = []application.ToolRequest{{Tool: "code", Action: "run", Risk: "low", Args: *args.Load()}}
		}
		return res, nil
	}
	w := wire(t, spy)
	ctx := context.Background()
	if err := w.store.CreateAgent(ctx, org, domain.Agent{ID: codeAgent, Name: "Coder", Role: "analyst", Title: "Analista", Tools: []string{"code"},
		Autonomy: "autonomous", State: domain.StateIdle}); err != nil {
		t.Fatal(err)
	}
	dr := drive.New(drive.Config{Store: drive.NewMemStore(), Objects: objstore.NewMemory(), OrgID: org})
	dctx := application.WithOrg(ctx, org)
	csv := "region,monto\nnorte,100\nsur,50\nnorte,25\n"
	if _, err := dr.Write(dctx, drive.Human("owner"), drive.WriteInput{Path: "/Agents/" + codeAgent + "/ventas.csv", Data: []byte(csv)}); err != nil {
		t.Fatal(err)
	}
	var fileActivity atomic.Int32
	df := &sandbox.DriveFiles{Drive: dr, Notify: func(context.Context, string, string, string) { fileActivity.Add(1) }}
	store := sandbox.NewMemoryStore()
	lim := sandbox.DefaultLimits()
	lim.TimeoutSeconds = 60
	svc := &sandbox.Service{Store: store, Runner: sandbox.NewHTTPRunner(os.Getenv("E2E_RUNNER_URL"), "e2e-token-0123456789abcdef"), Files: df, Sink: df,
		Guard: gateway.Guard{G: w.gw}, Ceiling: lim, MaxRunsPerDay: 10, Poll: 100 * time.Millisecond}
	_ = store.PutSettings(ctx, org, sandbox.Settings{Enabled: true})
	w.orch.SetCodeRunner(svc)

	longWait := func(what string, cond func() bool) {
		t.Helper()
		deadline := time.Now().Add(90 * time.Second)
		for time.Now().Before(deadline) {
			if cond() {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatalf("timeout waiting for %s", what)
	}

	// 1. The agent asks to run code on a CSV of the drive: an approval with the exact code appears and nothing runs.
	req1, _ := w.orch.Submit(ctx, "analiza las ventas")
	ap1 := w.awaitApproval()
	run1, err := store.GetRunByApproval(ctx, org, ap1.ID)
	if err != nil || run1.Code != code1 || run1.Status != sandbox.StatusAwaiting || len(run1.Inputs) != 1 || run1.Inputs[0].Name != "ventas.csv" {
		t.Fatalf("the approval must carry the exact code and input: %+v %v", run1, err)
	}
	if ap1.Action != "code.run" || ap1.Risk != "high" || sandboxContainersRunning() != "" {
		t.Fatalf("approval %+v / a container runs before approval", ap1)
	}
	// 2. Approve: pandas runs in the container and the outputs land on the agent's desk.
	if _, err := w.appr.Decide(ctx, ap1.ID, "approve", ""); err != nil {
		t.Fatal(err)
	}
	longWait("request 1 done", func() bool { return w.request(req1).Status == domain.RequestDone })
	done1, _ := store.GetRunByApproval(ctx, org, ap1.ID)
	if done1.Status != sandbox.StatusSucceeded || !strings.Contains(done1.Stdout, "total 175") || len(done1.Outputs) != 2 {
		t.Fatalf("run 1: %s exit=%v stdout=%q stderr=%q outputs=%+v", done1.Status, done1.ExitCode, done1.Stdout, done1.Stderr, done1.Outputs)
	}
	base := "/Agents/" + codeAgent + "/outputs/" + run1.ID + "/"
	f, err := dr.FileByPath(dctx, base+"resumen.csv")
	if err != nil || f.CreatedBy != drive.AgentP(codeAgent) {
		t.Fatalf("resumen.csv on the desk as the agent: %+v %v", f, err)
	}
	rc, _, _ := dr.Open(dctx, f, 0)
	var b bytes.Buffer
	_, _ = b.ReadFrom(rc)
	rc.Close()
	if !strings.Contains(b.String(), "norte,125") || !strings.Contains(b.String(), "sur,50") {
		t.Fatalf("resumen.csv = %q", b.String())
	}
	png, err := dr.FileByPath(dctx, base+"grafico.png")
	if err != nil || png.Size < 100 || png.Mime != "image/png" {
		t.Fatalf("grafico.png: %+v %v", png, err)
	}
	if fileActivity.Load() != 2 || !strings.Contains(spy.seen(), "untrusted_data") {
		t.Fatalf("file.activity=%d / the agent did not get the delimited result", fileActivity.Load())
	}
	if sandboxContainersRunning() != "" {
		t.Fatal("containers must be disposable")
	}

	// 3. The same agent changes the code: it needs a NEW approval (new hash), and nothing runs until then.
	set(map[string]any{"language": "python", "code": "import time\nprint('start', flush=True)\ntime.sleep(120)\n", "purpose": "espera larga"})
	req2, _ := w.orch.Submit(ctx, "otra vez, otro codigo")
	var ap2 domain.Approval
	w.waitFor("second approval", func() bool {
		ps, _ := w.store.ListApprovals(ctx, org, "pending")
		for _, p := range ps {
			if p.ID != ap1.ID {
				ap2 = p
				return true
			}
		}
		return false
	})
	run2, _ := store.GetRunByApproval(ctx, org, ap2.ID)
	if run2.Hash == run1.Hash || run2.Status != sandbox.StatusAwaiting || ap2.ID == ap1.ID {
		t.Fatalf("modified code must be a new approval with a new hash: %+v", run2)
	}
	// 4. Approve it, then pull the kill switch while it runs: the container is killed and the run aborted.
	w.waitFor("agent settled", func() bool { return w.agent(codeAgent).State == domain.StateAwaitingApproval })
	if _, err := w.appr.Decide(ctx, ap2.ID, "approve", ""); err != nil {
		t.Fatal(err)
	}
	longWait("run 2 running in a container", func() bool {
		r, _ := store.GetRunByApproval(ctx, org, ap2.ID)
		return r.Status == sandbox.StatusRunning && sandboxContainersRunning() != ""
	})
	if _, err := w.ctl.KillSwitch(ctx, org, controls.LevelFreeze, "drill", "admin"); err != nil {
		t.Fatal(err)
	}
	longWait("run 2 aborted", func() bool {
		r, _ := store.GetRunByApproval(ctx, org, ap2.ID)
		return r.Status == sandbox.StatusAborted
	})
	longWait("container gone", func() bool { return sandboxContainersRunning() == "" })
	_ = req2
}

package runnerd

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"net/http/httptest"

	"aiworkforce/backend/internal/sandbox"
)

// Integration tests with REAL Docker. They need the docker CLI, a reachable
// daemon and the sandbox image (docker build -t wfai-sandbox-python:1 sandbox/).
// Without them the tests are skipped (never faked).

const testToken = "0123456789abcdef-test-token"

func dockerEnv(t *testing.T) (*sandbox.HTTPRunner, *Server) {
	t.Helper()
	img := os.Getenv("SANDBOX_IMAGE")
	if img == "" {
		img = "wfai-sandbox-python:1"
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker CLI not available")
	}
	if err := exec.Command("docker", "image", "inspect", img).Run(); err != nil {
		t.Skipf("sandbox image %s not built (docker build -t %s sandbox/): %v", img, img, err)
	}
	// The runner's own environment holds secrets: none may reach a container.
	t.Setenv("RUNTIME_TOKEN", "sekret-runtime-token-xyz")
	t.Setenv("DATABASE_URL", "postgres://user:sekret-db-password@db/x")
	srv, err := New(Config{Token: testToken, Image: img, Runtime: os.Getenv("SANDBOX_RUNTIME"), MaxConcurrent: 4})
	if err != nil {
		t.Fatal(err)
	}
	hs := httptest.NewServer(srv.Handler())
	t.Cleanup(hs.Close)
	t.Cleanup(func() { srv.AbortAll("") })
	return sandbox.NewHTTPRunner(hs.URL, testToken), srv
}

func lim() sandbox.Limits {
	l := sandbox.DefaultLimits()
	l.TimeoutSeconds = 20
	return l
}

func run(t *testing.T, r *sandbox.HTTPRunner, id, code string, l sandbox.Limits, files ...sandbox.RunFile) sandbox.RunResponse {
	t.Helper()
	resp, err := r.Run(context.Background(), sandbox.RunRequest{RunID: id, OrgID: "org1", Code: code, Files: files, Limits: l})
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func containers(t *testing.T) string {
	out, _ := exec.Command("docker", "ps", "-aq", "--filter", "label=wfai.sandbox=1").Output()
	return strings.TrimSpace(string(out))
}

// stillRunning waits up to 10 s for every sandbox container to stop running
// (a killed --rm container can take a moment to be removed; what matters is
// that the code no longer runs) and returns the ones still running.
func stillRunning(t *testing.T) string {
	deadline := time.Now().Add(10 * time.Second)
	for {
		out, _ := exec.Command("docker", "ps", "-q", "--filter", "label=wfai.sandbox=1", "--filter", "status=running").Output()
		ids := strings.TrimSpace(string(out))
		if ids == "" || time.Now().After(deadline) {
			return ids
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func TestDockerHappyPathInputsAndOutputs(t *testing.T) {
	r, _ := dockerEnv(t)
	resp := run(t, r, "run_happy01", `
import pandas as pd, numpy as np, matplotlib
matplotlib.use("Agg")
import matplotlib.pyplot as plt, openpyxl
df = pd.read_csv("in/data.csv")
print("sum", int(df["v"].sum()))
df.to_csv("out/result.csv", index=False)
df.to_excel("out/result.xlsx", index=False)
df.plot(); plt.savefig("out/chart.png")
open("out/evil.html","w").write("<script>alert(1)</script>")
open("out/run.sh","w").write("rm -rf /")
`, lim(), sandbox.RunFile{Name: "data.csv", Data: []byte("v\n1\n2\n3\n")})
	if resp.Status != sandbox.RunOK || !strings.Contains(resp.Stdout, "sum 6") {
		t.Fatalf("resp %+v", resp)
	}
	names := map[string]bool{}
	for _, f := range resp.Files {
		names[f.Name] = true
	}
	if !names["result.csv"] || !names["result.xlsx"] || !names["chart.png"] || names["evil.html"] || names["run.sh"] {
		t.Fatalf("files %v dropped %v", names, resp.Dropped)
	}
	if len(resp.Dropped) < 2 {
		t.Fatalf("dropped %v", resp.Dropped)
	}
	if c := containers(t); c != "" {
		t.Fatalf("containers must be disposable, left: %s", c)
	}
}

func TestDockerNoNetwork(t *testing.T) {
	r, _ := dockerEnv(t)
	resp := run(t, r, "run_net00001", `
import urllib.request, socket
try:
    urllib.request.urlopen("http://1.1.1.1", timeout=4)
    print("CONNECTED")
except Exception as e:
    print("BLOCKED", type(e).__name__)
try:
    socket.create_connection(("8.8.8.8", 53), timeout=4)
    print("CONNECTED2")
except Exception as e:
    print("BLOCKED2", type(e).__name__)
try:
    print(socket.gethostbyname("example.com")); print("RESOLVED")
except Exception as e:
    print("NODNS")
import os
# Some kernels (e.g. Docker Desktop's) create fallback tunnel devices (gre0, sit0...)
# in every network namespace; they stay DOWN. What matters: only loopback is UP.
up = []
for i in sorted(os.listdir("/sys/class/net")):
    p = "/sys/class/net/%s/flags" % i
    if os.path.isfile(p) and int(open(p).read().strip(), 16) & 1:
        up.append(i)
print("IFACES", up)
`, lim())
	if resp.Status != sandbox.RunOK || strings.Contains(resp.Stdout, "CONNECTED") || strings.Contains(resp.Stdout, "RESOLVED") ||
		!strings.Contains(resp.Stdout, "BLOCKED") || !strings.Contains(resp.Stdout, "BLOCKED2") {
		t.Fatalf("network must be unreachable: %+v", resp)
	}
	if !strings.Contains(resp.Stdout, "IFACES ['lo']") {
		t.Fatalf("only loopback expected: %s", resp.Stdout)
	}
}

func TestDockerTimeoutKillsRun(t *testing.T) {
	r, _ := dockerEnv(t)
	l := lim()
	l.TimeoutSeconds = 3
	start := time.Now()
	resp := run(t, r, "run_loop0001", "print('start', flush=True)\nwhile True:\n    pass\n", l)
	if resp.Status != sandbox.RunTimeout {
		t.Fatalf("want timeout, got %+v", resp)
	}
	if d := time.Since(start); d > 20*time.Second {
		t.Fatalf("took %s", d)
	}
	if !strings.Contains(resp.Stdout, "start") {
		t.Fatalf("partial stdout lost: %+v", resp)
	}
	time.Sleep(500 * time.Millisecond)
	if c := containers(t); c != "" {
		t.Fatalf("container left behind: %s", c)
	}
}

func TestDockerBigWritesAreStopped(t *testing.T) {
	r, _ := dockerEnv(t)
	l := lim()
	l.TimeoutSeconds = 30
	// 2 GB into /work/out: RLIMIT_FSIZE / the tmpfs size stop it long before.
	resp := run(t, r, "run_big00001", `
chunk = b"x" * (1 << 20)
with open("out/huge.csv", "wb") as f:
    for i in range(2048):
        f.write(chunk)
print("WROTE-2GB")
`, l)
	if strings.Contains(resp.Stdout, "WROTE-2GB") || resp.Status == sandbox.RunOK {
		t.Fatalf("2 GB write must not succeed: %+v", resp)
	}
	for _, f := range resp.Files {
		if int64(len(f.Data)) > l.MaxOutputFileBytes {
			t.Fatalf("returned file too big: %d", len(f.Data))
		}
	}
	// 2 GB into many files on the tmpfs (each under the per-file limit)
	resp = run(t, r, "run_big00002", `
import os
chunk = b"y" * (1 << 20)
try:
    for n in range(600):
        with open(f"/work/f{n}.bin", "wb") as f:
            for i in range(4):
                f.write(chunk)
    print("WROTE-MANY")
except OSError as e:
    print("STOPPED", e.errno)
`, l)
	if strings.Contains(resp.Stdout, "WROTE-MANY") || !strings.Contains(resp.Stdout, "STOPPED") {
		t.Fatalf("tmpfs size must stop the writes: %+v", resp)
	}
}

func TestDockerStdoutFloodIsCapped(t *testing.T) {
	r, _ := dockerEnv(t)
	l := lim()
	resp := run(t, r, "run_flood001", "import sys\nfor i in range(200000):\n    print('line', i, 'x'*80)\n", l)
	if len(resp.Stdout) > 2*l.MaxStdioBytes+1024 || !resp.StdoutTruncated {
		t.Fatalf("stdout %d bytes truncated=%v status=%s", len(resp.Stdout), resp.StdoutTruncated, resp.Status)
	}
}

func TestDockerSecretsAbsentAndHardening(t *testing.T) {
	r, _ := dockerEnv(t)
	resp := run(t, r, "run_env00001", `
import os, json
print("ENV", json.dumps(dict(os.environ)))
print("UID", os.getuid(), os.getgid())
caps = [l for l in open("/proc/self/status") if l.startswith("CapEff")][0].split()[1]
print("CAPEFF", caps)
for p in ("/usr/local/evil", "/etc/evil", "/opt/evil"):
    try:
        open(p, "w").write("x"); print("WROTE", p)
    except OSError as e:
        print("RO", p)
print("NOSOCK", not os.path.exists("/var/run/docker.sock"))
print("HOSTSHOME", os.path.exists("/Users") or os.path.exists("/host"))
nns = [l for l in open("/proc/self/status") if l.startswith("NoNewPrivs")][0].split()[1]
print("NONEWPRIVS", nns)
`, lim())
	out := resp.Stdout
	if resp.Status != sandbox.RunOK {
		t.Fatalf("%+v", resp)
	}
	for _, secret := range []string{"sekret", "RUNTIME_TOKEN", "DATABASE_URL", "SANDBOX_TOKEN", testToken} {
		if strings.Contains(out, secret) {
			t.Fatalf("secret %q reached the container: %s", secret, out)
		}
	}
	for _, want := range []string{"UID 65532 65532", "CAPEFF 0000000000000000", "NOSOCK True", "HOSTSHOME False", "NONEWPRIVS 1"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in %s", want, out)
		}
	}
	if strings.Contains(out, "WROTE") {
		t.Fatalf("root fs must be read-only: %s", out)
	}
}

func TestDockerMemoryAndPidLimits(t *testing.T) {
	r, _ := dockerEnv(t)
	l := lim()
	l.MemoryMB = 128
	resp := run(t, r, "run_mem00001", "x = bytearray(1024*1024*1024)\nprint('ALLOCATED')\n", l)
	if strings.Contains(resp.Stdout, "ALLOCATED") || resp.Status == sandbox.RunOK {
		t.Fatalf("memory limit not enforced: %+v", resp)
	}
	l = lim()
	l.TimeoutSeconds = 15
	resp = run(t, r, "run_fork0001", `
import os, sys
n = 0
try:
    while True:
        pid = os.fork()
        if pid == 0:
            import time; time.sleep(20); os._exit(0)
        n += 1
except OSError:
    print("FORK-LIMITED", n < 200)
`, l)
	if !strings.Contains(resp.Stdout, "FORK-LIMITED True") {
		t.Fatalf("pids limit not enforced: %+v", resp)
	}
	time.Sleep(500 * time.Millisecond)
	if c := containers(t); c != "" {
		t.Fatalf("container left behind: %s", c)
	}
}

func TestDockerAbortKillsContainer(t *testing.T) {
	r, srv := dockerEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan sandbox.RunResponse, 1)
	go func() {
		resp, _ := r.Run(ctx, sandbox.RunRequest{RunID: "run_abort001", OrgID: "org1", Code: "import time\nprint('x', flush=True)\ntime.sleep(60)\n", Limits: lim()})
		done <- resp
	}()
	deadline := time.Now().Add(15 * time.Second)
	for containers(t) == "" && time.Now().Before(deadline) {
		time.Sleep(200 * time.Millisecond)
	}
	if containers(t) == "" {
		t.Fatal("the container never started")
	}
	// Kill switch path 1: explicit abort call.
	if err := r.Abort(context.Background(), "org1", "run_abort001"); err != nil {
		t.Fatal(err)
	}
	if c := stillRunning(t); c != "" {
		t.Fatalf("abort must kill the container: %s", c)
	}
	cancel()
	<-done
	// Kill switch path 2: AbortAll (also finds orphans by label).
	go func() {
		_, _ = r.Run(context.Background(), sandbox.RunRequest{RunID: "run_abort002", OrgID: "org1", Code: "import time\ntime.sleep(60)\n", Limits: lim()})
	}()
	for i := 0; i < 50 && containers(t) == ""; i++ {
		time.Sleep(200 * time.Millisecond)
	}
	srv.AbortAll("")
	if c := stillRunning(t); c != "" {
		t.Fatalf("AbortAll must kill running containers: %s", c)
	}
}

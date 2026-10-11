package runnerd

import (
	"archive/tar"
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"aiworkforce/backend/internal/sandbox"
)

func newTestServer(t *testing.T) *Server {
	t.Helper()
	s, err := New(Config{Token: "0123456789abcdef-test-token"})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestRunnerRequiresToken(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("a runner without a token must not start")
	}
	s := newTestServer(t)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	for _, h := range []string{"", "Bearer wrong"} {
		req, _ := http.NewRequest("POST", srv.URL+"/v1/run", strings.NewReader("{}"))
		if h != "" {
			req.Header.Set("Authorization", h)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("auth %q: %d", h, resp.StatusCode)
		}
	}
	resp, _ := http.Get(srv.URL + "/healthz")
	if resp.StatusCode != 200 {
		t.Fatal("healthz must be open")
	}
}

// The isolation guarantees are visible in the docker arguments.
func TestDockerArgsIsolation(t *testing.T) {
	t.Setenv("RUNTIME_TOKEN", "super-secret")
	t.Setenv("SANDBOX_TOKEN", "super-secret")
	s := newTestServer(t)
	args := s.DockerArgs(sandbox.RunRequest{RunID: "run_abc123"}, sandbox.DefaultLimits())
	joined := " " + strings.Join(args, " ") + " "
	for _, want := range []string{" --network none ", " --read-only ", " --cap-drop ALL ", " --security-opt no-new-privileges ", " --user 65532:65532 ",
		" --pids-limit 128 ", " --memory 512m ", " --memory-swap 512m ", " --pull never ", "/work:rw,noexec,nosuid,nodev,size=256m"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("docker args lack %q: %s", want, joined)
		}
	}
	for _, bad := range []string{" -v ", "--volume", "--mount", "--privileged", "unconfined", "--network host", "docker.sock", "super-secret", "--env-file", "--cap-add",
		" --pid ", "--pid=", " --ipc ", "--ipc=", " --uts ", "--userns", "--device"} { // host namespaces/devices; --pids-limit is a wanted limit
		if strings.Contains(joined, bad) {
			t.Fatalf("docker args must not contain %q: %s", bad, joined)
		}
	}
	// -e only carries SBX_ limits
	for i, a := range args {
		if a == "-e" && !strings.HasPrefix(args[i+1], "SBX_") {
			t.Fatalf("unexpected env %q", args[i+1])
		}
	}
	s.cfg.Runtime = "runsc"
	if j := strings.Join(s.DockerArgs(sandbox.RunRequest{RunID: "run_abc123"}, sandbox.DefaultLimits()), " "); !strings.Contains(j, "--runtime runsc") {
		t.Fatal("SANDBOX_RUNTIME must select the runtime")
	}
}

func TestValidateRejectsBadRequests(t *testing.T) {
	good := sandbox.RunRequest{RunID: "run_abc123", Code: "print(1)"}
	if err := validate(good); err != nil {
		t.Fatal(err)
	}
	for name, mut := range map[string]func(*sandbox.RunRequest){
		"id":        func(r *sandbox.RunRequest) { r.RunID = "../x" },
		"empty":     func(r *sandbox.RunRequest) { r.Code = " " },
		"name":      func(r *sandbox.RunRequest) { r.Files = []sandbox.RunFile{{Name: "../etc/passwd"}} },
		"duplicate": func(r *sandbox.RunRequest) { r.Files = []sandbox.RunFile{{Name: "a.csv"}, {Name: "A.csv"}} },
	} {
		r := good
		mut(&r)
		if validate(r) == nil {
			t.Fatalf("%s must be rejected", name)
		}
	}
}

// The container is untrusted: a malicious result tar cannot smuggle paths, links or big files.
func TestParseResultDistrustsContainer(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	add := func(name string, typ byte, data string) {
		h := &tar.Header{Name: name, Typeflag: typ, Mode: 0o600, Size: int64(len(data))}
		if typ == tar.TypeSymlink {
			h.Linkname, h.Size = "/etc/passwd", 0
		}
		_ = tw.WriteHeader(h)
		if typ == tar.TypeReg {
			_, _ = tw.Write([]byte(data))
		}
	}
	add("meta.json", tar.TypeReg, `{"exit_code":0}`)
	add("stdout.txt", tar.TypeReg, "hi")
	add("out/ok.csv", tar.TypeReg, "a,b")
	add("out/../../etc/x.csv", tar.TypeReg, "evil")
	add("out/link.csv", tar.TypeSymlink, "")
	add("out/page.html", tar.TypeReg, "<script>")
	add("out/.hidden.csv", tar.TypeReg, "x")
	add("elsewhere/x.csv", tar.TypeReg, "x")
	_ = tw.Close()
	r, err := parseResult(buf.Bytes(), sandbox.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if len(r.files) != 1 || r.files[0].Name != "ok.csv" || r.stdout != "hi" {
		t.Fatalf("files %+v stdout %q", r.files, r.stdout)
	}
	if len(r.dropped) != 3 {
		t.Fatalf("dropped %v", r.dropped)
	}
	if _, err := parseResult([]byte("garbage"), sandbox.DefaultLimits()); err == nil {
		t.Fatal("garbage must fail")
	}
}

var _ = context.Background

// Command sandbox-runner is the only service with access to Docker. It runs
// the code the backend sends after a human approved it, each run in a
// disposable container. See docs/architecture/code-sandbox.md.
//
// SECURITY: this process controls the Docker daemon, which is root-equivalent
// on the host. It must not be exposed beyond the internal network, it requires
// SANDBOX_TOKEN, and it never forwards its environment to the containers. For a
// stronger boundary run it against a dedicated, rootless or remote Docker
// daemon, and set SANDBOX_RUNTIME=runsc (gVisor) when available.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"aiworkforce/backend/internal/sandbox"
	"aiworkforce/backend/internal/sandbox/runnerd"
)

func envInt(k string, def int) int {
	if n, err := strconv.Atoi(os.Getenv(k)); err == nil && n > 0 {
		return n
	}
	return def
}

// runnerToken is the bearer token of the service. In RENDER_EXEC=local mode the
// dedicated RENDER_TOKEN wins (SANDBOX_TOKEN is the fallback); otherwise only
// SANDBOX_TOKEN is read, as always.
func runnerToken(local bool) string {
	if local {
		if t := os.Getenv("RENDER_TOKEN"); t != "" {
			return t
		}
	}
	return os.Getenv("SANDBOX_TOKEN")
}

// healthcheck is `sandbox-runner -healthcheck`: the container HEALTHCHECK of the
// render image (it has no curl/wget).
func healthcheck() int {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8090"
	}
	c := http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get("http://127.0.0.1:" + port + "/healthz")
	if err != nil {
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "-healthcheck" {
		os.Exit(healthcheck())
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	lim := sandbox.DefaultLimits()
	lim.TimeoutSeconds = envInt("SANDBOX_MAX_RUNTIME_SECONDS", lim.TimeoutSeconds)
	lim.MemoryMB = envInt("SANDBOX_MEMORY_MB", lim.MemoryMB)
	lim.PIDs = envInt("SANDBOX_PIDS", lim.PIDs)
	lim.MaxOutputBytes = int64(envInt("SANDBOX_MAX_OUTPUT_BYTES", int(lim.MaxOutputBytes)))
	lim.MaxOutputFileBytes = int64(envInt("SANDBOX_MAX_OUTPUT_FILE_BYTES", int(lim.MaxOutputFileBytes)))
	local := strings.EqualFold(strings.TrimSpace(os.Getenv("RENDER_EXEC")), runnerd.RenderExecLocal)
	srv, err := runnerd.New(runnerd.Config{
		RenderExec:     os.Getenv("RENDER_EXEC"),
		Token:          runnerToken(local),
		Image:          os.Getenv("SANDBOX_IMAGE"),
		RenderImage:    os.Getenv("SANDBOX_RENDER_IMAGE"),
		Runtime:        os.Getenv("SANDBOX_RUNTIME"),
		SeccompProfile: os.Getenv("SANDBOX_SECCOMP_PROFILE"),
		MaxConcurrent:  envInt("SANDBOX_MAX_CONCURRENT", 2),
		WorkMB:         envInt("SANDBOX_WORK_MB", 256),
		Ceiling:        lim,
		Log:            log,
	})
	if err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
	srv.CleanScratch() // local render mode: stale scratch dirs of a crashed predecessor
	srv.KillOrphans()  // a crashed predecessor must not leave code running
	port := os.Getenv("PORT")
	if port == "" {
		port = "8090"
	}
	hs := &http.Server{Addr: ":" + port, Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		srv.AbortAll("")
		sctx, c := context.WithTimeout(context.Background(), 10*time.Second)
		defer c()
		_ = hs.Shutdown(sctx)
	}()
	log.Info("sandbox-runner listening", "addr", hs.Addr, "render_exec", os.Getenv("RENDER_EXEC"), "image", os.Getenv("SANDBOX_IMAGE"), "runtime", os.Getenv("SANDBOX_RUNTIME"))
	if err := hs.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

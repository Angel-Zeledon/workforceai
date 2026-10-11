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

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	lim := sandbox.DefaultLimits()
	lim.TimeoutSeconds = envInt("SANDBOX_MAX_RUNTIME_SECONDS", lim.TimeoutSeconds)
	lim.MemoryMB = envInt("SANDBOX_MEMORY_MB", lim.MemoryMB)
	lim.PIDs = envInt("SANDBOX_PIDS", lim.PIDs)
	lim.MaxOutputBytes = int64(envInt("SANDBOX_MAX_OUTPUT_BYTES", int(lim.MaxOutputBytes)))
	lim.MaxOutputFileBytes = int64(envInt("SANDBOX_MAX_OUTPUT_FILE_BYTES", int(lim.MaxOutputFileBytes)))
	srv, err := runnerd.New(runnerd.Config{
		Token:          os.Getenv("SANDBOX_TOKEN"),
		Image:          os.Getenv("SANDBOX_IMAGE"),
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
	srv.KillOrphans() // a crashed predecessor must not leave code running
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
	log.Info("sandbox-runner listening", "addr", hs.Addr, "image", os.Getenv("SANDBOX_IMAGE"), "runtime", os.Getenv("SANDBOX_RUNTIME"))
	if err := hs.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

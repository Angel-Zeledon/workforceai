package main

import (
	"log/slog"
	"os"
	"strings"

	"aiworkforce/backend/internal/drive"
	"aiworkforce/backend/internal/sandbox"
)

// renderFromEnv is the sandbox render capability for the agents' fs.read
// (images and PDF pages as pixels, docs/architecture/agent-vision-pdf.md). It
// uses the same runner as code runs (SANDBOX_URL + SANDBOX_TOKEN); the runner
// serves renders only when it has a render image (SANDBOX_RENDER_IMAGE), and
// DRIVE_RENDER=off disables it here. Without it, PNG/JPEG/GIF images are still
// prepared with the Go decoders and PDF pages cannot be seen (the agent says so).
func renderFromEnv(log *slog.Logger) drive.Renderer {
	url, token := strings.TrimSpace(os.Getenv("SANDBOX_URL")), os.Getenv("SANDBOX_TOKEN")
	if url == "" || token == "" || strings.EqualFold(strings.TrimSpace(os.Getenv("DRIVE_RENDER")), "off") {
		log.Info("render sandbox not configured: agents see PNG/JPEG/GIF images, scanned PDF pages cannot be rendered")
		return nil
	}
	log.Info("render sandbox: agents can see PDF pages and images", "runner", url)
	return sandbox.DriveRenderer{R: sandbox.NewHTTPRunner(url, token)}
}

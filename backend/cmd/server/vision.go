package main

import (
	"log/slog"
	"os"
	"strings"

	"aiworkforce/backend/internal/drive"
	"aiworkforce/backend/internal/sandbox"
)

// renderEndpoint picks the service that renders PDF pages and images: the
// dedicated renderer (RENDER_URL + RENDER_TOKEN, the no-Docker wf-render service,
// which cannot run code) wins over the sandbox runner (SANDBOX_URL +
// SANDBOX_TOKEN). ok is false when neither pair is complete or DRIVE_RENDER=off.
func renderEndpoint() (source, url, token string, ok bool) {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("DRIVE_RENDER")), "off") {
		return "", "", "", false
	}
	if url, token = strings.TrimSpace(os.Getenv("RENDER_URL")), os.Getenv("RENDER_TOKEN"); url != "" && token != "" {
		return "renderer", url, token, true
	}
	if url, token = strings.TrimSpace(os.Getenv("SANDBOX_URL")), os.Getenv("SANDBOX_TOKEN"); url != "" && token != "" {
		return "sandbox runner", url, token, true
	}
	return "", "", "", false
}

// renderFromEnv is the render capability for the agents' fs.read (images and PDF
// pages as pixels, docs/architecture/agent-vision-pdf.md); see renderEndpoint
// for which service it uses. It is independent of code execution, which wireSandbox
// derives from SANDBOX_URL + SANDBOX_TOKEN alone: RENDER_* never enables code runs.
// Without a renderer, PNG/JPEG/GIF images are still prepared with the Go decoders
// and PDF pages cannot be seen (the agent says so).
func renderFromEnv(log *slog.Logger) drive.Renderer {
	source, url, token, ok := renderEndpoint()
	if !ok {
		log.Info("render not configured: agents see PNG/JPEG/GIF images, scanned PDF pages cannot be rendered")
		return nil
	}
	log.Info("render: agents can see PDF pages and images", "via", source, "url", url)
	return sandbox.DriveRenderer{R: sandbox.NewHTTPRunner(url, token)}
}

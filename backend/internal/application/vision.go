package application

import (
	"context"
	"fmt"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"

	"aiworkforce/backend/internal/domain"
)

// Vision (capability 1 of docs/architecture/agent-capabilities.md, details in
// docs/architecture/agent-vision-pdf.md): agents SEE images. An image reaches
// the runtime only as an ImagePart produced by the drive's fs.read (view
// "image"), so the same zone checks, tool labels, controls and audit apply as to
// any read; nothing here reads the drive directly. The runtime decides which
// allowed provider can see it (data policy) and reports "vision_unavailable"
// when none can.

// ImagePart is one image for the model. Data is the normalized image (re-encoded,
// metadata stripped); in JSON it travels as base64 under "data_b64".
type ImagePart struct {
	Name       string `json:"name"`
	Mime       string `json:"mime"`
	Data       []byte `json:"data_b64"`
	SourcePath string `json:"source_path,omitempty"`
	Page       int    `json:"page,omitempty"`
	Width      int    `json:"width,omitempty"`
	Height     int    `json:"height,omitempty"`
}

// Runtime notices about images (RunTaskResponse.Notices).
const (
	NoticeVisionUnavailable = "vision_unavailable" // no allowed provider can see images: text only
	NoticeImagesDropped     = "images_dropped"     // some images were over the per-call caps
	// EvProviderNoVision tells the UI (once per request) why the agents could not see the images.
	EvProviderNoVision = "provider.no_vision"
)

// VisionConfig bounds what goes to the runtime in one call.
type VisionConfig struct {
	MaxImages     int  // images per runtime call (VISION_MAX_IMAGES, default 4)
	MaxImageBytes int  // per image after normalization (VISION_MAX_IMAGE_MB, default 5)
	MaxTotalBytes int  // per call (VISION_MAX_TOTAL_MB, default 16)
	FirstPass     bool // user attachments that are images go with the first call (VISION_ATTACH_FIRST_PASS, default on)
}

func envIntDefault(k string, def int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(os.Getenv(k))); err == nil && n > 0 {
		return n
	}
	return def
}

// visionConfig is read from the environment once; tests replace it.
var visionConfig = sync.OnceValue(func() VisionConfig {
	first := true
	if v, err := strconv.ParseBool(strings.TrimSpace(os.Getenv("VISION_ATTACH_FIRST_PASS"))); err == nil {
		first = v
	}
	return VisionConfig{
		MaxImages:     min(envIntDefault("VISION_MAX_IMAGES", 4), 8),
		MaxImageBytes: min(envIntDefault("VISION_MAX_IMAGE_MB", 5), 10) << 20,
		MaxTotalBytes: min(envIntDefault("VISION_MAX_TOTAL_MB", 16), 32) << 20,
		FirstPass:     first,
	}
})

// capImages merges add into have and keeps the NEWEST images within the caps
// (the image the agent just asked for matters more than an older one). It
// returns how many were dropped.
func capImages(have, add []ImagePart, cfg VisionConfig) ([]ImagePart, int) {
	all := append(append([]ImagePart{}, have...), add...)
	seen := map[string]bool{}
	var keep []ImagePart
	total, dropped := 0, 0
	for i := len(all) - 1; i >= 0; i-- {
		im := all[i]
		key := im.SourcePath + "#" + strconv.Itoa(im.Page) + "#" + strconv.Itoa(len(im.Data))
		if seen[key] {
			continue // the same image read twice is sent once
		}
		seen[key] = true
		if len(im.Data) == 0 || len(im.Data) > cfg.MaxImageBytes || len(keep) >= cfg.MaxImages || total+len(im.Data) > cfg.MaxTotalBytes {
			dropped++
			continue
		}
		total += len(im.Data)
		keep = append(keep, im)
	}
	for i, j := 0, len(keep)-1; i < j; i, j = i+1, j-1 { // back to reading order
		keep[i], keep[j] = keep[j], keep[i]
	}
	return keep, dropped
}

// withImages adds images to a run request, telling the agent (as data) when
// some did not fit.
func withImages(in *RunTaskRequest, add []ImagePart) {
	if len(add) == 0 {
		return
	}
	var dropped int
	in.Images, dropped = capImages(in.Images, add, visionConfig())
	if dropped > 0 {
		in.ExternalContent = append(append([]string{}, in.ExternalContent...),
			fmt.Sprintf("[images] %d image(s) were not attached to this call: at most %d images per call. Ask for the one you need again if it matters.",
				dropped, visionConfig().MaxImages))
	}
}

var imageExt = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true}

// IsImagePath reports whether a drive path names an image the agents can see.
func IsImagePath(p string) bool { return imageExt[strings.ToLower(path.Ext(p))] }

// attachmentPaths returns the drive paths of the [attachments] block the chat
// appends to a request text (chat_files.go: attachmentNote).
func attachmentPaths(text string) []string {
	i := strings.LastIndex(text, "\n[attachments]\n")
	if i < 0 {
		return nil
	}
	block := text[i:]
	if j := strings.Index(block, "\n[/attachments]"); j >= 0 {
		block = block[:j]
	}
	var out []string
	for _, line := range strings.Split(block, "\n") {
		if p, ok := strings.CutPrefix(strings.TrimSpace(line), "- /"); ok && p != "" {
			out = append(out, "/"+p)
		}
	}
	return out
}

// firstPassImages offers the images the user attached to the chat message that
// started the request on the agent's FIRST call, the way Claude/ChatGPT do. Each
// one is an fs.read (view "image") through the Tool Gateway as the agent: an
// attachment outside what the agent may read is simply not attached (and the
// agent still sees the path in the request text).
func (o *Orchestrator) firstPassImages(ctx context.Context, rs *run, t *domain.Task, agent domain.Agent, in *RunTaskRequest) {
	cfg, gw := visionConfig(), o.conn.gw
	if !cfg.FirstPass || gw == nil || rs == nil {
		return
	}
	var paths []string
	for _, p := range attachmentPaths(rs.req.Text) {
		if IsImagePath(p) && len(paths) < cfg.MaxImages {
			paths = append(paths, p)
		}
	}
	if len(paths) == 0 {
		return
	}
	org := o.org(ctx)
	if gw.Route(ctx, org, agent.ID, "fs", "read") != RouteRead {
		return
	}
	taint := rs.taintOf(t.ID)
	var images []ImagePart
	var blocks []string
	for _, p := range paths {
		out := gw.Execute(ctx, GatewayCall{Org: org, AgentID: agent.ID, TaskID: t.ID, RequestID: rs.req.ID, OnBehalfOf: ActorFrom(ctx, ""),
			Tool: "fs", Action: "read", Args: map[string]any{"path": p, "view": "image"}, Autonomy: agent.Autonomy, Tainted: taint.tainted,
			ReadOnlyRequest: rs.readOnlyRequest()})
		if out.Decision != "allowed" || out.Status != "succeeded" {
			reason := out.DenyReason
			if reason == "" {
				reason = out.Decision
			}
			o.rec.Audit(ctx, domain.AuditLog{Actor: agent.ID, Action: "tool.denied", Entity: "task", EntityID: t.ID,
				Details: map[string]any{"tool": "fs", "action": "read", "reason": reason, "attachment": true}})
			continue
		}
		images = append(images, out.Images...)
		blocks = append(blocks, out.Blocks...)
		rs.ext.mu.Lock()
		taint.tainted = taint.tainted || out.Tainted
		taint.suspected = taint.suspected || out.InjectionSuspected
		rs.ext.mu.Unlock()
	}
	if len(blocks) > 0 {
		in.ExternalContent = append(append([]string{}, in.ExternalContent...), blocks...)
	}
	withImages(in, images)
}

// noVisionSent remembers the requests that already told the UI about
// vision_unavailable (bounded: forgotten wholesale past 10000 requests).
var noVisionSent = struct {
	sync.Mutex
	m map[string]bool
}{m: map[string]bool{}}

func firstNoVision(requestID string) bool {
	noVisionSent.Lock()
	defer noVisionSent.Unlock()
	if noVisionSent.m[requestID] {
		return false
	}
	if len(noVisionSent.m) >= 10000 {
		noVisionSent.m = map[string]bool{}
	}
	noVisionSent.m[requestID] = true
	return true
}

// visionNotices turns the runtime's image notices into evidence the person can
// read on the task and, once per request, a provider.no_vision event the UI shows.
func (o *Orchestrator) visionNotices(ctx context.Context, rs *run, t domain.Task, agent domain.Agent, out *domain.StructuredOutput, notices []string) {
	note := func(format string, a ...any) { out.Evidence = append(out.Evidence, fmt.Sprintf(format, a...)) }
	for _, n := range notices {
		switch n {
		case NoticeVisionUnavailable:
			note("No pude ver las imágenes: ningún proveedor de IA permitido para esta organización puede ver imágenes (%s); trabajé solo con el texto.", NoticeVisionUnavailable)
			if rs == nil {
				continue
			}
			if !firstNoVision(rs.req.ID) {
				continue
			}
			o.rec.Emit(ctx, Action{Type: EvProviderNoVision, AgentID: agent.ID, Entity: "org", EntityID: o.org(ctx),
				Payload: map[string]any{"reason": NoticeVisionUnavailable, "task_id": t.ID},
				Text:    "Los agentes no pueden ver imágenes con los proveedores de IA permitidos: trabajan solo con el texto."})
		case NoticeImagesDropped:
			note("Algunas imágenes superaban los límites por llamada y no se enviaron al modelo (%s).", NoticeImagesDropped)
		}
	}
}

// mergeNotices keeps every notice once, in order.
func mergeNotices(a, b []string) []string {
	out := append([]string{}, a...)
	for _, n := range b {
		dup := false
		for _, x := range out {
			dup = dup || x == n
		}
		if !dup {
			out = append(out, n)
		}
	}
	return out
}

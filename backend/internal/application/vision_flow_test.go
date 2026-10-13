package application_test

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
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

func testPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 64, 48))
	img.Set(3, 3, color.RGBA{200, 10, 10, 255})
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// visionWorld wires a real drive behind the Tool Gateway (the fs tool) and a
// runtime that records what it was sent.
func visionWorld(t *testing.T, turn func(n int, in application.RunTaskRequest) application.RunTaskResponse) (*wired, *[]application.RunTaskRequest) {
	t.Helper()
	var mu sync.Mutex
	var turns []application.RunTaskRequest
	rt := &fakeRuntime{plan: onePlan("sales")}
	rt.runTask = func(in application.RunTaskRequest) (application.RunTaskResponse, error) {
		mu.Lock()
		turns = append(turns, in)
		n := len(turns)
		mu.Unlock()
		return turn(n, in), nil
	}
	w := wire(t, &spyRuntime{fakeRuntime: rt})
	drv := drive.New(drive.Config{Store: drive.NewMemStore(), Objects: objstore.NewMemory(), OrgID: org, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	ctx := application.WithOrg(context.Background(), org)
	for _, p := range []string{"/Agents/sales/foto.png", "/Clients/Acme/secreta.png"} {
		if _, err := drv.Write(ctx, drive.Human("u"), drive.WriteInput{Path: p, Data: testPNG(t)}); err != nil {
			t.Fatal(err)
		}
	}
	w.gw.AddNative(&drive.GatewayTool{FS: &drive.AgentFS{Svc: drv}})
	return w, &turns
}

// An image the agent reads with fs.read reaches the SECOND pass as an image
// part; one outside its zones never does. A runtime that reports
// vision_unavailable is surfaced on the task and once to the UI.
func TestImageReadReachesSecondPassThroughPermissions(t *testing.T) {
	w, turns := visionWorld(t, func(n int, in application.RunTaskRequest) application.RunTaskResponse {
		res := okResult("hecho")
		if n == 1 {
			res.ToolRequests = []application.ToolRequest{
				{Tool: "fs", Action: "read", Args: map[string]any{"path": "/Agents/sales/foto.png"}, Risk: "low"},
				{Tool: "fs", Action: "read", Args: map[string]any{"path": "/Clients/Acme/secreta.png"}, Risk: "low"},
			}
		} else {
			res.Notices = []string{application.NoticeVisionUnavailable}
		}
		return res
	})
	reqID, err := w.orch.Submit(context.Background(), "describe la foto de mi escritorio")
	if err != nil {
		t.Fatal(err)
	}
	w.waitFor("task done", func() bool { return w.tasks(reqID)["Responder correos"].Status == domain.TaskDone })
	if len(*turns) != 2 {
		t.Fatalf("expected two turns, got %d", len(*turns))
	}
	first, second := (*turns)[0], (*turns)[1]
	if len(first.Images) != 0 {
		t.Fatal("no image before the agent asked for one")
	}
	if len(second.Images) != 1 || second.Images[0].SourcePath != "/Agents/sales/foto.png" || len(second.Images[0].Data) == 0 {
		t.Fatalf("second pass must carry exactly the permitted image: %+v", second.Images)
	}
	ext := strings.Join(second.ExternalContent, "\n")
	if !strings.Contains(ext, "image attached") || !strings.Contains(ext, "not_granted") || !strings.Contains(ext, "untrusted") {
		t.Fatalf("the image is described as delimited data and the refusal is reported: %q", ext)
	}
	task := w.tasks(reqID)["Responder correos"]
	if task.Output == nil || !strings.Contains(strings.Join(task.Output.Evidence, "\n"), application.NoticeVisionUnavailable) {
		t.Fatalf("vision_unavailable must be visible on the task: %+v", task.Output)
	}
	if n := w.pub.count(application.EvProviderNoVision); n != 1 {
		t.Fatalf("provider.no_vision events = %d, want 1", n)
	}
}

// Images the user attached to the chat message are offered on the FIRST pass,
// read as the agent: an attachment outside the agent's zones is not attached.
func TestAttachedImagesOnFirstPass(t *testing.T) {
	w, turns := visionWorld(t, func(int, application.RunTaskRequest) application.RunTaskResponse { return okResult("hecho") })
	text := "¿qué ves en estas fotos?\n\n[attachments]\nFiles attached by the user:\n- /Agents/sales/foto.png\n- /Clients/Acme/secreta.png\n- /Agents/sales/notas.md\n[/attachments]"
	reqID, err := w.orch.Submit(context.Background(), text)
	if err != nil {
		t.Fatal(err)
	}
	w.waitFor("task done", func() bool { return w.tasks(reqID)["Responder correos"].Status == domain.TaskDone })
	first := (*turns)[0]
	if len(first.Images) != 1 || first.Images[0].SourcePath != "/Agents/sales/foto.png" {
		t.Fatalf("first pass images: %+v", first.Images)
	}
	if !strings.Contains(strings.Join(first.ExternalContent, "\n"), "image attached") {
		t.Fatal("the attached image must be described as data")
	}
}

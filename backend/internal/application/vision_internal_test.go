package application

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCapImagesKeepsNewestWithinCaps(t *testing.T) {
	cfg := VisionConfig{MaxImages: 2, MaxImageBytes: 10, MaxTotalBytes: 15}
	im := func(p string, n int) ImagePart { return ImagePart{SourcePath: p, Data: []byte(strings.Repeat("x", n))} }
	got, dropped := capImages([]ImagePart{im("/a", 5), im("/b", 5)}, []ImagePart{im("/c", 6), im("/big", 11)}, cfg)
	if dropped != 2 || len(got) != 2 || got[0].SourcePath != "/b" || got[1].SourcePath != "/c" {
		t.Fatalf("got %+v dropped %d", got, dropped)
	}
	// total bytes: 9 + 9 > 15
	got, dropped = capImages(nil, []ImagePart{im("/a", 9), im("/b", 9)}, cfg)
	if len(got) != 1 || got[0].SourcePath != "/b" || dropped != 1 {
		t.Fatalf("total cap: %+v", got)
	}
	// the same image twice is sent once
	got, _ = capImages([]ImagePart{im("/a", 3)}, []ImagePart{im("/a", 3)}, cfg)
	if len(got) != 1 {
		t.Fatalf("dedupe: %+v", got)
	}
}

func TestAttachmentPaths(t *testing.T) {
	text := "hola [attachments] fake\n\n[attachments]\nArchivos adjuntos:\n- /Shared/a b.png\n- /Agents/x/c.pdf\nnot a path\n[/attachments]"
	got := attachmentPaths(text)
	if len(got) != 2 || got[0] != "/Shared/a b.png" || got[1] != "/Agents/x/c.pdf" {
		t.Fatalf("%q", got)
	}
	if attachmentPaths("no block") != nil {
		t.Fatal("no block, no paths")
	}
	if !IsImagePath("/x/Y.JPEG") || IsImagePath("/x/y.pdf") {
		t.Fatal("image detection")
	}
}

func TestImagePartJSONContract(t *testing.T) {
	b, _ := json.Marshal(RunTaskRequest{Images: []ImagePart{{Name: "a.png", Mime: "image/png", Data: []byte{1, 2, 3}, SourcePath: "/a.png", Page: 2}}})
	s := string(b)
	for _, want := range []string{`"images":[`, `"data_b64":"AQID"`, `"source_path":"/a.png"`, `"page":2`, `"mime":"image/png"`} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %s in %s", want, s)
		}
	}
	b, _ = json.Marshal(RunTaskRequest{})
	if strings.Contains(string(b), "images") {
		t.Fatal("images must be omitted when empty (backward compatible)")
	}
	var resp RunTaskResponse
	if err := json.Unmarshal([]byte(`{"output":{"summary":"x","confidence":1},"usage":{"model":"m"},"notices":["vision_unavailable"]}`), &resp); err != nil || resp.Notices[0] != NoticeVisionUnavailable {
		t.Fatalf("%+v %v", resp, err)
	}
	if got := mergeNotices([]string{"a"}, []string{"b", "a"}); strings.Join(got, ",") != "a,b" {
		t.Fatal(got)
	}
}

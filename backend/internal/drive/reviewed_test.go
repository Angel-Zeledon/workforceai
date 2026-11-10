package drive_test

import (
	"context"
	"slices"
	"testing"

	"aiworkforce/backend/internal/drive"
)

// D2 "Para revisar": a department file a person marked as reviewed (tag "revisado") loses the mark when an agent
// writes a new version of it, and keeps it when a person does or when the file is outside /Offices.
func TestAgentWriteUnderOfficesDropsReviewedTag(t *testing.T) {
	svc, _, _ := newSvc(drive.Limits{})
	ctx := context.Background()
	if _, err := svc.EnsureOfficeFolder(ctx, "dep_1", "Contabilidad", nil); err != nil {
		t.Fatal(err)
	}
	h, ag := drive.Human("u1"), drive.AgentP("accounting")
	write := func(p drive.Principal, path, text string) drive.File {
		t.Helper()
		r, err := svc.Write(ctx, p, drive.WriteInput{Path: path, Data: []byte(text)})
		if err != nil {
			t.Fatal(err)
		}
		return r.File
	}
	tag := func(f drive.File, tags ...string) {
		t.Helper()
		if _, err := svc.SetTags(ctx, h, f.ID, tags); err != nil {
			t.Fatal(err)
		}
	}
	tagsOf := func(path string) []string {
		t.Helper()
		f, err := svc.FileByPath(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		return f.Tags
	}

	const p = "/Offices/Contabilidad/cierre.md"
	f := write(ag, p, "v1")
	tag(f, "cierre", drive.TagReviewed)
	write(ag, p, "v2 del agente")
	if got := tagsOf(p); slices.Contains(got, drive.TagReviewed) || !slices.Contains(got, "cierre") {
		t.Fatalf("after an agent's version the reviewed tag goes, the rest stays: %v", got)
	}

	tag(f, drive.TagReviewed)
	write(h, p, "v3 de la persona")
	if got := tagsOf(p); !slices.Contains(got, drive.TagReviewed) {
		t.Fatalf("a person's version keeps the reviewed tag: %v", got)
	}

	// an agent cannot create a department file already marked as reviewed
	r, err := svc.Write(ctx, ag, drive.WriteInput{Path: "/Offices/Contabilidad/nuevo.md", Data: []byte("x"), Tags: []string{drive.TagReviewed}})
	if err != nil || slices.Contains(r.File.Tags, drive.TagReviewed) {
		t.Fatalf("new file by an agent: %v %v", r.File.Tags, err)
	}

	// outside the departments the tag is just a tag
	const s = "/Shared/notas.md"
	g := write(ag, s, "v1")
	tag(g, drive.TagReviewed)
	write(ag, s, "v2")
	if got := tagsOf(s); !slices.Contains(got, drive.TagReviewed) {
		t.Fatalf("outside /Offices an agent's version keeps the tag: %v", got)
	}
}

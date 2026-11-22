package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/drive"
	"aiworkforce/backend/internal/filecomments"
)

// Anchored comments (migration 828): round trip with anchor and change request, tenant isolation under RLS, no
// deletion by the application, and the composite foreign key refusing a file of another organization.
func TestFileCommentStorePostgres(t *testing.T) {
	env := testStore(t)
	seedOrg(t, env.Store, "org-a")
	seedOrg(t, env.Store, "org-b")
	ctx := context.Background()
	svc := driveSvc(env)
	w, err := svc.Write(application.WithOrg(ctx, "org-a"), drive.Human("u"), drive.WriteInput{Path: "/Shared/doc.md", Data: []byte("Pago a 60 días.")})
	if err != nil {
		t.Fatal(err)
	}
	cs := &FileCommentStore{S: env.Store}
	at := time.Now().UTC().Truncate(time.Microsecond)
	c := filecomments.Comment{ID: "fcm_1", FileID: w.File.ID, Author: filecomments.Actor{Kind: "person", ID: "u1"}, Body: "Que sea a 30", CreatedAt: at,
		Anchor: &filecomments.Anchor{Quote: "Pago a 60 días.", Prefix: "", Suffix: "", Version: 1, Start: 0, End: 15}}
	if err := cs.Add(ctx, "org-a", c); err != nil {
		t.Fatal(err)
	}
	got, err := cs.Get(ctx, "org-a", "fcm_1")
	if err != nil || got.Anchor == nil || got.Anchor.Quote != "Pago a 60 días." || got.Change != nil || got.Author.ID != "u1" {
		t.Fatalf("get = %+v %v", got, err)
	}
	by, ver := "agent:legal", 2
	c.Resolved, c.ResolvedBy, c.ResolvedVersion, c.ResolvedAt = true, &by, &ver, &at
	c.Change = &filecomments.Change{AgentID: "legal", BaseVersion: 1, RequestedBy: "person:u1", RequestedAt: at, TurnID: "t1"}
	if err := cs.Update(ctx, "org-a", c); err != nil {
		t.Fatal(err)
	}
	got, _ = cs.Get(ctx, "org-a", "fcm_1")
	if !got.Resolved || got.ResolvedVersion == nil || *got.ResolvedVersion != 2 || got.Change == nil || got.Change.AgentID != "legal" {
		t.Fatalf("after update = %+v", got)
	}
	if l, _ := cs.List(ctx, "org-a", w.File.ID); len(l) != 1 {
		t.Fatalf("list = %+v", l)
	}
	if _, err := cs.Get(ctx, "org-b", "fcm_1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("org-b reads it: %v", err)
	}
	if l, _ := cs.List(ctx, "org-b", w.File.ID); len(l) != 0 {
		t.Fatalf("org-b lists %+v", l)
	}
	if err := cs.Update(ctx, "org-b", c); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("org-b updates it: %v", err)
	}
	if err := cs.Add(ctx, "org-b", filecomments.Comment{ID: "fcm_x", FileID: w.File.ID, Author: c.Author, Body: "x", CreatedAt: at}); err == nil {
		t.Fatal("a comment of org-b on a file of org-a must fail")
	}
	if err := env.exec(ctx, "org-a", `DELETE FROM file_comments`); err == nil {
		t.Fatal("file_comments must not be deletable by app_user")
	}
}

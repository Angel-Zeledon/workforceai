package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/config"
	"aiworkforce/backend/internal/controls"
	"aiworkforce/backend/internal/customers"
	"aiworkforce/backend/internal/drive"
	"aiworkforce/backend/internal/inboxes"
	"aiworkforce/backend/internal/infrastructure/memory"
	"aiworkforce/backend/internal/objstore"
)

// The wiring: memory store without Postgres, the controls gate (kill switch beats read-only, fail closed) and the
// customer folder lookup through the B12 service.
func TestWireInboxesGateAndCustomerFolder(t *testing.T) {
	const org = "org-inbox-wiring"
	ctx := application.WithOrg(context.Background(), org)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctl := controls.New(controls.NewMemStore(), nil, nil)
	dr := drive.New(drive.Config{Store: drive.NewMemStore(), Objects: objstore.NewMemory(), OrgID: org, Log: log})
	cs := customers.New(customers.Config{Store: customers.NewMemStore(), OrgID: org, Log: log})
	cs.SetFolders(customerFolders{drive: dr})
	acmeName := "Acme"
	acme, err := cs.Create(ctx, "u_admin", customers.Input{Name: &acmeName})
	if err != nil {
		t.Fatal(err)
	}
	var cfg config.Config
	cfg.App.OrgID = org
	rec := &application.Recorder{OrgID: org, Store: memory.New(), Pub: nopPub{}, Log: log}
	if wireInboxes(cfg, nil, rec, ctl, cs, nil) != nil {
		t.Fatal("no drive must mean no inboxes")
	}
	svc := wireInboxes(cfg, nil, rec, ctl, cs, dr)

	c, err := svc.Create(ctx, "u_admin", inboxes.Input{Name: "Acme exports", CustomerID: acme.ID})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(c.FolderPath, acme.FolderPath+"/") || acme.FolderPath == "" {
		t.Fatalf("customer folder: %q under %q", c.FolderPath, acme.FolderPath)
	}
	o, ibx, err := svc.Authenticate(ctx, c.ID, c.Token)
	if err != nil || o != org {
		t.Fatalf("authenticate: %v %s", err, o)
	}
	push := func() error {
		_, err := svc.Push(ctx, org, ibx, inboxes.PushInput{ContentType: "application/json", Body: strings.NewReader(`{"n":1}`)})
		return err
	}
	if err := push(); err != nil {
		t.Fatalf("open push: %v", err)
	}
	if _, err := ctl.SetMode(ctx, org, controls.ModeReadOnly, "u_admin", "audit week"); err != nil {
		t.Fatal(err)
	}
	if err := push(); !errors.Is(err, inboxes.ErrReadOnly) {
		t.Fatalf("read-only: %v", err)
	}
	if _, err := ctl.KillSwitch(ctx, org, controls.LevelFreeze, "incident", "u_admin"); err != nil {
		t.Fatal(err)
	}
	if err := push(); !errors.Is(err, inboxes.ErrKillSwitch) {
		t.Fatalf("kill switch: %v", err)
	}
	if inboxGate(nil) != nil {
		t.Fatal("no controls = open gate")
	}

	// An archived customer stops its inbox.
	if err := cs.Archive(ctx, "u_admin", acme.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := (inboxCustomers{cs: cs}).CustomerFolder(ctx, org, acme.ID); err == nil {
		t.Fatal("archived customer resolved a folder")
	}
}

func TestInboxBaseURL(t *testing.T) {
	t.Setenv("PUBLIC_API_URL", "")
	if got := inboxBaseURL("https://app.example.com/"); got != "https://app.example.com/api/v1" {
		t.Fatalf("from UI_BASE_URL: %q", got)
	}
	if got := inboxBaseURL(""); got != "" {
		t.Fatalf("nothing configured: %q", got)
	}
	t.Setenv("PUBLIC_API_URL", "https://api.example.com/api/v1/")
	if got := inboxBaseURL("https://app.example.com"); got != "https://api.example.com/api/v1" {
		t.Fatalf("PUBLIC_API_URL wins: %q", got)
	}
}

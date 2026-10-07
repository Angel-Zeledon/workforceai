package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"strings"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/audit"
	"aiworkforce/backend/internal/domain"

	"aiworkforce/backend/internal/config"
	"aiworkforce/backend/internal/controls"
	"aiworkforce/backend/internal/infrastructure/postgres"
)

// runCtl is the out-of-band emergency path ("server ctl freeze"): it flips the
// kill switch straight in the database when the UI or the API cannot answer.
//
//	server ctl freeze|lockdown [-org ID] [-reason TEXT]
//	server ctl release          [-org ID] -reason TEXT
//	server ctl audit-verify     [-org ID | -all] [-anchor-seq N -anchor-hash H]
//	server ctl audit-verify     -file export.jsonl [-strict]
//
// audit-verify checks the hash chain of the audit trail (database or an
// exported JSONL file) and exits with an error when it does not verify.
//
// The operator who can run it already has database access; every change is
// logged. Running servers read the state on every check, so it takes effect at
// once.
func runCtl(log *slog.Logger, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: server ctl freeze|lockdown|release|audit-verify [-org ID] [-reason TEXT]")
	}
	cmd := strings.ToLower(args[0])
	fs := flag.NewFlagSet("ctl", flag.ContinueOnError)
	cfg := config.Load()
	org := fs.String("org", cfg.App.OrgID, "organization id")
	reason := fs.String("reason", "", "reason (required for release)")
	all := fs.Bool("all", false, "audit-verify: every organization")
	file := fs.String("file", "", "audit-verify: verify an exported JSONL file offline (no database needed)")
	strict := fs.Bool("strict", false, "audit-verify -file: fail on gaps (use for unfiltered exports)")
	anchorSeq := fs.Int64("anchor-seq", 0, "audit-verify: externally stored chain position")
	anchorHash := fs.String("anchor-hash", "", "audit-verify: externally stored hash of that position")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if cmd == "audit-verify" && *file != "" {
		return verifyAuditFile(*file, *strict)
	}
	if cfg.DatabaseURL == "" {
		return errors.New("DATABASE_URL is required: the in-memory store lives inside the running server")
	}
	ctx := context.Background()
	pg, err := postgres.Open(ctx, postgres.Options{URL: cfg.DatabaseURL, MigrateURL: cfg.MigrateURL, AppRole: cfg.DBAppRole})
	if err != nil {
		return err
	}
	defer pg.Close()
	if cmd == "audit-verify" {
		return verifyAuditDB(ctx, pg, *org, *all, *anchorSeq, *anchorHash)
	}
	svc := controls.New(&postgres.ConnStore{S: pg}, func(_ context.Context, org, actor, action, _, _ string, _ map[string]any) {
		log.Info("audit", "org", org, "actor", actor, "action", action)
	}, nil)
	const actor = "ctl"
	switch cmd {
	case "freeze", "lockdown":
		_, err = svc.KillSwitch(ctx, *org, cmd, *reason, actor)
	case "release":
		_, err = svc.Release(ctx, *org, *reason, actor, false)
	default:
		return fmt.Errorf("unknown ctl command %q", cmd)
	}
	if err == nil {
		fmt.Printf("ok: %s applied to organization %s\n", cmd, *org)
	}
	return err
}

func verifyAuditFile(path string, strict bool) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	recs, err := audit.ReadJSONL(b)
	if err != nil {
		return err
	}
	rep := audit.VerifyRecords(recs, strict)
	fmt.Println("file "+path+":", describe(rep))
	if !rep.OK {
		return fmt.Errorf("audit export does not verify (%s at seq %d)", rep.Reason, rep.BrokenAtSeq)
	}
	return nil
}

func verifyAuditDB(ctx context.Context, pg *postgres.Store, org string, all bool, anchorSeq int64, anchorHash string) error {
	orgs := []string{org}
	if all {
		ids, err := pg.ListOrgIDs(ctx)
		if err != nil {
			return err
		}
		// The fixed demo organization has no memberships, so it is not listed.
		if !slices.Contains(ids, org) {
			ids = append(ids, org)
		}
		orgs = ids
	}
	var anchor *domain.AuditHead
	if anchorSeq > 0 || anchorHash != "" {
		if all || anchorSeq < 1 || anchorHash == "" {
			return errors.New("-anchor-seq and -anchor-hash must be given together, for one organization")
		}
		anchor = &domain.AuditHead{Seq: anchorSeq, Hash: anchorHash}
	}
	broken := 0
	for _, o := range orgs {
		rep, err := application.VerifyChain(ctx, pg, o, anchor)
		if err != nil {
			return fmt.Errorf("organization %s: %w", o, err)
		}
		fmt.Println("organization "+o+":", describe(rep))
		if !rep.OK {
			broken++
		}
	}
	if broken > 0 {
		return fmt.Errorf("%d organization(s) have an audit trail that does not verify", broken)
	}
	return nil
}

func describe(r audit.Report) string {
	if r.OK {
		return fmt.Sprintf("OK, %d entries verified (seq %d..%d, head %s)", r.Checked, r.FirstSeq, r.LastSeq, r.HeadHash)
	}
	return fmt.Sprintf("BROKEN at seq %d: %s (%s); %d entries verified before the break", r.BrokenAtSeq, r.Reason, r.Detail, r.Checked)
}

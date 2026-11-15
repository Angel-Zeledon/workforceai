package drive_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/drive"
)

// shareOrg answers both adapters over a fixed company: Legal (dep_legal: ana leads, beto), Contabilidad (dep_acct:
// carla leads, dani), dep_solo (eli, who is alone and leads nothing), and General (everybody). Every office is private:
// only a member has access.
type shareOrg struct{ rosters map[string]drive.OfficeRoster }

func newShareOrg() shareOrg {
	return shareOrg{rosters: map[string]drive.OfficeRoster{
		"dep_legal": {Known: true, Leads: []string{"ana"}, Members: []string{"ana", "beto"}},
		"dep_acct":  {Known: true, Leads: []string{"carla"}, Members: []string{"carla", "dani"}},
		"dep_solo":  {Known: true, Members: []string{"eli"}}, // no responsible: any member decides
		"general":   {Known: true, General: true, Members: []string{"ana", "beto", "carla", "dani", "eli"}},
	}}
}

func (o shareOrg) OfficeAccess(_ context.Context, _, user, office string) (string, error) {
	if slices.Contains(o.rosters[office].Members, user) {
		return drive.AccessEdit, nil
	}
	return drive.AccessNone, nil
}

func (o shareOrg) OfficeRoster(_ context.Context, _, office string) (drive.OfficeRoster, error) {
	return o.rosters[office], nil
}

func shareEnv(t *testing.T) *agentEnv {
	t.Helper()
	e := newOfficeEnv(t)
	org := newShareOrg()
	e.svc.SetPeople(org)
	e.svc.SetShareOffices(org)
	for p, c := range map[string]string{
		"/Offices/Legal/Casos/notas.md": "notas internas del caso",
		"/Offices/Legal/privado.md":     "solo para legal",
	} {
		if _, err := e.svc.Write(e.ctx, drive.Human("ana"), drive.WriteInput{Path: p, Data: []byte(c)}); err != nil {
			t.Fatal(err)
		}
	}
	return e
}

func (e *agentEnv) share(t *testing.T, user, path, target string) drive.Share {
	t.Helper()
	sh, err := e.svc.RequestShare(e.ctx, user, path, target)
	if err != nil {
		t.Fatalf("share %s: %v", path, err)
	}
	return sh
}

func shareCode(err error) string {
	var de *drive.Error
	if errors.As(err, &de) {
		return de.Code
	}
	return ""
}

func (e *agentEnv) level(user, path string) string { return e.svc.PersonAccess(e.ctx, user, path) }

const (
	demanda = "/Offices/Legal/Casos/demanda.md"
	notas   = "/Offices/Legal/Casos/notas.md"
	privado = "/Offices/Legal/privado.md"
)

func TestShareRequestApproveGivesReadOnly(t *testing.T) {
	e := shareEnv(t)
	if got := e.level("carla", demanda); got != drive.AccessNone {
		t.Fatalf("before the share: %s", got)
	}
	sh := e.share(t, "beto", "/Offices/Legal/Casos", "dep_acct")
	if sh.Status != drive.ShareStatusPending || sh.Kind != drive.ShareKindFolder || sh.Name != "Casos" || sh.OwnerOfficeID != "dep_legal" ||
		sh.RequestedBy != "beto" || sh.DecidedBy != "" || sh.CanDecide || !sh.CanRevoke {
		t.Fatalf("a member's request is pending: %+v", sh)
	}
	if fo, err := e.svc.FolderByID(e.ctx, sh.FileID); err != nil || fo.Path != "/Offices/Legal/Casos" {
		t.Fatalf("file_id of a folder is the folder id: %q %+v %v", sh.FileID, fo, err)
	}
	if f := e.share(t, "ana", privado, "dep_solo"); f.FileID == "" || f.Kind != drive.ShareKindFile {
		t.Fatalf("file_id of a file: %+v", f)
	} else if got, err := e.svc.GetFile(e.ctx, f.FileID); err != nil || got.Path != privado {
		t.Fatalf("file_id opens the file: %+v %v", got, err)
	}
	if got := e.level("carla", demanda); got != drive.AccessNone {
		t.Fatalf("pending opens nothing: %s", got)
	}
	// only the responsible decides: a plain member gets 403, the target's people get 404
	if _, err := e.svc.DecideShare(e.ctx, "beto", sh.ID, drive.ShareApprove); shareCode(err) != "not_decider" || !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("a member that is not a responsible: %v", err)
	}
	if _, err := e.svc.DecideShare(e.ctx, "carla", sh.ID, drive.ShareApprove); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("the target does not decide: %v", err)
	}
	got, err := e.svc.DecideShare(e.ctx, "ana", sh.ID, drive.ShareApprove)
	if err != nil || got.Status != drive.ShareStatusActive || got.DecidedBy != "ana" || got.DecidedAt == nil || got.CanDecide || !got.CanRevoke {
		t.Fatalf("approve: %+v %v", got, err)
	}
	if _, err := e.svc.DecideShare(e.ctx, "ana", sh.ID, drive.ShareReject); shareCode(err) != "not_pending" || !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("decided twice: %v", err)
	}
	// the target's members read the folder and what hangs from it, never edit, never the rest of Legal
	for _, who := range []string{"carla", "dani"} {
		for _, p := range []string{"/Offices/Legal/Casos", demanda, notas} {
			if got := e.level(who, p); got != drive.AccessRead {
				t.Fatalf("%s on %s: %s", who, p, got)
			}
		}
		for _, p := range []string{privado, "/Offices/Legal", "/offices/legal/privado.md"} {
			if got := e.level(who, p); got != drive.AccessNone {
				t.Fatalf("%s on %s must stay closed: %s", who, p, got)
			}
		}
	}
	// the case of the path does not matter, and the owner keeps edit
	if got := e.level("dani", "/offices/LEGAL/casos/Demanda.md"); got != drive.AccessRead {
		t.Fatalf("case-insensitive: %s", got)
	}
	if got := e.level("beto", demanda); got != drive.AccessEdit {
		t.Fatalf("the owner keeps edit: %s", got)
	}
	// someone from a third office sees nothing
	if got := e.level("eli", demanda); got != drive.AccessNone {
		t.Fatalf("a third office: %s", got)
	}
	vis := e.svc.VisibleTo(e.ctx, "carla", []string{demanda, privado, "/Shared/x.md"})
	if !vis[0] || vis[1] || !vis[2] {
		t.Fatalf("visible: %v", vis)
	}
}

func TestShareRejectedOpensNothing(t *testing.T) {
	e := shareEnv(t)
	sh := e.share(t, "beto", demanda, "dep_acct")
	got, err := e.svc.DecideShare(e.ctx, "ana", sh.ID, drive.ShareReject)
	if err != nil || got.Status != drive.ShareStatusRejected {
		t.Fatalf("reject: %+v %v", got, err)
	}
	if lvl := e.level("carla", demanda); lvl != drive.AccessNone {
		t.Fatalf("rejected: %s", lvl)
	}
	// a rejected share does not count as a duplicate: it can be asked again
	e.share(t, "beto", demanda, "dep_acct")
}

func TestShareByResponsibleIsBornActive(t *testing.T) {
	e := shareEnv(t)
	sh := e.share(t, "ana", demanda, "dep_acct")
	if sh.Status != drive.ShareStatusActive || sh.Kind != drive.ShareKindFile || sh.DecidedBy != "ana" || sh.CanDecide || !sh.CanRevoke {
		t.Fatalf("active at birth: %+v", sh)
	}
	if lvl := e.level("dani", demanda); lvl != drive.AccessRead {
		t.Fatalf("dani: %s", lvl)
	}
	if lvl := e.level("dani", notas); lvl != drive.AccessNone {
		t.Fatalf("only the file was shared: %s", lvl)
	}
	// an office without responsible: any member decides, so its own request is active too
	if _, err := e.svc.EnsureOfficeFolder(e.ctx, "dep_solo", "Solo", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Write(e.ctx, drive.Human("eli"), drive.WriteInput{Path: "/Offices/Solo/a.md", Data: []byte("a")}); err != nil {
		t.Fatal(err)
	}
	if sh := e.share(t, "eli", "/Offices/Solo/a.md", "dep_acct"); sh.Status != drive.ShareStatusActive {
		t.Fatalf("no responsible: %+v", sh)
	}
}

func TestShareRevokeRemovesAccess(t *testing.T) {
	e := shareEnv(t)
	sh := e.share(t, "beto", "/Offices/Legal/Casos", "dep_acct")
	if _, err := e.svc.DecideShare(e.ctx, "ana", sh.ID, drive.ShareApprove); err != nil {
		t.Fatal(err)
	}
	if lvl := e.level("carla", demanda); lvl != drive.AccessRead {
		t.Fatal(lvl)
	}
	// neither the target nor a member that did not ask and is not a responsible may revoke
	if err := e.svc.RevokeShare(e.ctx, "carla", sh.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("target: %v", err)
	}
	other := e.share(t, "ana", privado, "dep_acct")
	if err := e.svc.RevokeShare(e.ctx, "beto", other.ID); shareCode(err) != "not_decider" || !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("a member who did not ask: %v", err)
	}
	// who asked may, and so may a responsible; the next call sees it closed
	if err := e.svc.RevokeShare(e.ctx, "beto", sh.ID); err != nil {
		t.Fatal(err)
	}
	if lvl := e.level("carla", demanda); lvl != drive.AccessNone {
		t.Fatalf("revoked: %s", lvl)
	}
	if err := e.svc.RevokeShare(e.ctx, "ana", sh.ID); shareCode(err) != "not_active" || !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("twice: %v", err)
	}
	if err := e.svc.RevokeShare(e.ctx, "ana", other.ID); err != nil {
		t.Fatal(err)
	}
	if lvl := e.level("carla", privado); lvl != drive.AccessNone {
		t.Fatalf("revoked by a responsible: %s", lvl)
	}
	// a pending request can be withdrawn by who asked
	p := e.share(t, "beto", notas, "dep_acct")
	if err := e.svc.RevokeShare(e.ctx, "beto", p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.DecideShare(e.ctx, "ana", p.ID, drive.ShareApprove); shareCode(err) != "not_pending" {
		t.Fatalf("a withdrawn request cannot be approved: %v", err)
	}
}

func TestShareRequestErrors(t *testing.T) {
	e := shareEnv(t)
	cases := []struct {
		name, user, path, target, code string
		kind                           error
	}{
		{"not a member", "carla", demanda, "dep_solo", "not_found", domain.ErrNotFound},
		{"office folder itself", "beto", "/Offices/Legal", "dep_acct", "not_in_office", domain.ErrInvalid},
		{"outside the offices", "beto", "/Shared/politica.md", "dep_acct", "not_in_office", domain.ErrInvalid},
		{"missing file", "beto", "/Offices/Legal/Casos/nada.md", "dep_acct", "not_found", domain.ErrNotFound},
		{"General", "beto", demanda, "general", "general_office", domain.ErrInvalid},
		{"same office", "beto", demanda, "dep_legal", "same_office", domain.ErrInvalid},
		{"unknown target", "beto", demanda, "dep_nope", "not_found", domain.ErrNotFound},
	}
	for _, c := range cases {
		_, err := e.svc.RequestShare(e.ctx, c.user, c.path, c.target)
		if shareCode(err) != c.code || !errors.Is(err, c.kind) {
			t.Errorf("%s: %v", c.name, err)
		}
	}
	e.share(t, "beto", demanda, "dep_acct")
	if _, err := e.svc.RequestShare(e.ctx, "ana", demanda, "dep_acct"); shareCode(err) != "already_shared" || !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("pending duplicate: %v", err)
	}
	if _, err := e.svc.RequestShare(e.ctx, "ana", "/offices/legal/casos/DEMANDA.md", "dep_acct"); shareCode(err) != "already_shared" {
		t.Fatalf("duplicate with another spelling: %v", err)
	}
	// the same path to another department is not a duplicate
	e.share(t, "ana", demanda, "dep_solo")
}

func TestShareListings(t *testing.T) {
	e := shareEnv(t)
	a := e.share(t, "beto", "/Offices/Legal/Casos", "dep_acct") // pending
	b := e.share(t, "ana", privado, "dep_acct")                 // active
	out, err := e.svc.OfficeShares(e.ctx, "ana", "dep_legal", drive.ShareOut, nil)
	if err != nil || len(out) != 2 {
		t.Fatalf("out: %+v %v", out, err)
	}
	for _, sh := range out {
		if sh.ID == a.ID && (!sh.CanDecide || !sh.CanRevoke) {
			t.Fatalf("the responsible can decide the pending one: %+v", sh)
		}
	}
	if pend, _ := e.svc.OfficeShares(e.ctx, "beto", "dep_legal", drive.ShareOut, []string{drive.ShareStatusPending}); len(pend) != 1 || pend[0].ID != a.ID || pend[0].CanDecide || !pend[0].CanRevoke {
		t.Fatalf("pending for the one who asked: %+v", pend)
	}
	// "in" lists only the active ones, for the members of the target
	in, err := e.svc.OfficeShares(e.ctx, "dani", "dep_acct", drive.ShareIn, nil)
	if err != nil || len(in) != 1 || in[0].ID != b.ID || in[0].CanDecide || in[0].CanRevoke {
		t.Fatalf("in: %+v %v", in, err)
	}
	// a person who is not a member, an unknown office and a bad direction
	for _, c := range []struct{ user, office, dir string }{{"carla", "dep_legal", "out"}, {"beto", "dep_acct", "in"}, {"ana", "dep_nope", "out"}} {
		if _, err := e.svc.OfficeShares(e.ctx, c.user, c.office, c.dir, nil); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("%+v: %v", c, err)
		}
	}
	if _, err := e.svc.OfficeShares(e.ctx, "ana", "dep_legal", "sideways", nil); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("dir: %v", err)
	}
	// by path: the share of the file and the one of the folder above it
	at, err := e.svc.SharesAt(e.ctx, "ana", demanda)
	if err != nil || len(at) != 1 || at[0].ID != a.ID {
		t.Fatalf("at: %+v %v", at, err)
	}
	if at, _ := e.svc.SharesAt(e.ctx, "ana", "/Offices/Legal"); len(at) != 0 {
		t.Fatalf("the office folder is not shared: %+v", at)
	}
	if at, _ := e.svc.SharesAt(e.ctx, "ana", "/Shared"); len(at) != 0 {
		t.Fatalf("outside the offices: %+v", at)
	}
}

func TestShareAuditTrail(t *testing.T) {
	e := shareEnv(t)
	sh := e.share(t, "beto", demanda, "dep_acct")
	if _, err := e.svc.DecideShare(e.ctx, "ana", sh.ID, drive.ShareApprove); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.RevokeShare(e.ctx, "ana", sh.ID); err != nil {
		t.Fatal(err)
	}
	var seen []string
	for _, a := range e.al.es {
		if strings.HasPrefix(a.Action, "drive.share.") {
			seen = append(seen, a.Action)
		}
	}
	if !slices.Equal(seen, []string{"drive.share.requested", "drive.share.decided", "drive.share.revoked"}) {
		t.Fatalf("audit: %v", seen)
	}
}

func TestShareAgentOfTargetReadsOnlyWhatIsShared(t *testing.T) {
	e := shareEnv(t)
	sh := e.share(t, "ana", "/Offices/Legal/Casos", "dep_acct")
	read := func(agent, p string) drive.Plan { return e.plan(agent, "read", map[string]any{"path": p}) }
	for _, p := range []string{demanda, notas, "/offices/legal/casos/demanda.md"} {
		if pl := read("acct", p); pl.Deny != "" || pl.Approval {
			t.Fatalf("the target's agent reads %s: %+v", p, pl)
		}
	}
	if pl := read("acct", privado); pl.Deny != "not_granted" {
		t.Fatalf("the rest of Legal stays closed: %+v", pl)
	}
	if pl := read("legal", demanda); pl.Deny != "" {
		t.Fatalf("the owner agent still reads its folder: %+v", pl)
	}
	if pl := read("sales", demanda); pl.Deny != "not_granted" { // an agent of General
		t.Fatalf("a third office: %+v", pl)
	}
	if pl := e.plan("acct", "list", map[string]any{"path": "/Offices/Legal/Casos"}); pl.Deny != "" {
		t.Fatalf("list the shared folder: %+v", pl)
	}
	if pl := e.plan("acct", "list", map[string]any{"path": "/Offices/Legal"}); pl.Deny != "not_granted" {
		t.Fatalf("list the owner's folder: %+v", pl)
	}
	if r := e.fs.Run(e.ctx, "acct", "read", map[string]any{"path": demanda}, false); !r.OK || !strings.Contains(r.Text, "demanda confidencial") {
		t.Fatalf("run read: %+v", r)
	}
	// it shows up as a reachable folder and in the search of the agent, without the rest of Legal
	r := e.fs.Run(e.ctx, "acct", "list", map[string]any{"path": "/"}, false)
	if !r.OK || !strings.Contains(r.Text, "/Offices/Legal/Casos") {
		t.Fatalf("roots: %+v", r)
	}
	r = e.fs.Run(e.ctx, "acct", "search", map[string]any{"query": "confidencial"}, false)
	if !r.OK || !strings.Contains(r.Text, "demanda.md") || strings.Contains(r.Text, "privado") {
		t.Fatalf("search: %+v", r)
	}
	// nothing is written there, with or without approval: write, write again, move out, edit, delete
	for _, c := range []struct {
		action string
		args   map[string]any
	}{
		{"write", map[string]any{"path": "/Offices/Legal/Casos/nuevo.md", "content": "x"}},
		{"write", map[string]any{"path": demanda, "content": "x"}},
		{"move", map[string]any{"path": demanda, "to": "/Offices/Contabilidad/Clientes/demanda.md"}},
		{"move", map[string]any{"path": "/Offices/Contabilidad/Clientes/balance.md", "to": "/Offices/Legal/Casos/balance.md"}},
		{"delete", map[string]any{"path": demanda}},
		{"edit", map[string]any{"path": demanda, "expected_version": 1, "ops": []any{map[string]any{"op": "append", "text": "x"}}}},
	} {
		if pl := e.plan("acct", c.action, c.args); pl.Deny != "shared_read_only" {
			t.Fatalf("%s must be refused outright: %+v", c.action, pl)
		}
	}
	// the person the agent works for narrows it: a member of the target reads; someone from another office does not
	for who, want := range map[string]string{"carla": "", "dani": "", "eli": "person_no_access"} {
		if pl := e.fs.Plan(drive.WithRequester(e.ctx, who), "acct", "read", map[string]any{"path": demanda}); pl.Deny != want {
			t.Fatalf("%s: %+v (want %q)", who, pl, want)
		}
	}
	// a Leer rule over it changes nothing: still no writes
	if _, err := e.svc.SetAgentAccess(e.ctx, drive.Human("ana"), "acct", "/Offices/Legal/Casos", drive.AccessRead); err != nil {
		t.Fatal(err)
	}
	if pl := e.plan("acct", "write", map[string]any{"path": demanda, "content": "x"}); pl.Deny != "shared_read_only" {
		t.Fatalf("a Leer rule must not reopen writes: %+v", pl)
	}
	// a Ninguno rule closes it for the agent even while the share stays
	if _, err := e.svc.SetAgentAccess(e.ctx, drive.Human("ana"), "acct", "/Offices/Legal/Casos", drive.AccessNone); err != nil {
		t.Fatal(err)
	}
	if pl := read("acct", demanda); pl.Deny == "" {
		t.Fatalf("Ninguno must close it: %+v", pl)
	}
	gs, err := e.svc.Grants(e.ctx, "acct")
	if err != nil || len(gs) != 1 {
		t.Fatalf("rules: %+v %v", gs, err)
	}
	if err := e.svc.RevokeGrant(e.ctx, drive.Human("ana"), gs[0].ID); err != nil {
		t.Fatal(err)
	}
	if pl := read("acct", demanda); pl.Deny != "" {
		t.Fatalf("without the rule the share reads again: %+v", pl)
	}
	// revoked: closed on the next call
	if err := e.svc.RevokeShare(e.ctx, "ana", sh.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.RevokeShare(e.ctx, "ana", sh.ID); err == nil {
		t.Fatal("revoked twice")
	}
	if pl := read("acct", demanda); pl.Deny == "" {
		t.Fatalf("revoked: the agent must not read any more: %+v", pl)
	}
}

func TestSharePendingGivesAgentNothing(t *testing.T) {
	e := shareEnv(t)
	e.share(t, "beto", demanda, "dep_acct")
	if pl := e.plan("acct", "read", map[string]any{"path": demanda}); pl.Deny != "not_granted" {
		t.Fatalf("pending: %+v", pl)
	}
}

func TestShareWithoutAdapterIsUnavailable(t *testing.T) {
	e := newOfficeEnv(t)
	if _, err := e.svc.RequestShare(e.ctx, "ana", demanda, "dep_acct"); !errors.Is(err, domain.ErrNotFound) || shareCode(err) != "shares_unavailable" {
		t.Fatalf("no adapter: %v", err)
	}
}

func TestAgentWriteClearsSentTag(t *testing.T) {
	e := shareEnv(t)
	h := drive.Human("ana")
	r, err := e.svc.Write(e.ctx, h, drive.WriteInput{Path: "/Offices/Legal/Casos/enviado.md", Data: []byte("v1"), Tags: []string{drive.TagSent, "otra"}})
	if err != nil {
		t.Fatal(err)
	}
	one := 1
	if r2, err := e.svc.Write(e.ctx, h, drive.WriteInput{Path: r.File.Path, Data: []byte("v2"), Expected: &one}); err != nil || !slices.Contains(r2.File.Tags, drive.TagSent) {
		t.Fatalf("a person's new version keeps the tag: %+v %v", r2.File.Tags, err)
	}
	two := 2
	r3, err := e.svc.Write(e.ctx, drive.AgentP("legal"), drive.WriteInput{Path: r.File.Path, Data: []byte("v3"), Expected: &two})
	if err != nil || slices.Contains(r3.File.Tags, drive.TagSent) || !slices.Contains(r3.File.Tags, "otra") {
		t.Fatalf("an agent's new version takes enviado off and keeps the rest: %+v %v", r3.File.Tags, err)
	}
}

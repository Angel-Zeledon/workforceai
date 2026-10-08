package artifacts_test

import (
	"errors"
	"strings"
	"testing"

	"aiworkforce/backend/internal/artifacts"
	"aiworkforce/backend/internal/domain"
)

func TestProposalsAgentsProposeOnlyPeopleDecide(t *testing.T) {
	e := newEnv(t, nil)
	a := e.sheetArt(`{"A1":{"v":1}}`)
	agent := artifacts.Actor{Kind: artifacts.ActorAgent, ID: "accounting"}
	next := raw(sheet(`{"A1":{"v":1},"A2":{"v":99}}`))

	// No attachment, or read only: no proposals.
	if _, err := e.svc.Propose(e.ctx, agent, a.ID, artifacts.ProposalInput{BaseVersion: 1, Content: next}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("unattached agent: %v", err)
	}
	e.attach(a.ID, "accounting", artifacts.ModeRead)
	if _, err := e.svc.Propose(e.ctx, agent, a.ID, artifacts.ProposalInput{BaseVersion: 1, Content: next}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("read-only agent: %v", err)
	}
	e.attach(a.ID, "accounting", artifacts.ModePropose)
	if _, err := e.svc.Propose(e.ctx, agent, a.ID, artifacts.ProposalInput{BaseVersion: 1, Content: raw(`{"schema":"nope"}`)}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("invalid content: %v", err)
	}
	p, err := e.svc.Propose(e.ctx, agent, a.ID, artifacts.ProposalInput{BaseVersion: 1, Content: next, Summary: "Agregar A2"})
	if err != nil || p.Status != artifacts.ProposalPending {
		t.Fatalf("propose: %v %+v", err, p)
	}
	if got, _ := e.svc.Get(e.ctx, a.ID, 0); got.PendingProposals != 1 || got.HeadVersion != 1 {
		t.Fatalf("meta after propose: %+v", got.Meta)
	}
	if e.pub.count("artifact.proposal_created") != 1 {
		t.Error("no proposal_created event")
	}

	// An agent never decides, not even with edit access; neither does a person without approve.
	e.attach(a.ID, "accounting", artifacts.ModeEdit)
	agentApprover := artifacts.Actor{Kind: artifacts.ActorAgent, ID: "accounting", CanApprove: true}
	for _, who := range []artifacts.Actor{agent, agentApprover, e.me} {
		if _, err := e.svc.Decide(e.ctx, who, a.ID, p.ID, artifacts.DecisionInput{Decision: "accept"}); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("%+v decided: %v", who, err)
		}
	}
	if got, _ := e.svc.Get(e.ctx, a.ID, 0); got.HeadVersion != 1 {
		t.Fatal("content changed without a human decision")
	}

	// A human with approve accepts: a new version with source accept_proposal.
	done, err := e.svc.Decide(e.ctx, e.admin, a.ID, p.ID, artifacts.DecisionInput{Decision: "accept"})
	if err != nil || done.Status != artifacts.ProposalAccepted || done.ResolvedVersion == nil || *done.ResolvedVersion != 2 {
		t.Fatalf("accept: %v %+v", err, done)
	}
	got, _ := e.svc.Get(e.ctx, a.ID, 0)
	if got.HeadVersion != 2 || got.PendingProposals != 0 || cellV(t, got, "A2") != 99 || got.LastAuthor.Kind != artifacts.ActorUser {
		t.Fatalf("after accept: %+v", got.Meta)
	}
	vs, _ := e.svc.Versions(e.ctx, a.ID)
	found := false
	for _, v := range vs {
		found = found || (v.Version == 2 && v.Source == "accept_proposal")
	}
	if !found {
		t.Fatalf("version 2 lacks source accept_proposal: %+v", vs)
	}
	if _, err := e.svc.Decide(e.ctx, e.admin, a.ID, p.ID, artifacts.DecisionInput{Decision: "reject"}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("second decision: %v", err)
	}

	// Reject leaves the content untouched.
	p2, _ := e.svc.Propose(e.ctx, agent, a.ID, artifacts.ProposalInput{BaseVersion: 2, Content: raw(sheet(`{"A1":{"v":5}}`))})
	if _, err := e.svc.Decide(e.ctx, e.admin, a.ID, p2.ID, artifacts.DecisionInput{Decision: "reject", Note: "no"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.svc.Get(e.ctx, a.ID, 0); got.HeadVersion != 2 || got.PendingProposals != 0 {
		t.Fatalf("after reject: %+v", got.Meta)
	}
	if open, _ := e.svc.Proposals(e.ctx, a.ID, "pending"); len(open) != 0 {
		t.Fatalf("pending: %+v", open)
	}
}

func TestProposalConflictLeavesItPending(t *testing.T) {
	e := newEnv(t, nil)
	a := e.sheetArt(`{"A1":{"v":1}}`)
	p, err := e.svc.Propose(e.ctx, e.me, a.ID, artifacts.ProposalInput{BaseVersion: 1, Content: raw(sheet(`{"A1":{"v":2}}`))})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Save(e.ctx, e.me, a.ID, artifacts.SaveInput{BaseVersion: 1, Content: raw(sheet(`{"A1":{"v":3}}`))}); err != nil {
		t.Fatal(err)
	}
	var c *artifacts.Conflict
	if _, err := e.svc.Decide(e.ctx, e.admin, a.ID, p.ID, artifacts.DecisionInput{Decision: "accept"}); !errors.As(err, &c) {
		t.Fatalf("want conflict, got %v", err)
	}
	if open, _ := e.svc.Proposals(e.ctx, a.ID, "pending"); len(open) != 1 {
		t.Fatalf("pending: %+v", open)
	}
}

func TestComments(t *testing.T) {
	e := newEnv(t, nil)
	a := e.sheetArt(`{"A1":{"v":1}}`)
	agent := artifacts.Actor{Kind: artifacts.ActorAgent, ID: "accounting"}
	if _, err := e.svc.AddComment(e.ctx, agent, a.ID, artifacts.CommentInput{Body: "hola"}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("unattached agent: %v", err)
	}
	for _, bad := range []string{"", "   ", strings.Repeat("x", 4001)} {
		if _, err := e.svc.AddComment(e.ctx, e.me, a.ID, artifacts.CommentInput{Body: bad}); !errors.Is(err, domain.ErrInvalid) {
			t.Fatalf("body %q: %v", bad[:min(3, len(bad))], err)
		}
	}
	c, err := e.svc.AddComment(e.ctx, e.me, a.ID, artifacts.CommentInput{Body: " Revisar A1 ", Anchor: &artifacts.Anchor{Sheet: "Margen", Range: "A1"}})
	if err != nil || c.Body != "Revisar A1" || c.Resolved {
		t.Fatalf("add: %v %+v", err, c)
	}
	e.attach(a.ID, "accounting", artifacts.ModePropose)
	reply, err := e.svc.AddComment(e.ctx, agent, a.ID, artifacts.CommentInput{Body: "Listo", ParentID: c.ID})
	if err != nil || reply.ParentID == nil {
		t.Fatalf("agent reply: %v", err)
	}
	if _, err := e.svc.ResolveComment(e.ctx, agent, a.ID, c.ID, true); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("agent resolved: %v", err)
	}
	r, err := e.svc.ResolveComment(e.ctx, e.me, a.ID, c.ID, true)
	if err != nil || !r.Resolved || r.ResolvedBy == nil {
		t.Fatalf("resolve: %v %+v", err, r)
	}
	all, _ := e.svc.Comments(e.ctx, a.ID)
	if len(all) != 2 {
		t.Fatalf("comments: %+v", all)
	}
	b := e.sheetArt(`{"A1":{"v":1}}`)
	if _, err := e.svc.AddComment(e.ctx, e.me, b.ID, artifacts.CommentInput{Body: "x", ParentID: c.ID}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("parent of another artifact: %v", err)
	}
}

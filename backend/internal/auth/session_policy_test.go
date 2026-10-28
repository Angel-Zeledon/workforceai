package auth

import (
	"context"
	"errors"
	"testing"
)

// Every path that opens a session follows the organization policy of a password login: an invitation link or an
// e-mail verification link must not become a way around enforced single sign-on or a required second factor.

type everyOrg struct{ p OrgPolicy }

func (e everyOrg) OrgPolicy(context.Context, string) (OrgPolicy, error) { return e.p, nil }

func TestInvitationAcceptFollowsTheOrganizationPolicy(t *testing.T) {
	e, fp := policyEnv(t)
	owner := e.register(t, "owner@acme.test", "Acme")
	op := e.principal(t, owner)

	fp.set(owner.OrgID, OrgPolicy{RequireMFA: true})
	inv, err := e.svc.CreateInvitation(bg, op, "new@acme.test", RoleMember)
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.AcceptInvitation(bg, AcceptInput{Token: inv.Token, Name: "Nueva", Password: goodPW}, ClientMeta{})
	var enroll *MFAEnrollmentRequiredError
	if !errors.As(err, &enroll) || enroll.Token == "" {
		t.Fatalf("accept under RequireMFA = %v, want an enrolment challenge instead of a session", err)
	}

	fp.set(owner.OrgID, OrgPolicy{EnforceSSO: true})
	inv2, err := e.svc.CreateInvitation(bg, op, "second@acme.test", RoleMember)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.AcceptInvitation(bg, AcceptInput{Token: inv2.Token, Name: "Otra", Password: goodPW}, ClientMeta{}); !errors.Is(err, ErrSSORequired) {
		t.Fatalf("accept under EnforceSSO = %v, want ErrSSORequired", err)
	}
	// The person still joined: the next sign-in goes through the identity provider.
	if ms, err := e.store.MembershipsOf(bg, mustUserID(t, e, "second@acme.test")); err != nil || len(ms) != 1 {
		t.Fatalf("memberships after accept = %v %v", ms, err)
	}
}

func TestVerificationLinkFollowsTheOrganizationPolicy(t *testing.T) {
	mm := &captureMailer{}
	e := newEnv(t, func(c *Config) {
		c.RequireVerification = true
		c.Mailer = mm
		c.AppURL = "https://app.example.com/"
		c.Policy = everyOrg{OrgPolicy{RequireMFA: true}}
	})
	if err := register(e, "ana@example.com", "es"); err == nil {
		t.Fatal("register opened a session before verification")
	}
	_, err := e.svc.Verify(context.Background(), tokenIn(t, mm.last(t)), ClientMeta{})
	var enroll *MFAEnrollmentRequiredError
	if !errors.As(err, &enroll) || enroll.Token == "" {
		t.Fatalf("verify under RequireMFA = %v, want an enrolment challenge instead of a session", err)
	}
	u, err := e.store.UserByEmail(context.Background(), "ana@example.com")
	if err != nil || u.VerifiedAt.IsZero() {
		t.Fatalf("the address must be verified even without a session: %v %+v", err, u)
	}
}

func mustUserID(t *testing.T, e *env, email string) string {
	t.Helper()
	u, err := e.store.UserByEmail(bg, email)
	if err != nil {
		t.Fatal(err)
	}
	return u.ID
}

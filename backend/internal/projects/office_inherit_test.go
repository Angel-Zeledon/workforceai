package projects_test

import (
	"context"
	"testing"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/projects"
)

// A project created from a request or a conversation asked from an office keeps that office; the request it launches is
// asked from it whoever launches it and from wherever. A project created with no office launches a request with none
// (the agents' base offices), even if the launcher is looking at another office.
func TestProjectInheritsTheOfficeItWasCreatedFrom(t *testing.T) {
	e := newEnv(t, newRT(), nil, nil)
	officeOf := func(id string) string {
		t.Helper()
		d := e.detail(id)
		if d.Project.RequestID == "" {
			t.Fatalf("project %s has no request", id)
		}
		r, err := e.store.GetRequest(context.Background(), "", d.Project.RequestID)
		if err != nil {
			t.Fatal(err)
		}
		return r.OfficeID
	}
	fromLegal := application.WithRequestOffice(e.ctx, "dept-legal")
	rec, err := e.svc.CreateDraft(fromLegal, projects.NewProject{Goal: "cierre financiero", TemplateID: tFC})
	if err != nil {
		t.Fatal(err)
	}
	if rec.OfficeID != "dept-legal" || e.detail(rec.ID).Project.OfficeID != "dept-legal" {
		t.Fatalf("the project forgot its office: %+v", rec)
	}
	plain := e.draft(tFC, 0) // created with no office
	if e.detail(plain).Project.OfficeID != "" {
		t.Fatal("a project created with no office has one")
	}
	body := projects.LaunchBody{ApprovedBudgetUSD: budgetBig, AcknowledgeUnderbudget: true, ProceedWithoutInputs: true}
	// launched while looking at another office, the first still works for the one it was created from
	if _, err := e.svc.Launch(application.WithRequestOffice(e.ctx, "dept-sales"), rec.ID, body); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Launch(application.WithRequestOffice(e.ctx, "dept-sales"), plain, body); err != nil {
		t.Fatal(err)
	}
	if got := officeOf(rec.ID); got != "dept-legal" {
		t.Fatalf("the request of the project is asked from %q, want dept-legal", got)
	}
	if got := officeOf(plain); got != "" {
		t.Fatalf("the request of a project with no office is asked from %q, want none", got)
	}
}

package meetings

import (
	"context"
	"testing"

	"aiworkforce/backend/internal/application"
)

// A meeting created from a request or a conversation asked from an office keeps that office; the requests its tasks
// start (the preparation of the agents, the action items) are asked from it, whoever starts them and from wherever.
// One created with no office starts requests with none.
func TestMeetingInheritsTheOfficeItWasCreatedFrom(t *testing.T) {
	r := newRig(t)
	fromLegal := application.WithRequestOffice(context.Background(), "dept-legal")
	in := CreateInput{Title: "Contratos", Start: base, End: base.Add(0),
		Attendees: []Attendee{{Kind: KindAgent, ID: "sales"}, {Kind: KindPerson, ID: "u1", Name: "Ana Pérez"}}}
	in.End = base.Add(3600e9)
	m, err := r.svc.Create(fromLegal, "org", "ana", in)
	if err != nil {
		t.Fatal(err)
	}
	if m.OfficeID != "dept-legal" {
		t.Fatalf("the meeting forgot its office: %+v", m)
	}
	plain := r.create(t, CreateInput{Attendees: []Attendee{{Kind: KindAgent, ID: "sales"}}})
	if plain.OfficeID != "" {
		t.Fatalf("a meeting created with no office has %q", plain.OfficeID)
	}
	// started from another office's screen: the preparation is still the meeting's office's
	other := application.WithRequestOffice(context.Background(), "dept-sales")
	if _, err := r.svc.Start(other, "org", "ana", m.ID, true, "en"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.Start(other, "org", "ana", plain.ID, true, "en"); err != nil {
		t.Fatal(err)
	}
	if len(r.tasks.batches) != 2 || r.tasks.batches[0].OfficeID != "dept-legal" || r.tasks.batches[1].OfficeID != "" {
		t.Fatalf("batches: %+v", r.tasks.batches)
	}
	// the action items of its minutes
	if _, err := r.svc.GenerateMinutes(context.Background(), "org", "ana", m.ID, MinutesInput{Notes: "Action items:\n- Valeria: send the quote"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.svc.CreateTasks(other, "org", "ana", m.ID, nil, "en"); err != nil {
		t.Fatal(err)
	}
	if got := r.tasks.batches[2].OfficeID; got != "dept-legal" {
		t.Fatalf("the action items are asked from %q, want dept-legal", got)
	}
}

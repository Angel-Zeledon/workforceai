package notify

import (
	"context"
	"testing"

	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/mailer"
)

// D3 round 2: with the retry queue on (the default), the responsibles of an office's approval get their e-mail even
// though they are not among the organization's addresses, and only while they still are its responsibles.
func TestQueuedNoticeReachesTheResponsibles(t *testing.T) {
	ctx := context.Background()
	fm := &flakyMail{}
	s, ob, _, _ := outboxSvc(t, fm)
	ap := officeApproval()
	responsible := true
	s.Approvals = &fakeApprovals{ap: ap}
	s.Approvers = func(context.Context, string, domain.Approval) (ApprovalAudience, error) {
		if !responsible {
			return ApprovalAudience{Office: true, Private: true}, nil
		}
		return ApprovalAudience{Office: true, Private: true, Emails: []string{"ana@example.com"}}, nil
	}
	s.Notify(ctx, "o1", ap)
	if len(fm.sent) != 1 || fm.sent[0].To != "ana@example.com" {
		t.Fatalf("sent %+v (rows %+v)", fm.sent, ob.Rows("o1"))
	}

	// an address that is neither the organization's nor a responsible's is still dropped by the queue
	if err := s.enqueue(ctx, "o1", ChanEmail, "eve@example.com", []byte(`{"subject":"x","text":"y","html":""}`)); err != nil {
		t.Fatal(err)
	}
	// and a responsible who stopped being one before delivery gets nothing
	responsible = false
	s.sendEmailFor(ctx, "o1", "ana@example.com", ap.ID, mailer.Message{Subject: "x", Text: "y"})
	before := len(fm.sent)
	if _, err := s.ProcessOutbox(ctx, "o1"); err != nil {
		t.Fatal(err)
	}
	if len(fm.sent) != before {
		t.Fatalf("delivered to someone who may not receive it: %+v", fm.sent[before:])
	}
}

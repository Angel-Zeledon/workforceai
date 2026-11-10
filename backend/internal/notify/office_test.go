package notify

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"

	"aiworkforce/backend/internal/domain"
)

func sentByTo(fm *fakeMail) map[string]string {
	fm.mu.Lock()
	defer fm.mu.Unlock()
	out := map[string]string{}
	for _, m := range fm.sent {
		out[m.To] = m.Text
	}
	return out
}

func officeApproval() domain.Approval {
	ap := pending("low")
	ap.AgentID = "legal"
	return ap
}

// D3: the approval of an open office's agent is e-mailed to its responsibles with the links; the organization's
// addresses and chats hear about it without links. An approval of General is sent as before.
func TestOfficeApprovalGoesToItsResponsibles(t *testing.T) {
	ctx := context.Background()
	open := func(_ context.Context, org string, ap domain.Approval) (ApprovalAudience, error) {
		if org != "o1" || ap.AgentID != "legal" {
			return ApprovalAudience{}, nil
		}
		return ApprovalAudience{Office: true, Emails: []string{"ana@example.com"}}, nil
	}
	s, _, fm, _ := newSvc(t, officeApproval())
	s.Approvers = open
	var chats atomic.Int32
	s.HTTP = &http.Client{Transport: rt(func(r *http.Request) (*http.Response, error) {
		chats.Add(1)
		return &http.Response{StatusCode: 200, Body: http.NoBody, Header: http.Header{}}, nil
	})}
	st, _ := s.Settings.GetSettings(ctx, "o1")
	st.SlackWebhook = "https://hooks.slack.com/services/T000/B000/XXXX"
	_ = s.Settings.PutSettings(ctx, st)
	s.Notify(ctx, "o1", officeApproval())
	by := sentByTo(fm)
	if len(by) != 2 || by["boss@example.com"] == "" || by["ana@example.com"] == "" {
		t.Fatalf("sent to %v", by)
	}
	if a, r := linksOf(t, by["ana@example.com"]); a == "" || r == "" {
		t.Fatalf("the responsible got no links: %s", by["ana@example.com"])
	}
	if a, _ := linksOf(t, by["boss@example.com"]); a != "" {
		t.Fatalf("a non-responsible got links: %s", by["boss@example.com"])
	}
	if chats.Load() == 0 {
		t.Fatal("an open office's approval must still reach the organization's chat")
	}

	// a responsible who is also an address of the organization gets one e-mail, with links (case-insensitive)
	s2, _, fm2, _ := newSvc(t, officeApproval())
	s2.Approvers = func(context.Context, string, domain.Approval) (ApprovalAudience, error) {
		return ApprovalAudience{Office: true, Emails: []string{"BOSS@example.com"}}, nil
	}
	s2.Notify(ctx, "o1", officeApproval())
	if by := sentByTo(fm2); len(by) != 1 {
		t.Fatalf("sent to %v", by)
	} else if a, _ := linksOf(t, by["boss@example.com"]); a == "" {
		t.Fatalf("the responsible got no links: %v", by)
	}

	// General: as today
	s3, _, fm3, _ := newSvc(t, pending("low"))
	s3.Approvers = open
	s3.Notify(ctx, "o1", pending("low"))
	if by := sentByTo(fm3); len(by) != 1 {
		t.Fatalf("General sent to %v", by)
	} else if a, _ := linksOf(t, by["boss@example.com"]); a == "" {
		t.Fatal("General lost its links")
	}
}

// D3: a private office's approval reaches only its responsibles (nothing to the organization's addresses or chats),
// and when who decides cannot be told nothing is sent to them either (fails closed).
func TestPrivateOfficeApprovalStaysWithItsResponsibles(t *testing.T) {
	ctx := context.Background()
	for name, f := range map[string]func(context.Context, string, domain.Approval) (ApprovalAudience, error){
		"private": func(context.Context, string, domain.Approval) (ApprovalAudience, error) {
			return ApprovalAudience{Office: true, Private: true, Emails: []string{"ana@example.com"}}, nil
		},
		"error": func(context.Context, string, domain.Approval) (ApprovalAudience, error) {
			return ApprovalAudience{}, errors.New("members unavailable")
		},
	} {
		s, _, fm, _ := newSvc(t, officeApproval())
		s.Approvers = f
		var chats atomic.Int32
		s.HTTP = &http.Client{Transport: rt(func(r *http.Request) (*http.Response, error) {
			chats.Add(1)
			return &http.Response{StatusCode: 200, Body: http.NoBody, Header: http.Header{}}, nil
		})}
		st, _ := s.Settings.GetSettings(ctx, "o1")
		st.SlackWebhook = "https://hooks.slack.com/services/T000/B000/XXXX"
		_ = s.Settings.PutSettings(ctx, st)
		s.Notify(ctx, "o1", officeApproval())
		by := sentByTo(fm)
		if by["boss@example.com"] != "" || chats.Load() != 0 {
			t.Fatalf("%s: the organization heard about it: %v, %d chats", name, by, chats.Load())
		}
		if name == "private" && (len(by) != 1 || by["ana@example.com"] == "") {
			t.Fatalf("private: sent to %v", by)
		}
	}
}

// D3: a signed link decides an office's approval only for one of its responsibles.
func TestLinkDecidesAnOfficeApprovalOnlyForItsResponsible(t *testing.T) {
	ctx := context.Background()
	s, fa, _, _ := newSvc(t, officeApproval())
	s.Approvers = func(context.Context, string, domain.Approval) (ApprovalAudience, error) {
		return ApprovalAudience{Office: true, Emails: []string{"ana@example.com"}}, nil
	}
	boss, _ := s.tokenFor("o1", "ap1", DecisionApprove, "boss@example.com")
	if _, _, err := s.Act(ctx, boss); !errors.Is(err, ErrNeedsApp) || fa.decided != 0 {
		t.Fatalf("a non-responsible's link: %v (decided %d)", err, fa.decided)
	}
	ana, _ := s.tokenFor("o1", "ap1", DecisionApprove, "ana@example.com")
	if _, _, err := s.Act(ctx, ana); err != nil || fa.decided != 1 {
		t.Fatalf("the responsible's link: %v (decided %d)", err, fa.decided)
	}
	s.Approvers = func(context.Context, string, domain.Approval) (ApprovalAudience, error) {
		return ApprovalAudience{}, errors.New("down")
	}
	fa.ap.Status = domain.ApprovalPending
	again, _ := s.tokenFor("o1", "ap1", DecisionApprove, "ana@example.com")
	if _, _, err := s.Act(ctx, again); !errors.Is(err, ErrNeedsApp) {
		t.Fatalf("an error must fail closed: %v", err)
	}
}

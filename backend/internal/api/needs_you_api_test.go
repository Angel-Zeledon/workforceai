package api_test

import (
	"context"
	"encoding/json"
	"slices"
	"sort"
	"testing"
	"time"

	"aiworkforce/backend/internal/api"
	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/needsyou"
	"aiworkforce/backend/internal/trust"
)

// "Te necesitan" (docs/architecture/needs-you.md). The fixture is officeApprovals: Legal (open, led by ana, a
// member) with the agent legal; Finanzas (open, no responsible) with accounting; Privada (private, led by carla, an
// admin) with hr; sales in General. beto is an admin outside every office; the owner is in none either.
type needsYouEnv struct {
	*officeApprovals
	trust  *trust.MemStore
	viewer string
}

func newNeedsYou(t *testing.T) *needsYouEnv {
	t.Helper()
	st := trust.NewMemStore()
	p := newOfficeApprovals(t, func(o *opts) {
		o.questions, o.initiative = true, true
		o.deps = func(d *api.Deps) {
			d.Trust = trust.New(trust.Config{Store: st, Agents: d.Store, OrgID: d.Cfg.OrgID})
		}
	})
	n := &needsYouEnv{officeApprovals: p, trust: st}
	n.viewer = p.e.member(p.owner, "vera-ny@example.com", auth.RoleViewer)
	return n
}

func (n *needsYouEnv) question(id, agent, askedFor string, at time.Time) {
	n.e.t.Helper()
	if err := n.e.store.CreateQuestion(context.Background(), n.org, application.Question{ID: id, TaskID: "t-" + id, RequestID: "r-" + id,
		AgentID: agent, Kind: "data", Need: "falta el dato " + id, Status: application.QuestionOpen, AskedFor: askedFor, CreatedAt: at}); err != nil {
		n.e.t.Fatal(err)
	}
}

func (n *needsYouEnv) suggestion(agent, key string) string {
	n.e.t.Helper()
	out, err := n.e.init.Raise(application.WithOrg(context.Background(), n.org), application.RaiseInput{AgentID: agent, Signal: "task_failed",
		SignalKey: key, Title: "La tarea " + key + " falló", Body: "¿La reviso?", Prompt: "Revisa " + key})
	if err != nil || !out.Raised {
		n.e.t.Fatalf("raise %s: %+v %v", key, out, err)
	}
	return out.Initiative.ID
}

func (n *needsYouEnv) proposal(id, agent string) {
	n.e.t.Helper()
	if err := n.trust.CreateProposal(context.Background(), n.org, trust.Proposal{ID: id, AgentID: agent, Kind: trust.KindPromotion,
		FromLevel: trust.LevelApproveEach, ToLevel: trust.LevelRules, Status: trust.StatusPending, CreatedAt: time.Now().UTC()}); err != nil {
		n.e.t.Fatal(err)
	}
}

func (n *needsYouEnv) list(token string) needsyou.Result {
	n.e.t.Helper()
	resp, raw := n.e.do("GET", api1+"/needs-you", token, nil, nil)
	var res needsyou.Result
	if resp.StatusCode != 200 || json.Unmarshal(raw, &res) != nil {
		n.e.t.Fatalf("GET /needs-you = %d %s", resp.StatusCode, raw)
	}
	return res
}

func (n *needsYouEnv) count(token string) needsyou.Result {
	n.e.t.Helper()
	resp, raw := n.e.do("GET", api1+"/needs-you/count", token, nil, nil)
	var res needsyou.Result
	if resp.StatusCode != 200 || json.Unmarshal(raw, &res) != nil {
		n.e.t.Fatalf("GET /needs-you/count = %d %s", resp.StatusCode, raw)
	}
	if len(res.Items) != 0 {
		n.e.t.Fatalf("the counter carries items: %s", raw)
	}
	return res
}

func keys(res needsyou.Result) []string {
	out := []string{}
	for _, it := range res.Items {
		out = append(out, it.Kind+":"+it.ID)
	}
	sort.Strings(out)
	return out
}

func sorted(s ...string) []string { sort.Strings(s); return s }

func TestNeedsYouFollowsTheRulesOfEachSource(t *testing.T) {
	n := newNeedsYou(t)
	for id, agent := range map[string]string{"ap-legal": "legal", "ap-hr": "hr", "ap-sales": "sales", "ap-fin": "accounting"} {
		n.approval(id, agent)
	}
	old := time.Now().UTC().Add(-time.Hour)
	beto, ana := n.e.userID(n.beto), n.e.userID(n.ana)
	n.question("q-hr", "hr", "", old)          // nobody launched it: owners and admins, of the private office only
	n.question("q-legal", "legal", "", old)    // nobody launched it: owners and admins
	n.question("q-beto", "sales", beto, old)   // for beto
	n.question("q-ana", "legal", ana, old)     // for ana
	n.question("q-ana-hr", "hr", ana, old)     // for ana, of the private office: she launched it, so she sees it
	inHR, inSales := n.suggestion("hr", "a"), n.suggestion("sales", "b")
	n.proposal("tp-hr", "hr")
	n.proposal("tp-sales", "sales")

	cases := []struct {
		who   string
		token string
		want  []string
	}{
		// an admin outside every office: nothing of the private office, not the approval Legal's responsible decides
		{"beto", n.beto, sorted("approval:ap-sales", "approval:ap-fin", "question:q-legal", "question:q-beto",
			"suggestion:"+inSales, "trust:tp-sales")},
		// the responsible of the private office (an admin): its work too
		{"carla", n.carla, sorted("approval:ap-hr", "approval:ap-sales", "approval:ap-fin", "question:q-hr", "question:q-legal",
			"suggestion:"+inHR, "suggestion:"+inSales, "trust:tp-hr", "trust:tp-sales")},
		// a member who leads Legal: Legal's approval (no approvals:decide needed), her questions; no trust proposal
		{"ana", n.ana, sorted("approval:ap-legal", "question:q-ana", "question:q-ana-hr", "suggestion:"+inSales)},
		// the owner is not in the private office either
		{"owner", n.owner.AccessToken, sorted("approval:ap-sales", "approval:ap-fin", "question:q-legal",
			"suggestion:"+inSales, "trust:tp-sales")},
		// a viewer resolves nothing
		{"viewer", n.viewer, []string{}},
	}
	for _, c := range cases {
		res := n.list(c.token)
		if got := keys(res); !slices.Equal(got, c.want) {
			t.Errorf("%s sees %v, want %v", c.who, got, c.want)
		}
		cnt := n.count(c.token)
		if cnt.Total != len(c.want) || cnt.Total != res.Total {
			t.Errorf("%s: count %d, list %d, want %d", c.who, cnt.Total, res.Total, len(c.want))
		}
		for k, v := range res.Counts {
			if cnt.Counts[k] != v {
				t.Errorf("%s: count of %s = %d, list %d", c.who, k, cnt.Counts[k], v)
			}
		}
		if len(res.Unavailable) != 0 {
			t.Errorf("%s: unavailable sources %v", c.who, res.Unavailable)
		}
	}
}

func TestNeedsYouItemsSayWhereAndHow(t *testing.T) {
	n := newNeedsYou(t)
	n.approval("ap-sales", "sales")
	n.e.store.CreateApproval(context.Background(), n.org, domain.Approval{ID: "ap-big", TaskID: "t-big", AgentID: "sales", Action: "pay",
		Title: "Pagar la factura", Risk: "high", Status: domain.ApprovalPending, CreatedAt: time.Now().UTC(), RequiredApprovals: 1,
		Decisions: []domain.ApprovalDecision{}})
	n.question("q-legal", "legal", "", time.Now().UTC().Add(-48*time.Hour))
	n.proposal("tp-sales", "sales")
	res := n.list(n.beto)
	by := map[string]needsyou.Item{}
	for _, it := range res.Items {
		by[it.ID] = it
	}
	if it := by["ap-sales"]; it.Target != "/approvals/ap-sales" || !it.Direct || it.Title == "" || it.AgentName == "" || it.Urgency != needsyou.UrgencyNormal {
		t.Errorf("low-risk approval: %+v", it)
	}
	if it := by["ap-big"]; it.Direct || it.Urgency != needsyou.UrgencyHigh || it.Risk != "high" {
		t.Errorf("a high-risk approval is never approved from the list: %+v", it)
	}
	if it := by["q-legal"]; it.Target != "/approvals/inbox" || it.Urgency != needsyou.UrgencyHigh || it.OfficeName != "Legal" || it.Title == "" {
		t.Errorf("question waiting two days: %+v", it)
	}
	if it := by["tp-sales"]; it.Direct || it.ProposalKind != trust.KindPromotion || it.ToLevel != trust.LevelRules || it.Urgency != needsyou.UrgencyLow {
		t.Errorf("trust proposal: %+v", it)
	}
	// the most urgent first: the old question and the high-risk approval lead
	if len(res.Items) != 4 || res.Items[0].Urgency != needsyou.UrgencyHigh || res.Items[1].Urgency != needsyou.UrgencyHigh {
		t.Errorf("order: %+v", res.Items)
	}
}

func TestNeedsYouWithoutAuthIsTheOwnersDemo(t *testing.T) {
	e := newEnv(t, opts{questions: true, initiative: true})
	now := time.Now().UTC()
	if err := e.store.CreateApproval(context.Background(), domain.DemoOrgID, domain.Approval{ID: "ap-demo", TaskID: "t", AgentID: "sales", Action: "send",
		Title: "Enviar", Risk: "low", Status: domain.ApprovalPending, CreatedAt: now, RequiredApprovals: 1, Decisions: []domain.ApprovalDecision{}}); err != nil {
		t.Fatal(err)
	}
	resp, raw := e.do("GET", api1+"/needs-you", "", nil, nil)
	var res needsyou.Result
	if resp.StatusCode != 200 || json.Unmarshal(raw, &res) != nil || res.Total != 1 || res.Items[0].ID != "ap-demo" {
		t.Fatalf("demo: %d %s", resp.StatusCode, raw)
	}
}

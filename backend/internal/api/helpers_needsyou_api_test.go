package api_test

import (
	"encoding/json"
	"testing"

	"aiworkforce/backend/internal/domain"
)

// "Te necesitan": an open helper proposal is pending for who asked for the request.

func (e *env) helperRaw(method, path, token string) []byte {
	e.t.Helper()
	resp, raw := e.do(method, path, token, nil, nil)
	if resp.StatusCode != 200 {
		e.t.Fatalf("%s %s = %d: %s", method, path, resp.StatusCode, raw)
	}
	return raw
}

func TestNeedsYouListsTheOpenProposalForTheAsker(t *testing.T) {
	e := newEnv(t, opts{cfg: helpersCfg})
	seedProposal(t, e, domain.DemoOrgID, "r4", "sales")
	var res struct {
		Total  int `json:"total"`
		Counts map[string]int
		Items  []struct{ Kind, ID, Title, Target string }
	}
	if raw := e.helperRaw("GET", api1+"/needs-you", ""); json.Unmarshal(raw, &res) != nil || res.Total != 1 || res.Items[0].Kind != "helpers" || res.Items[0].ID != "r4" || res.Counts["helpers"] != 1 {
		t.Fatalf("demo: %s", raw)
	}
	e.helperCall("POST", api1+"/requests/r4/helpers/decline", "", map[string]any{}, 200)
	if raw := e.helperRaw("GET", api1+"/needs-you/count", ""); json.Unmarshal(raw, &res) != nil || res.Total != 0 {
		t.Fatalf("an answered proposal is not pending any more: %s", raw)
	}
	off := newEnv(t, opts{})
	seedProposal(t, off, domain.DemoOrgID, "r5", "sales")
	if raw := off.helperRaw("GET", api1+"/needs-you/count", ""); json.Unmarshal(raw, &res) != nil || res.Total != 0 {
		t.Fatalf("with the flag off nothing is listed: %s", raw)
	}
}

func TestNeedsYouProposalIsOnlyTheAskers(t *testing.T) {
	p := newPrivateWork(t, helpersOn)
	seedProposal(t, p.e, p.owner.OrgID, "req-sales", "sales")
	var res struct{ Total int }
	for _, tok := range []string{p.ana, p.beto, p.owner.AccessToken} { // nobody asked for it (no run meta): it is not theirs to answer
		if raw := p.e.helperRaw("GET", api1+"/needs-you/count", tok); json.Unmarshal(raw, &res) != nil || res.Total != 0 {
			t.Fatalf("someone else's proposal must not be listed: %s", raw)
		}
	}
}

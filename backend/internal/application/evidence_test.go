package application_test

import (
	"context"
	"regexp"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

// B6: a figure is only linked to a source the task really saw; anything the
// runtime claims about status or source is discarded and recomputed.
func TestCitationsAreVerifiedOnlyAgainstWhatTheTaskSaw(t *testing.T) {
	rt := &fakeRuntime{plan: func(application.PlanRequest) (application.PlanResponse, error) {
		return application.PlanResponse{Tasks: []application.PlannedTask{
			{Key: "a", Title: "Margen", AgentID: "accounting"},
			{Key: "b", Title: "Informe", AgentID: "analyst", DependsOn: []string{"a"}},
		}}, nil
	}}
	rt.runTask = func(in application.RunTaskRequest) (application.RunTaskResponse, error) {
		if in.Task.Title == "Margen" {
			res := okResult("El margen real es 24%, no 31%.")
			res.Output.Metrics = map[string]any{"ticket": "$50,000"}
			return res, nil
		}
		dep := in.Context.DependencyOutputs[0].TaskID
		res := okResult("Informe con margen de 24%")
		forged := time.Now()
		res.Output.Citations = []domain.Citation{
			{Value: "24%", Label: "margen real", Ref: "task:" + dep},
			{Value: "$50,000", Label: "ticket", Ref: "task:" + dep},
			{Value: "7 puntos", Label: "diferencia", Ref: "task:" + dep}, // derived: source seen, figure not in it
			{Value: "$9,999", Ref: "task:invented-task", Status: domain.CitationVerified,
				Source: &domain.CitationSource{TaskID: "invented-task", ReadAt: &forged}}, // forged verdict
			{Value: "$1", Ref: "memory:never-recalled"},
			{Value: "$2", Ref: "conn:email:msg-1"},
			{Value: "$3", Ref: "web:https://example.test"},
			{Value: "sin cifra", Ref: "task:" + dep}, // not a figure: dropped
		}
		return res, nil
	}
	h := newHarness(t, rt, nil)
	reqID, err := h.orch.Submit(context.Background(), "margen del cliente")
	if err != nil {
		t.Fatal(err)
	}
	h.waitFor("request done", func() bool { return h.request(reqID).Status == domain.RequestDone })
	tasks := h.tasks(reqID)
	depID := tasks["Margen"].ID
	cites := tasks["Informe"].Output.Citations
	if len(cites) != 7 {
		t.Fatalf("citations kept: %d %+v", len(cites), cites)
	}
	want := []struct {
		status, reason string
		checked        bool
	}{
		{domain.CitationVerified, "", true},
		{domain.CitationVerified, "", true},
		{domain.CitationVerified, "", false},
		{domain.CitationUnverified, application.ReasonNotSeen, false},
		{domain.CitationUnverified, application.ReasonNotSeen, false},
		{domain.CitationUnverified, application.ReasonNotSeen, false},
		{domain.CitationUnverified, application.ReasonUnknownKind, false},
	}
	for i, w := range want {
		c := cites[i]
		if c.Status != w.status || c.Reason != w.reason || c.ValueChecked != w.checked {
			t.Errorf("citation %d (%s): %+v", i, c.Ref, c)
		}
		if w.status == domain.CitationVerified && (c.Source == nil || c.Source.TaskID != depID || c.Source.AgentID != "accounting" || c.Kind != "task") {
			t.Errorf("citation %d source: %+v", i, c.Source)
		}
		if w.status != domain.CitationVerified && c.Source != nil {
			t.Errorf("citation %d is unverified but kept a source: %+v", i, c.Source)
		}
	}
	if !h.hasAudit("evidence.citations_checked") {
		t.Fatal("the citation check must be audited")
	}

	rep := h.request(reqID).ReportID
	if rep == nil {
		t.Fatal("no report")
	}
	ev, err := h.q.ReportEvidence(context.Background(), *rep)
	if err != nil {
		t.Fatal(err)
	}
	if ev.Verified != 3 || ev.Total != 7 || ev.RequestID != reqID || ev.Citations[0].TaskID != tasks["Informe"].ID {
		t.Fatalf("report evidence: %+v", ev)
	}
	for i, c := range ev.Citations {
		if (i < 3) != (c.Status == domain.CitationVerified) {
			t.Fatalf("verified citations must come first: %+v", ev.Citations)
		}
	}
}

var blockID = regexp.MustCompile(`<untrusted_data id="([^"]+)"`)

// B6: a connector citation must name an item one of the task's reads returned.
func TestConnectorCitationMustMatchAnItemTheTaskRead(t *testing.T) {
	spy := &spyRuntime{fakeRuntime: &fakeRuntime{plan: onePlan("sales")}}
	var itemID string
	spy.fakeRuntime.runTask = func(in application.RunTaskRequest) (application.RunTaskResponse, error) {
		res := okResult("revisado")
		if len(in.ExternalContent) == 0 {
			res.ToolRequests = []application.ToolRequest{{Tool: "email", Action: "search", Args: map[string]any{"q": ""}, Risk: "low"}}
			return res, nil
		}
		if m := blockID.FindStringSubmatch(in.ExternalContent[0]); m != nil {
			itemID = m[1]
		}
		res.Output.Citations = []domain.Citation{
			{Value: "$4,200", Label: "depósito", Ref: "conn:email:" + itemID},
			{Value: "$4,200", Label: "depósito", Ref: "conn:email:made-up-id"},
		}
		return res, nil
	}
	w := wire(t, spy)
	read := w.conn("Gmail lectura", "mail.read")
	w.grant(read, "sales", "mail.read")
	reqID, err := w.orch.Submit(context.Background(), "revisa el correo del banco")
	if err != nil {
		t.Fatal(err)
	}
	w.waitFor("request done", func() bool { return w.request(reqID).Status == domain.RequestDone })
	if itemID == "" {
		t.Fatal("the read returned no item")
	}
	cites := w.tasks(reqID)["Responder correos"].Output.Citations
	if len(cites) != 2 {
		t.Fatalf("citations: %+v", cites)
	}
	ok := cites[0]
	if ok.Status != domain.CitationVerified || ok.Source == nil || ok.Source.Provider != "google_gmail" || ok.Source.Tool != "email" ||
		ok.Source.ItemID != itemID || ok.Source.ReadAt == nil || ok.ValueChecked {
		t.Fatalf("read item citation: %+v %+v", ok, ok.Source)
	}
	if bad := cites[1]; bad.Status != domain.CitationUnverified || bad.Reason != application.ReasonNotSeen || bad.Source != nil {
		t.Fatalf("made-up item citation: %+v", bad)
	}
}

package api

import (
	"encoding/json"
	"strings"
	"testing"

	"aiworkforce/backend/internal/artifacts"
	"aiworkforce/backend/internal/projects"
)

func ptr(s string) *string { return &s }

// D3: a project's nodes of a private office's agent keep their place, without their content; a project that is
// only the private office's work is hidden whole.
func TestRedactProject(t *testing.T) {
	sees := func(id string) bool { return id != "legal" }
	d := projects.Detail{
		Nodes: []projects.Node{
			{ID: "n1", Title: "contrato secreto", AgentID: ptr("legal"), State: "done", Error: ptr("cláusula secreta"),
				Files: []string{"/Offices/Legal/x.md"}, TaskID: "t-legal", DelegationChain: []string{"legal"}, ApprovalAction: "firmar_secreto"},
			{ID: "n2", Title: "cotización", AgentID: ptr("sales"), State: "running"},
			{ID: "root", Title: "Proyecto", Kind: "objective"},
		},
		Approvals: []projects.Approval{{ID: "a1", AgentID: ptr("legal"), Title: "firmar secreto"}, {ID: "a2", AgentID: ptr("sales")}},
	}
	if redactProject(&d, sees) {
		t.Fatal("a mixed project is not hidden whole")
	}
	b, _ := json.Marshal(d)
	if strings.Contains(string(b), "secret") || strings.Contains(string(b), "legal") || strings.Contains(string(b), "Legal") {
		t.Fatalf("redacted project = %s", b)
	}
	if d.Nodes[0].ID != "n1" || d.Nodes[0].State != "done" || d.Nodes[1].Title != "cotización" || len(d.Approvals) != 1 {
		t.Fatalf("redacted project lost the visible plan: %s", b)
	}
	only := projects.Detail{Nodes: []projects.Node{{ID: "n1", AgentID: ptr("legal")}, {ID: "root"}}}
	if !redactProject(&only, sees) {
		t.Fatal("a project of the private office only is hidden")
	}
	none := projects.Detail{Nodes: []projects.Node{{ID: "root"}}}
	if redactProject(&none, sees) {
		t.Fatal("a project without agents yet is not hidden")
	}
}

func TestRedactWorkspace(t *testing.T) {
	sees := func(id string) bool { return id != "legal" }
	ws := artifacts.ProjectWorkspace{
		Deliverables: []artifacts.Deliverable{
			{DeliverableID: "d1", Title: "dictamen secreto", AgentID: "legal", Artifacts: []artifacts.Meta{{ID: "art-legal"}}},
			{DeliverableID: "d2", Title: "cotización", AgentID: "sales", Artifacts: []artifacts.Meta{{ID: "art-sales"}}},
		},
		Links: []artifacts.Link{{ID: "l1", From: "art-sales", To: "art-legal"}, {ID: "l2", From: "art-sales", To: "art-sales"}},
	}
	if redactWorkspace(&ws, sees) || len(ws.Deliverables) != 1 || ws.Deliverables[0].AgentID != "sales" || len(ws.Links) != 1 || ws.Links[0].ID != "l2" {
		t.Fatalf("workspace = %+v", ws)
	}
	only := artifacts.ProjectWorkspace{Deliverables: []artifacts.Deliverable{{AgentID: "legal"}}}
	if !redactWorkspace(&only, sees) {
		t.Fatal("a workspace of the private office only is hidden")
	}
}

package projects

import (
	"testing"

	"aiworkforce/backend/internal/domain"
)

func leaf(id, obj, state string, deps ...string) Node {
	return Node{ID: id, ObjectiveID: obj, Kind: KindTask, State: state, DependsOn: deps}
}

func TestProjectPartialWhenObjectiveOmitted(t *testing.T) {
	skipped := leaf("a", "o1", StateDone)
	skipped.Skipped = true
	omitted := leaf("b", "o1", StateCancelled, "a")
	omitted.Omitted = true
	nodes := []Node{skipped, omitted, leaf("c", "o2", StateDone)}
	c := countStates(nodes)
	req := &domain.Request{Status: domain.RequestDone}
	if st := deriveStatus(Record{}, req, nodes, nil, c); st != StatusPartial {
		t.Fatalf("status = %s, want partial (objective o1 has no result, o2 delivered)", st)
	}
	if lost := objectivesLost(nodes); len(lost) != 1 || lost[0] != "o1" {
		t.Fatalf("lost = %v", lost)
	}
	// skipping a step nothing essential depends on: the project is simply done
	extra := leaf("d", "o2", StateDone, "c")
	side := leaf("e", "o2", StateDone)
	side.Skipped = true
	nodes2 := []Node{leaf("c", "o2", StateDone), extra, side}
	nodes2[2].DependsOn = nil
	nodes2[1].DependsOn = []string{"c"}
	// e is a final node that was skipped, but d (also final) delivered
	if st := deriveStatus(Record{}, req, nodes2, nil, countStates(nodes2)); st != StatusDone {
		t.Fatalf("status = %s, want done", st)
	}
	if !terminalStatus(StatusPartial) {
		t.Fatal("partial is a final status")
	}
}

func TestNeedsInputIsWaitingHuman(t *testing.T) {
	nodes := []Node{leaf("a", "o1", StateNeedsInput), leaf("b", "o1", StatePending, "a")}
	c := countStates(nodes)
	if c.needsInput != 1 {
		t.Fatalf("needs input = %d", c.needsInput)
	}
	if st := deriveStatus(Record{}, &domain.Request{Status: domain.RequestRunning}, nodes, nil, c); st != StatusWaitingHuman {
		t.Fatalf("status = %s", st)
	}
	if g := rollupState(nodes); g != StateNeedsInput {
		t.Fatalf("group = %s", g)
	}
}

func TestNodeShowsTheFilesItsTaskWrote(t *testing.T) {
	task := domain.Task{ID: "t1", Status: domain.TaskDone, Output: &domain.StructuredOutput{Files: []string{"/Clients/ACME/carta.docx"}}}
	svc := &Service{}
	rec := Record{ID: "p", RequestID: "r", Nodes: []NodeDef{{ID: "n1", Kind: KindTask, Title: "Carta"}}}
	d := svc.build(t.Context(), snapshot{rec: rec, tasks: map[string]domain.Task{"n1": task}, taskNode: map[string]string{"t1": "n1"}})
	if len(d.Nodes) != 1 || len(d.Nodes[0].Files) != 1 || d.Nodes[0].Files[0] != "/Clients/ACME/carta.docx" {
		t.Fatalf("nodes = %+v", d.Nodes)
	}
}

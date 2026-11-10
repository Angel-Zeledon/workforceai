package application

import (
	"slices"
	"testing"
)

func TestAddressedAgents(t *testing.T) {
	rs := &run{chat: &chatLink{speaker: "a-legal", reassignTo: "a-acct"},
		preset: &PlanResponse{Tasks: []PlannedTask{{AgentID: "a-plan"}, {AgentID: assistantID}}}}
	if got := addressedAgents(rs); !slices.Equal(got, []string{"a-legal", "a-acct", "a-plan"}) {
		t.Errorf("addressed: %v", got)
	}
	if got := addressedAgents(&run{}); len(got) != 0 {
		t.Errorf("a request addressed to nobody: %v", got)
	}
}

package offices

import (
	"context"
	"sort"
)

func (m *MemStore) ListAgentOffices(_ context.Context, org string) ([]AgentOffice, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]AgentOffice, 0)
	for _, a := range sub(m.agentOf, org) {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].AgentID != out[j].AgentID {
			return out[i].AgentID < out[j].AgentID
		}
		return out[i].OfficeID < out[j].OfficeID
	})
	return out, nil
}

func (m *MemStore) PutAgentOffice(_ context.Context, org string, a AgentOffice) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	sub(m.agentOf, org)[a.AgentID+"|"+a.OfficeID] = a
	return nil
}

func (m *MemStore) DeleteAgentOffice(_ context.Context, org, agentID, officeID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(sub(m.agentOf, org), agentID+"|"+officeID)
	return nil
}

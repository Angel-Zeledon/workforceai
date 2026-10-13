package sandbox

import "context"

// CountAutoRunsOfTask implements TaskRunCounter: desk-mode runs of the task that started.
func (m *MemoryStore) CountAutoRunsOfTask(_ context.Context, org, taskID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, r := range m.runs {
		if r.OrgID == org && r.TaskID == taskID && r.AutoRun && r.StartedAt != nil {
			n++
		}
	}
	return n, nil
}

var _ TaskRunCounter = (*MemoryStore)(nil)

package postgres

import (
	"context"

	"aiworkforce/backend/internal/application"
)

// Run meta and task checkpoints (durable execution, application/durable.go;
// migration 290_durable_runs.sql).

var _ application.RunStore = (*Store)(nil)

func (s *Store) PutRunMeta(ctx context.Context, orgID string, m application.RunMeta) error {
	return s.exec(ctx, orgID, `INSERT INTO request_runs (org_id, request_id, requested_by, conversation_id, read_only, removed_task_ids, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,now())
		ON CONFLICT (org_id, request_id) DO UPDATE SET requested_by=EXCLUDED.requested_by, conversation_id=EXCLUDED.conversation_id,
			read_only=EXCLUDED.read_only, removed_task_ids=EXCLUDED.removed_task_ids, updated_at=now()`,
		orgID, m.RequestID, m.RequestedBy, m.ConversationID, m.ReadOnly, jb(strs(m.RemovedTaskIDs)))
}

func (s *Store) GetRunMeta(ctx context.Context, orgID, requestID string) (application.RunMeta, error) {
	m, err := one(ctx, s, orgID, func(r scanner) (application.RunMeta, error) {
		var m application.RunMeta
		var removed []byte
		err := r.Scan(&m.RequestID, &m.RequestedBy, &m.ConversationID, &m.ReadOnly, &removed, &m.UpdatedAt)
		unmarshal(removed, &m.RemovedTaskIDs)
		return m, err
	}, `SELECT request_id, requested_by, conversation_id, read_only, removed_task_ids, updated_at FROM request_runs WHERE org_id=$1 AND request_id=$2`, orgID, requestID)
	return m, mapErr(err)
}

func (s *Store) PutCheckpoint(ctx context.Context, orgID string, c application.TaskCheckpoint) error {
	var taint []byte
	if c.Taint != nil {
		taint = jb(c.Taint)
	}
	tools := c.Tools
	if tools == nil {
		tools = []application.ToolRequest{}
	}
	return s.exec(ctx, orgID, `INSERT INTO task_checkpoints (org_id, task_id, request_id, approval_id, tool_index, tools, output, taint, executed, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,now())
		ON CONFLICT (org_id, task_id) DO UPDATE SET request_id=EXCLUDED.request_id, approval_id=EXCLUDED.approval_id, tool_index=EXCLUDED.tool_index,
			tools=EXCLUDED.tools, output=EXCLUDED.output, taint=EXCLUDED.taint, executed=EXCLUDED.executed, updated_at=now()`,
		orgID, c.TaskID, c.RequestID, c.ApprovalID, c.ToolIndex, jb(tools), jb(c.Output), taint, c.Executed)
}

func (s *Store) GetCheckpoint(ctx context.Context, orgID, taskID string) (application.TaskCheckpoint, error) {
	c, err := one(ctx, s, orgID, func(r scanner) (application.TaskCheckpoint, error) {
		var c application.TaskCheckpoint
		var tools, output, taint []byte
		err := r.Scan(&c.TaskID, &c.RequestID, &c.ApprovalID, &c.ToolIndex, &tools, &output, &taint, &c.Executed, &c.UpdatedAt)
		unmarshal(tools, &c.Tools)
		unmarshal(output, &c.Output)
		if len(taint) > 0 {
			c.Taint = &application.CheckpointTaint{}
			unmarshal(taint, c.Taint)
		}
		return c, err
	}, `SELECT task_id, request_id, approval_id, tool_index, tools, output, taint, executed, updated_at FROM task_checkpoints WHERE org_id=$1 AND task_id=$2`, orgID, taskID)
	return c, mapErr(err)
}

func (s *Store) DeleteCheckpoint(ctx context.Context, orgID, taskID string) error {
	return s.exec(ctx, orgID, `DELETE FROM task_checkpoints WHERE org_id=$1 AND task_id=$2`, orgID, taskID)
}

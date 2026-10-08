package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"aiworkforce/backend/internal/application"
)

// Run meta, task checkpoints and executed approvals (durable execution,
// application/durable.go; migrations 290_durable_runs.sql and 350_durable_resume.sql).

var (
	_ application.RunStore        = (*Store)(nil)
	_ application.ExecutionLedger = (*Store)(nil)
)

// jbOpt is jb for optional values: nil is stored as SQL NULL.
func jbOpt[T any](v *T) []byte {
	if v == nil {
		return nil
	}
	return jb(v)
}

func (s *Store) PutRunMeta(ctx context.Context, orgID string, m application.RunMeta) error {
	return s.exec(ctx, orgID, `INSERT INTO request_runs (org_id, request_id, requested_by, conversation_id, read_only, removed_task_ids, gate, chat, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,now())
		ON CONFLICT (org_id, request_id) DO UPDATE SET requested_by=EXCLUDED.requested_by, conversation_id=EXCLUDED.conversation_id,
			read_only=EXCLUDED.read_only, removed_task_ids=EXCLUDED.removed_task_ids, gate=EXCLUDED.gate, chat=EXCLUDED.chat, updated_at=now()`,
		orgID, m.RequestID, m.RequestedBy, m.ConversationID, m.ReadOnly, jb(strs(m.RemovedTaskIDs)), jbOpt(m.Gate), jbOpt(m.Chat))
}

func (s *Store) GetRunMeta(ctx context.Context, orgID, requestID string) (application.RunMeta, error) {
	m, err := one(ctx, s, orgID, func(r scanner) (application.RunMeta, error) {
		var m application.RunMeta
		var removed, gate, chat []byte
		err := r.Scan(&m.RequestID, &m.RequestedBy, &m.ConversationID, &m.ReadOnly, &removed, &gate, &chat, &m.UpdatedAt)
		unmarshal(removed, &m.RemovedTaskIDs)
		if len(gate) > 0 {
			m.Gate = &application.RunGate{}
			unmarshal(gate, m.Gate)
		}
		if len(chat) > 0 {
			m.Chat = &application.ChatLinkMeta{}
			unmarshal(chat, m.Chat)
		}
		return m, err
	}, `SELECT request_id, requested_by, conversation_id, read_only, removed_task_ids, gate, chat, updated_at FROM request_runs WHERE org_id=$1 AND request_id=$2`, orgID, requestID)
	return m, mapErr(err)
}

func (s *Store) PutCheckpoint(ctx context.Context, orgID string, c application.TaskCheckpoint) error {
	tools := c.Tools
	if tools == nil {
		tools = []application.ToolRequest{}
	}
	return s.exec(ctx, orgID, `INSERT INTO task_checkpoints (org_id, task_id, request_id, approval_id, tool_index, tools, output, taint, gateway, executed, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,now())
		ON CONFLICT (org_id, task_id) DO UPDATE SET request_id=EXCLUDED.request_id, approval_id=EXCLUDED.approval_id, tool_index=EXCLUDED.tool_index,
			tools=EXCLUDED.tools, output=EXCLUDED.output, taint=EXCLUDED.taint, gateway=EXCLUDED.gateway, executed=EXCLUDED.executed, updated_at=now()`,
		orgID, c.TaskID, c.RequestID, c.ApprovalID, c.ToolIndex, jb(tools), jb(c.Output), jbOpt(c.Taint), jbOpt(c.Gateway), c.Executed)
}

func (s *Store) GetCheckpoint(ctx context.Context, orgID, taskID string) (application.TaskCheckpoint, error) {
	c, err := one(ctx, s, orgID, func(r scanner) (application.TaskCheckpoint, error) {
		var c application.TaskCheckpoint
		var tools, output, taint, gw []byte
		err := r.Scan(&c.TaskID, &c.RequestID, &c.ApprovalID, &c.ToolIndex, &tools, &output, &taint, &gw, &c.Executed, &c.UpdatedAt)
		unmarshal(tools, &c.Tools)
		unmarshal(output, &c.Output)
		if len(taint) > 0 {
			c.Taint = &application.CheckpointTaint{}
			unmarshal(taint, c.Taint)
		}
		if len(gw) > 0 {
			c.Gateway = &application.GatewayPending{}
			unmarshal(gw, c.Gateway)
		}
		return c, err
	}, `SELECT task_id, request_id, approval_id, tool_index, tools, output, taint, gateway, executed, updated_at FROM task_checkpoints WHERE org_id=$1 AND task_id=$2`, orgID, taskID)
	return c, mapErr(err)
}

func (s *Store) DeleteCheckpoint(ctx context.Context, orgID, taskID string) error {
	return s.exec(ctx, orgID, `DELETE FROM task_checkpoints WHERE org_id=$1 AND task_id=$2`, orgID, taskID)
}

// ClaimExecution inserts the execution record of an approval; the primary key
// (org_id, approval_id) lets exactly one caller win, in any instance.
func (s *Store) ClaimExecution(ctx context.Context, orgID string, e application.ApprovalExecution) (bool, application.ApprovalExecution, error) {
	if e.Status == "" {
		e.Status = application.ExecutionClaimed
	}
	claimed := false
	var prev application.ApprovalExecution
	err := s.WithOrgTx(ctx, orgID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `INSERT INTO approval_executions (org_id, approval_id, task_id, tool, action, args_hash, status, claimed_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,now()) ON CONFLICT (org_id, approval_id) DO NOTHING`,
			orgID, e.ApprovalID, e.TaskID, e.Tool, e.Action, e.ArgsHash, e.Status)
		if err != nil {
			return err
		}
		claimed = tag.RowsAffected() == 1
		return tx.QueryRow(ctx, `SELECT approval_id, task_id, tool, action, args_hash, status, hold_id, claimed_at, finished_at
			FROM approval_executions WHERE org_id=$1 AND approval_id=$2`, orgID, e.ApprovalID).
			Scan(&prev.ApprovalID, &prev.TaskID, &prev.Tool, &prev.Action, &prev.ArgsHash, &prev.Status, &prev.HoldID, &prev.ClaimedAt, &prev.FinishedAt)
	})
	return claimed, prev, err
}

// FinishExecution stores the outcome of a claimed execution.
func (s *Store) FinishExecution(ctx context.Context, orgID, approvalID, status, holdID string) error {
	tag, err := s.execTag(ctx, orgID, `UPDATE approval_executions SET status=$3, hold_id=$4, finished_at=$5 WHERE org_id=$1 AND approval_id=$2`,
		orgID, approvalID, status, holdID, time.Now().UTC())
	if err == nil && tag.RowsAffected() == 0 {
		return mapErr(pgx.ErrNoRows)
	}
	return err
}

package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

// V2: work profiles (470), thought-audit traces (471) and steer notes (472).
// Every call runs through WithOrgTx, so Row-Level Security applies.

var _ application.V2Store = (*Store)(nil)

func scanProfile(r scanner) (application.AgentProfile, error) {
	var p application.AgentProfile
	var skills []byte
	var at time.Time
	if err := r.Scan(&p.AgentID, &p.Instructions, &p.Creativity, &skills, &p.Tone, &p.Version, &p.UpdatedBy, &at); err != nil {
		return p, err
	}
	unmarshal(skills, &p.Skills)
	if p.Skills == nil {
		p.Skills = []string{}
	}
	p.UpdatedAt = &at
	return p, nil
}

const profileCols = `agent_id, instructions, creativity, skills, tone, version, updated_by, updated_at`

func (s *Store) GetAgentProfile(ctx context.Context, orgID, agentID string) (application.AgentProfile, bool, error) {
	p, err := one(ctx, s, orgID, scanProfile, `SELECT `+profileCols+` FROM agent_profiles WHERE org_id=$1 AND agent_id=$2`, orgID, agentID)
	if err != nil {
		if mapErr(err) == domain.ErrNotFound {
			return application.AgentProfile{}, false, nil
		}
		return p, false, err
	}
	return p, true, nil
}

func (s *Store) SaveAgentProfile(ctx context.Context, orgID string, p application.AgentProfile, v application.ProfileVersion) error {
	return s.WithOrgTx(ctx, orgID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `INSERT INTO agent_profiles (org_id, agent_id, instructions, creativity, skills, tone, version, updated_by, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,now())
			ON CONFLICT (org_id, agent_id) DO UPDATE SET instructions=EXCLUDED.instructions, creativity=EXCLUDED.creativity,
				skills=EXCLUDED.skills, tone=EXCLUDED.tone, version=EXCLUDED.version, updated_by=EXCLUDED.updated_by, updated_at=now()
			WHERE agent_profiles.version = EXCLUDED.version - 1`,
			orgID, p.AgentID, p.Instructions, p.Creativity, jb(strs(p.Skills)), p.Tone, p.Version, p.UpdatedBy)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return domain.ErrConflict // two editors raced: the second one must reload
		}
		_, err = tx.Exec(ctx, `INSERT INTO agent_profile_versions (org_id, agent_id, version, author, restored_from, diff, snapshot, created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
			orgID, v.AgentID, v.Version, v.Author, v.RestoredFrom, jb(v.Diff), jb(v.Snapshot), v.CreatedAt)
		return err
	})
}

func scanProfileVersion(r scanner) (application.ProfileVersion, error) {
	var v application.ProfileVersion
	var diff, snap []byte
	if err := r.Scan(&v.AgentID, &v.Version, &v.Author, &v.RestoredFrom, &diff, &snap, &v.CreatedAt); err != nil {
		return v, err
	}
	unmarshal(diff, &v.Diff)
	unmarshal(snap, &v.Snapshot)
	if v.Diff == nil {
		v.Diff = map[string]application.ProfileChange{}
	}
	if v.Snapshot.Skills == nil {
		v.Snapshot.Skills = []string{}
	}
	return v, nil
}

const profileVersionCols = `agent_id, version, author, restored_from, diff, snapshot, created_at`

func (s *Store) ListProfileVersions(ctx context.Context, orgID, agentID string) ([]application.ProfileVersion, error) {
	return many(ctx, s, orgID, scanProfileVersion, `SELECT `+profileVersionCols+` FROM agent_profile_versions WHERE org_id=$1 AND agent_id=$2 ORDER BY version DESC`, orgID, agentID)
}

func (s *Store) GetProfileVersion(ctx context.Context, orgID, agentID string, version int) (application.ProfileVersion, error) {
	v, err := one(ctx, s, orgID, scanProfileVersion, `SELECT `+profileVersionCols+` FROM agent_profile_versions WHERE org_id=$1 AND agent_id=$2 AND version=$3`, orgID, agentID, version)
	return v, mapErr(err)
}

func (s *Store) ListAgentProfiles(ctx context.Context, orgID string) ([]application.AgentProfile, error) {
	return many(ctx, s, orgID, scanProfile, `SELECT `+profileCols+` FROM agent_profiles WHERE org_id=$1`, orgID)
}

func (s *Store) AddTrace(ctx context.Context, orgID string, t application.Trace) error {
	return s.WithOrgTx(ctx, orgID, func(tx pgx.Tx) error {
		if t.Attempt == 0 {
			var n int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM agent_traces WHERE org_id=$1 AND task_id=$2`, orgID, t.TaskID).Scan(&n); err != nil {
				return err
			}
			t.Attempt = n + 1
		}
		_, err := tx.Exec(ctx, `INSERT INTO agent_traces (id, org_id, agent_id, task_id, request_id, attempt, created_at, data)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, t.ID, orgID, t.AgentID, t.TaskID, t.RequestID, t.Attempt, t.CreatedAt, jb(t))
		return err
	})
}

func (s *Store) AppendTraceEvent(ctx context.Context, orgID, taskID string, e application.TraceEvent) (bool, error) {
	// A trace saved without events holds JSON null in data->'events' (not SQL NULL):
	// COALESCE does not catch it and jsonb_array_length(null) fails with "cannot get
	// array length of a scalar". Anything that is not an array counts as empty.
	tag, err := s.execTag(ctx, orgID, `UPDATE agent_traces SET data = jsonb_set(data, '{events}',
			(CASE WHEN jsonb_typeof(data->'events') = 'array' THEN data->'events' ELSE '[]'::jsonb END) || $3::jsonb)
		WHERE org_id=$1 AND id = (SELECT id FROM agent_traces WHERE org_id=$1 AND task_id=$2 ORDER BY created_at DESC, id DESC LIMIT 1)
		AND jsonb_array_length(CASE WHEN jsonb_typeof(data->'events') = 'array' THEN data->'events' ELSE '[]'::jsonb END) < 40`, orgID, taskID, jb(e))
	return tag.RowsAffected() > 0, err
}

func scanTrace(r scanner) (application.Trace, error) {
	var t application.Trace
	var data []byte
	if err := r.Scan(&data); err != nil {
		return t, err
	}
	unmarshal(data, &t)
	if t.Events == nil {
		t.Events = []application.TraceEvent{}
	}
	if t.ToolRequests == nil {
		t.ToolRequests = []application.TraceTool{}
	}
	return t, nil
}

func (s *Store) ListTraces(ctx context.Context, orgID, agentID string, limit int, before time.Time) ([]application.Trace, error) {
	if before.IsZero() {
		before = time.Now().UTC().Add(time.Hour)
	}
	return many(ctx, s, orgID, scanTrace, `SELECT data FROM agent_traces WHERE org_id=$1 AND agent_id=$2 AND created_at < $3
		ORDER BY created_at DESC, id DESC LIMIT $4`, orgID, agentID, before, limit)
}

func (s *Store) GetTrace(ctx context.Context, orgID, agentID, id string) (application.Trace, error) {
	t, err := one(ctx, s, orgID, scanTrace, `SELECT data FROM agent_traces WHERE org_id=$1 AND agent_id=$2 AND id=$3`, orgID, agentID, id)
	return t, mapErr(err)
}

func (s *Store) PurgeTraces(ctx context.Context, orgID string, before time.Time) (int, error) {
	tag, err := s.execTag(ctx, orgID, `DELETE FROM agent_traces WHERE org_id=$1 AND created_at < $2`, orgID, before)
	return int(tag.RowsAffected()), err
}

func (s *Store) GetTraceStorePrompts(ctx context.Context, orgID string) (bool, error) {
	on, err := one(ctx, s, orgID, func(r scanner) (bool, error) {
		var b bool
		err := r.Scan(&b)
		return b, err
	}, `SELECT store_prompts FROM trace_settings WHERE org_id=$1`, orgID)
	if err != nil {
		if mapErr(err) == domain.ErrNotFound {
			return true, nil
		}
		return true, err
	}
	return on, nil
}

func (s *Store) SetTraceStorePrompts(ctx context.Context, orgID string, on bool) error {
	return s.exec(ctx, orgID, `INSERT INTO trace_settings (org_id, store_prompts) VALUES ($1,$2)
		ON CONFLICT (org_id) DO UPDATE SET store_prompts=EXCLUDED.store_prompts, updated_at=now()`, orgID, on)
}

func (s *Store) AddSteerNote(ctx context.Context, orgID string, n application.SteerNote) error {
	return s.exec(ctx, orgID, `INSERT INTO task_steer_notes (id, org_id, task_id, agent_id, note, author, created_at) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		n.ID, orgID, n.TaskID, n.AgentID, n.Note, n.Author, n.CreatedAt)
}

func (s *Store) ListSteerNotes(ctx context.Context, orgID, taskID string) ([]application.SteerNote, error) {
	return many(ctx, s, orgID, func(r scanner) (application.SteerNote, error) {
		var n application.SteerNote
		err := r.Scan(&n.ID, &n.TaskID, &n.AgentID, &n.Note, &n.Author, &n.CreatedAt, &n.DeliveredAt)
		return n, err
	}, `SELECT id, task_id, agent_id, note, author, created_at, delivered_at FROM task_steer_notes WHERE org_id=$1 AND task_id=$2 ORDER BY created_at, id`, orgID, taskID)
}

func (s *Store) MarkSteerDelivered(ctx context.Context, orgID string, ids []string, at time.Time) error {
	return s.exec(ctx, orgID, `UPDATE task_steer_notes SET delivered_at=$3 WHERE org_id=$1 AND id = ANY($2) AND delivered_at IS NULL`, orgID, ids, at)
}

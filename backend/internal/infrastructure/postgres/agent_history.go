package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"aiworkforce/backend/internal/agenthistory"
	"aiworkforce/backend/internal/domain"
)

// AgentHistoryStore implements agenthistory.Store on Postgres (migration 827). Every call runs through WithOrgTx,
// so Row-Level Security isolates the organizations; the org_id filters are a second barrier. The table is
// append-only: there is no UPDATE here (a trigger refuses one), only INSERT and the retention DELETE.
type AgentHistoryStore struct{ S *Store }

var _ agenthistory.Store = (*AgentHistoryStore)(nil)

const agentVersionCols = `agent_id, version, created_at, actor, actor_kind, actor_name, source, fields, restored_from, config`

func scanAgentVersion(r scanner) (agenthistory.Version, error) {
	var v agenthistory.Version
	var fields, cfg []byte
	if err := r.Scan(&v.AgentID, &v.Version, &v.CreatedAt, &v.Actor, &v.ActorKind, &v.ActorName, &v.Source, &fields, &v.RestoredFrom, &cfg); err != nil {
		return v, err
	}
	v.CreatedAt = v.CreatedAt.UTC()
	if err := json.Unmarshal(fields, &v.Fields); err != nil {
		return v, err
	}
	if v.Fields == nil {
		v.Fields = []string{}
	}
	return v, json.Unmarshal(cfg, &v.Config)
}

func (s *AgentHistoryStore) Last(ctx context.Context, org, agentID string) (agenthistory.Version, bool, error) {
	v, err := one(ctx, s.S, org, scanAgentVersion, `SELECT `+agentVersionCols+` FROM agent_config_versions
		WHERE org_id=$1 AND agent_id=$2 ORDER BY version DESC LIMIT 1`, org, agentID)
	if err = mapErr(err); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return agenthistory.Version{}, false, nil
		}
		return v, false, err
	}
	return v, true, nil
}

func (s *AgentHistoryStore) Append(ctx context.Context, org string, v agenthistory.Version, keep int) error {
	return s.S.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		var last int
		if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(version), 0) FROM agent_config_versions WHERE org_id=$1 AND agent_id=$2`,
			org, v.AgentID).Scan(&last); err != nil {
			return err
		}
		if v.Version != last+1 {
			return fmt.Errorf("%w: version %d of agent %s already exists", domain.ErrConflict, v.Version, v.AgentID)
		}
		fields := v.Fields
		if fields == nil {
			fields = []string{}
		}
		_, err := tx.Exec(ctx, `INSERT INTO agent_config_versions (org_id, agent_id, version, created_at, actor, actor_kind, actor_name, source, fields, restored_from, config)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
			org, v.AgentID, v.Version, v.CreatedAt, v.Actor, v.ActorKind, v.ActorName, v.Source, jb(fields), v.RestoredFrom, jb(v.Config))
		if isUnique(err) {
			return fmt.Errorf("%w: version %d of agent %s already exists", domain.ErrConflict, v.Version, v.AgentID)
		}
		if err != nil || keep <= 0 {
			return err
		}
		_, err = tx.Exec(ctx, `DELETE FROM agent_config_versions WHERE org_id=$1 AND agent_id=$2 AND version <= $3`,
			org, v.AgentID, v.Version-keep)
		return err
	})
}

func (s *AgentHistoryStore) List(ctx context.Context, org, agentID string, limit int) ([]agenthistory.Version, error) {
	if limit <= 0 {
		limit = agenthistory.DefaultKeep
	}
	return many(ctx, s.S, org, scanAgentVersion, `SELECT `+agentVersionCols+` FROM agent_config_versions
		WHERE org_id=$1 AND agent_id=$2 ORDER BY version DESC LIMIT $3`, org, agentID, limit)
}

func (s *AgentHistoryStore) Get(ctx context.Context, org, agentID string, n int) (agenthistory.Version, error) {
	v, err := one(ctx, s.S, org, scanAgentVersion, `SELECT `+agentVersionCols+` FROM agent_config_versions
		WHERE org_id=$1 AND agent_id=$2 AND version=$3`, org, agentID, n)
	if err = mapErr(err); err != nil {
		return v, fmt.Errorf("%w: version %d of agent %s", err, n, agentID)
	}
	return v, nil
}

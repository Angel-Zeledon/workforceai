package postgres

import (
	"context"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

var _ application.DebateStore = (*Store)(nil)

const debateCols = `id, topic, trigger_kind, request_id, task_id, options, participants, turns, synthesis, status, fail_code, max_turns, locale, model, provider, cost_usd, created_by, created_at`

func scanDebate(r scanner) (domain.Debate, error) {
	var d domain.Debate
	var options, participants, turns, synthesis []byte
	err := r.Scan(&d.ID, &d.Topic, &d.Trigger, &d.RequestID, &d.TaskID, &options, &participants, &turns, &synthesis, &d.Status, &d.FailCode,
		&d.MaxTurns, &d.Locale, &d.Model, &d.Provider, &d.CostUSD, &d.CreatedBy, &d.CreatedAt)
	unmarshal(options, &d.Options)
	unmarshal(participants, &d.Participants)
	unmarshal(turns, &d.Turns)
	unmarshal(synthesis, &d.Synthesis)
	application.NormalizeDebate(&d)
	return d, err
}

// CreateDebate stores one debate (written once, never edited).
func (s *Store) CreateDebate(ctx context.Context, orgID string, d domain.Debate) error {
	application.NormalizeDebate(&d)
	return s.exec(ctx, orgID, `INSERT INTO team_debates (org_id, `+debateCols+`)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)`,
		orgID, d.ID, d.Topic, d.Trigger, d.RequestID, d.TaskID, jb(d.Options), jb(d.Participants), jb(d.Turns), jb(d.Synthesis),
		d.Status, d.FailCode, d.MaxTurns, d.Locale, d.Model, d.Provider, d.CostUSD, d.CreatedBy, d.CreatedAt)
}

func (s *Store) GetDebate(ctx context.Context, orgID, id string) (domain.Debate, error) {
	d, err := one(ctx, s, orgID, scanDebate, `SELECT `+debateCols+` FROM team_debates WHERE org_id=$1 AND id=$2`, orgID, id)
	return d, mapErr(err)
}

// ListDebates returns the newest debates first; requestID filters to one request when not empty.
func (s *Store) ListDebates(ctx context.Context, orgID, requestID string, limit int) ([]domain.Debate, error) {
	return many(ctx, s, orgID, scanDebate, `SELECT `+debateCols+` FROM team_debates WHERE org_id=$1 AND ($2='' OR request_id=$2)
		ORDER BY created_at DESC, id LIMIT $3`, orgID, requestID, limit)
}

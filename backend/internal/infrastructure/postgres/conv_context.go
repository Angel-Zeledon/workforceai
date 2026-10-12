package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"aiworkforce/backend/internal/application"
)

// Conversation context of /clear and /compact (application.ConvContextStore;
// migration 600_conversation_context.sql). NULL timestamps mean "never".

var _ application.ConvContextStore = (*Store)(nil)

func (s *Store) GetConvContext(ctx context.Context, org, conv string) (application.ConvContext, bool, error) {
	c, err := one(ctx, s, org, func(r scanner) (application.ConvContext, error) {
		var c application.ConvContext
		var cleared, upto *time.Time
		err := r.Scan(&c.ConversationID, &c.Epoch, &cleared, &c.Summary, &upto, &c.SummaryTokens, &c.UpdatedAt)
		if cleared != nil {
			c.ClearedAt = *cleared
		}
		if upto != nil {
			c.SummaryUpto = *upto
		}
		return c, err
	}, `SELECT conversation_id, epoch, cleared_at, summary, summary_upto, summary_tokens, updated_at
		FROM conversation_contexts WHERE org_id=$1 AND conversation_id=$2`, org, conv)
	if errors.Is(err, pgx.ErrNoRows) {
		return application.ConvContext{}, false, nil
	}
	if err != nil {
		return application.ConvContext{}, false, err
	}
	return c, true, nil
}

func (s *Store) PutConvContext(ctx context.Context, org string, c application.ConvContext) error {
	return s.exec(ctx, org, `INSERT INTO conversation_contexts (org_id, conversation_id, epoch, cleared_at, summary, summary_upto, summary_tokens, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,now())
		ON CONFLICT (org_id, conversation_id) DO UPDATE SET epoch=EXCLUDED.epoch, cleared_at=EXCLUDED.cleared_at,
			summary=EXCLUDED.summary, summary_upto=EXCLUDED.summary_upto, summary_tokens=EXCLUDED.summary_tokens, updated_at=now()`,
		org, c.ConversationID, c.Epoch, nullTime(c.ClearedAt), c.Summary, nullTime(c.SummaryUpto), c.SummaryTokens)
}

func nullTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

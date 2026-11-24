package postgres

import (
	"context"
	"fmt"

	"aiworkforce/backend/internal/domain"
)

// DeleteAgent removes an agent that was just created and never finished being set up (a hire that failed half way).
func (s *Store) DeleteAgent(ctx context.Context, orgID, id string) error {
	tag, err := s.execTag(ctx, orgID, `DELETE FROM agents WHERE org_id=$1 AND id=$2`, orgID, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: agent %s", domain.ErrNotFound, id)
	}
	return nil
}

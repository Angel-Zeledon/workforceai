package postgres

import (
	"context"

	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/filecomments"
)

// FileCommentStore implements filecomments.Store on Postgres (migration 828). Every call runs through WithOrgTx, so
// Row-Level Security isolates the organizations; the org_id filters are a second barrier.
type FileCommentStore struct{ S *Store }

var _ filecomments.Store = (*FileCommentStore)(nil)

const fileCommentCols = `id, file_id, parent_id, anchor, author, body, resolved, resolved_by, resolved_version, resolved_at, change, created_at`

func scanFileComment(r scanner) (filecomments.Comment, error) {
	var c filecomments.Comment
	var anchor, author, change []byte
	err := r.Scan(&c.ID, &c.FileID, &c.ParentID, &anchor, &author, &c.Body, &c.Resolved, &c.ResolvedBy, &c.ResolvedVersion, &c.ResolvedAt, &change, &c.CreatedAt)
	if len(anchor) > 0 && string(anchor) != "null" {
		c.Anchor = &filecomments.Anchor{}
		unmarshal(anchor, c.Anchor)
	}
	if len(change) > 0 && string(change) != "null" {
		c.Change = &filecomments.Change{}
		unmarshal(change, c.Change)
	}
	unmarshal(author, &c.Author)
	return c, err
}

// optJSON is the JSONB of an optional value (SQL NULL when absent).
func optJSON[T any](v *T) []byte {
	if v == nil {
		return nil
	}
	return jb(v)
}

func (s *FileCommentStore) Add(ctx context.Context, org string, c filecomments.Comment) error {
	return s.S.exec(ctx, org, `INSERT INTO file_comments (org_id, id, file_id, parent_id, anchor, author, body, resolved, resolved_by, resolved_version, resolved_at, change, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
		org, c.ID, c.FileID, c.ParentID, optJSON(c.Anchor), jb(c.Author), c.Body, c.Resolved, c.ResolvedBy, c.ResolvedVersion, c.ResolvedAt, optJSON(c.Change), c.CreatedAt)
}

func (s *FileCommentStore) List(ctx context.Context, org, fileID string) ([]filecomments.Comment, error) {
	return many(ctx, s.S, org, scanFileComment, `SELECT `+fileCommentCols+` FROM file_comments WHERE org_id=$1 AND file_id=$2 ORDER BY created_at, id`, org, fileID)
}

func (s *FileCommentStore) Get(ctx context.Context, org, id string) (filecomments.Comment, error) {
	c, err := one(ctx, s.S, org, scanFileComment, `SELECT `+fileCommentCols+` FROM file_comments WHERE org_id=$1 AND id=$2`, org, id)
	return c, mapErr(err)
}

func (s *FileCommentStore) Update(ctx context.Context, org string, c filecomments.Comment) error {
	tag, err := s.S.execTag(ctx, org, `UPDATE file_comments SET resolved=$3, resolved_by=$4, resolved_version=$5, resolved_at=$6, change=$7
		WHERE org_id=$1 AND id=$2`, org, c.ID, c.Resolved, c.ResolvedBy, c.ResolvedVersion, c.ResolvedAt, optJSON(c.Change))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

package postgres

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"

	"aiworkforce/backend/internal/artifacts"
	"aiworkforce/backend/internal/domain"
)

// ArtifactStore implements artifacts.Store on Postgres (migration 280). Every
// call runs through WithOrgTx, so Row-Level Security applies. A version save is
// one transaction: the head is replaced only if its version is the expected
// one (compare-and-swap) and the immutable version row is appended.
type ArtifactStore struct{ S *Store }

var _ artifacts.Store = (*ArtifactStore)(nil)

const artMetaCols = `id, kind, title, status, head_version, created_by, last_author, task_id, request_id, customer_id, project_id, deliverable_id,
	progress, build_state, locale, pending_proposals, tainted, locked, size_bytes, attachments, created_at, updated_at`

func scanArtMeta(r scanner) (artifacts.Meta, error) {
	var m artifacts.Meta
	var kind string
	var by, last, att []byte
	err := r.Scan(&m.ID, &kind, &m.Title, &m.Status, &m.HeadVersion, &by, &last, &m.TaskID, &m.RequestID, &m.CustomerID, &m.ProjectID, &m.DeliverableID,
		&m.Progress, &m.BuildState, &m.Locale, &m.PendingProposals, &m.Tainted, &m.Locked, &m.SizeBytes, &att, &m.CreatedAt, &m.UpdatedAt)
	m.Kind = artifacts.Kind(kind)
	m.SchemaVersion = m.Kind.Schema()
	unmarshal(by, &m.CreatedBy)
	unmarshal(last, &m.LastAuthor)
	unmarshal(att, &m.Attachments)
	if m.Attachments == nil {
		m.Attachments = []artifacts.Attachment{}
	}
	m.DependsOnArtifact = []string{} // derived by the service from the links
	return m, err
}

func (a *ArtifactStore) Create(ctx context.Context, org string, st artifacts.Stored, v artifacts.Version) error {
	m := st.Meta
	return a.S.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO artifacts (id, org_id, kind, title, status, head_version, created_by, last_author, task_id, request_id, customer_id,
			project_id, deliverable_id, progress, build_state, locale, pending_proposals, tainted, locked, size_bytes, attachments, content, created_at, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24)`,
			m.ID, org, string(m.Kind), m.Title, m.Status, m.HeadVersion, jb(m.CreatedBy), jb(m.LastAuthor), m.TaskID, m.RequestID, m.CustomerID,
			m.ProjectID, m.DeliverableID, m.Progress, m.BuildState, m.Locale, m.PendingProposals, m.Tainted, m.Locked, m.SizeBytes, jb(m.Attachments),
			[]byte(st.Content), m.CreatedAt, m.UpdatedAt); err != nil {
			return err
		}
		return insertArtVersion(ctx, tx, org, v)
	})
}

func insertArtVersion(ctx context.Context, tx pgx.Tx, org string, v artifacts.Version) error {
	_, err := tx.Exec(ctx, `INSERT INTO artifact_versions (org_id, artifact_id, version, base_version, author, source, summary, task_id, content_hash, content, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		org, v.ArtifactID, v.Version, v.BaseVersion, jb(v.Author), v.Source, v.Summary, v.TaskID, v.ContentHash, []byte(v.Content), v.CreatedAt)
	return err
}

func (a *ArtifactStore) Get(ctx context.Context, org, id string) (artifacts.Stored, error) {
	st, err := one(ctx, a.S, org, func(r scanner) (artifacts.Stored, error) {
		var s artifacts.Stored
		var kind string
		var by, last, att, content []byte
		m := &s.Meta
		err := r.Scan(&m.ID, &kind, &m.Title, &m.Status, &m.HeadVersion, &by, &last, &m.TaskID, &m.RequestID, &m.CustomerID, &m.ProjectID, &m.DeliverableID,
			&m.Progress, &m.BuildState, &m.Locale, &m.PendingProposals, &m.Tainted, &m.Locked, &m.SizeBytes, &att, &m.CreatedAt, &m.UpdatedAt, &content)
		m.Kind = artifacts.Kind(kind)
		m.SchemaVersion = m.Kind.Schema()
		unmarshal(by, &m.CreatedBy)
		unmarshal(last, &m.LastAuthor)
		unmarshal(att, &m.Attachments)
		if m.Attachments == nil {
			m.Attachments = []artifacts.Attachment{}
		}
		m.DependsOnArtifact = []string{}
		s.Content = json.RawMessage(content)
		return s, err
	}, `SELECT `+artMetaCols+`, content FROM artifacts WHERE org_id=$1 AND id=$2`, org, id)
	return st, mapErr(err)
}

func (a *ArtifactStore) List(ctx context.Context, org string) ([]artifacts.Meta, error) {
	return many(ctx, a.S, org, scanArtMeta, `SELECT `+artMetaCols+` FROM artifacts WHERE org_id=$1 ORDER BY updated_at DESC, id`, org)
}

func (a *ArtifactStore) Commit(ctx context.Context, org string, st artifacts.Stored, v artifacts.Version, expectedHead int) error {
	m := st.Meta
	return a.S.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE artifacts SET head_version=$3, last_author=$4, size_bytes=$5, content=$6, updated_at=$7, title=$8, status=$9, locked=$10
			WHERE org_id=$1 AND id=$2 AND head_version=$11`,
			org, m.ID, m.HeadVersion, jb(m.LastAuthor), m.SizeBytes, []byte(st.Content), m.UpdatedAt, m.Title, m.Status, m.Locked, expectedHead)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			var one int
			if err := tx.QueryRow(ctx, `SELECT 1 FROM artifacts WHERE org_id=$1 AND id=$2`, org, m.ID).Scan(&one); err != nil {
				return mapErr(err)
			}
			return domain.ErrConflict
		}
		return insertArtVersion(ctx, tx, org, v)
	})
}

func (a *ArtifactStore) UpdateMeta(ctx context.Context, org string, m artifacts.Meta) error {
	tag, err := a.S.execTag(ctx, org, `UPDATE artifacts SET title=$3, status=$4, task_id=$5, request_id=$6, customer_id=$7, project_id=$8, deliverable_id=$9,
		progress=$10, build_state=$11, locale=$12, pending_proposals=$13, tainted=$14, locked=$15, attachments=$16, updated_at=$17
		WHERE org_id=$1 AND id=$2`,
		org, m.ID, m.Title, m.Status, m.TaskID, m.RequestID, m.CustomerID, m.ProjectID, m.DeliverableID,
		m.Progress, m.BuildState, m.Locale, m.PendingProposals, m.Tainted, m.Locked, jb(m.Attachments), m.UpdatedAt)
	if err == nil && tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return err
}

func scanArtVersion(r scanner) (artifacts.Version, error) {
	var v artifacts.Version
	var author, content []byte
	err := r.Scan(&v.ArtifactID, &v.Version, &v.BaseVersion, &author, &v.Source, &v.Summary, &v.TaskID, &v.ContentHash, &v.CreatedAt, &content)
	unmarshal(author, &v.Author)
	v.Content = json.RawMessage(content)
	return v, err
}

const artVersionCols = `artifact_id, version, base_version, author, source, summary, task_id, content_hash, created_at`

func (a *ArtifactStore) GetVersion(ctx context.Context, org, id string, n int) (artifacts.Version, error) {
	v, err := one(ctx, a.S, org, scanArtVersion, `SELECT `+artVersionCols+`, content FROM artifact_versions WHERE org_id=$1 AND artifact_id=$2 AND version=$3`, org, id, n)
	return v, mapErr(err)
}

func (a *ArtifactStore) ListVersions(ctx context.Context, org, id string) ([]artifacts.VersionInfo, error) {
	return many(ctx, a.S, org, func(r scanner) (artifacts.VersionInfo, error) {
		var v artifacts.VersionInfo
		var author []byte
		err := r.Scan(&v.ArtifactID, &v.Version, &v.BaseVersion, &author, &v.Source, &v.Summary, &v.TaskID, &v.ContentHash, &v.CreatedAt)
		unmarshal(author, &v.Author)
		return v, err
	}, `SELECT `+artVersionCols+` FROM artifact_versions WHERE org_id=$1 AND artifact_id=$2 ORDER BY version DESC`, org, id)
}

const artLinkCols = `id, from_id, to_id, relation, anchor, alias, auto, synced_version`

func scanArtLink(r scanner) (artifacts.Link, error) {
	var l artifacts.Link
	var anchor []byte
	err := r.Scan(&l.ID, &l.From, &l.To, &l.Relation, &anchor, &l.Alias, &l.Auto, &l.SyncedVersion)
	if len(anchor) > 0 && string(anchor) != "null" {
		var an artifacts.Anchor
		unmarshal(anchor, &an)
		l.Anchor = &an
	}
	return l, err
}

func anchorJSON(a *artifacts.Anchor) []byte {
	if a == nil {
		return nil
	}
	return jb(a)
}

func (a *ArtifactStore) ReplaceAutoLinks(ctx context.Context, org, from string, links []artifacts.Link) error {
	return a.S.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		synced := map[string]int{}
		rows, err := tx.Query(ctx, `SELECT to_id, relation, synced_version FROM artifact_links WHERE org_id=$1 AND from_id=$2 AND auto`, org, from)
		if err != nil {
			return err
		}
		for rows.Next() {
			var to, rel string
			var v int
			if err := rows.Scan(&to, &rel, &v); err != nil {
				rows.Close()
				return err
			}
			synced[to+"|"+rel] = v
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM artifact_links WHERE org_id=$1 AND from_id=$2 AND auto`, org, from); err != nil {
			return err
		}
		for _, l := range links {
			if v, ok := synced[l.To+"|"+l.Relation]; ok {
				l.SyncedVersion = v
			}
			if _, err := tx.Exec(ctx, `INSERT INTO artifact_links (`+artLinkCols+`, org_id) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
				l.ID, l.From, l.To, l.Relation, anchorJSON(l.Anchor), l.Alias, true, l.SyncedVersion, org); err != nil {
				return err
			}
		}
		return nil
	})
}

func (a *ArtifactStore) AddLink(ctx context.Context, org string, l artifacts.Link) error {
	return a.S.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		var one int
		err := tx.QueryRow(ctx, `SELECT 1 FROM artifact_links WHERE org_id=$1 AND from_id=$2 AND to_id=$3 AND relation=$4 AND NOT auto`, org, l.From, l.To, l.Relation).Scan(&one)
		if err == nil {
			return domain.ErrConflict
		}
		if err != pgx.ErrNoRows {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO artifact_links (`+artLinkCols+`, org_id) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
			l.ID, l.From, l.To, l.Relation, anchorJSON(l.Anchor), l.Alias, l.Auto, l.SyncedVersion, org)
		return err
	})
}

func (a *ArtifactStore) ListLinks(ctx context.Context, org, id string) ([]artifacts.Link, error) {
	return many(ctx, a.S, org, scanArtLink, `SELECT `+artLinkCols+` FROM artifact_links WHERE org_id=$1 AND (from_id=$2 OR to_id=$2) ORDER BY created_at, id`, org, id)
}

func (a *ArtifactStore) ListAllLinks(ctx context.Context, org string) ([]artifacts.Link, error) {
	return many(ctx, a.S, org, scanArtLink, `SELECT `+artLinkCols+` FROM artifact_links WHERE org_id=$1 ORDER BY created_at, id`, org)
}

func (a *ArtifactStore) SetSyncedVersion(ctx context.Context, org, linkID string, version int) error {
	return a.S.exec(ctx, org, `UPDATE artifact_links SET synced_version=$3 WHERE org_id=$1 AND id=$2`, org, linkID, version)
}

// ---- comments and proposals (migration 340) ----

var _ artifacts.CollabStore = (*ArtifactStore)(nil)

const artCommentCols = `id, artifact_id, parent_id, anchor, author, body, mentions, resolved, resolved_by, created_at`

func scanArtComment(r scanner) (artifacts.Comment, error) {
	var c artifacts.Comment
	var anchor, author, mentions []byte
	err := r.Scan(&c.ID, &c.ArtifactID, &c.ParentID, &anchor, &author, &c.Body, &mentions, &c.Resolved, &c.ResolvedBy, &c.CreatedAt)
	if len(anchor) > 0 && string(anchor) != "null" {
		c.Anchor = &artifacts.Anchor{}
		unmarshal(anchor, c.Anchor)
	}
	unmarshal(author, &c.Author)
	unmarshal(mentions, &c.Mentions)
	if c.Mentions == nil {
		c.Mentions = []string{}
	}
	return c, err
}

func (a *ArtifactStore) AddComment(ctx context.Context, org string, c artifacts.Comment) error {
	_, err := a.S.execTag(ctx, org, `INSERT INTO artifact_comments (id, org_id, artifact_id, parent_id, anchor, author, body, mentions, resolved, resolved_by, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		c.ID, org, c.ArtifactID, c.ParentID, anchorJSON(c.Anchor), jb(c.Author), c.Body, jb(c.Mentions), c.Resolved, c.ResolvedBy, c.CreatedAt)
	return err
}

func (a *ArtifactStore) ListComments(ctx context.Context, org, artifactID string) ([]artifacts.Comment, error) {
	return many(ctx, a.S, org, scanArtComment, `SELECT `+artCommentCols+` FROM artifact_comments WHERE org_id=$1 AND artifact_id=$2 ORDER BY created_at, id`, org, artifactID)
}

func (a *ArtifactStore) GetComment(ctx context.Context, org, id string) (artifacts.Comment, error) {
	c, err := one(ctx, a.S, org, scanArtComment, `SELECT `+artCommentCols+` FROM artifact_comments WHERE org_id=$1 AND id=$2`, org, id)
	return c, mapErr(err)
}

func (a *ArtifactStore) UpdateComment(ctx context.Context, org string, c artifacts.Comment) error {
	tag, err := a.S.execTag(ctx, org, `UPDATE artifact_comments SET resolved=$3, resolved_by=$4 WHERE org_id=$1 AND id=$2`, org, c.ID, c.Resolved, c.ResolvedBy)
	if err == nil && tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return err
}

const artProposalCols = `id, artifact_id, base_version, author, summary, content, status, resolved_by, resolved_note, resolved_version, created_at, resolved_at`

func scanArtProposal(r scanner) (artifacts.Proposal, error) {
	var p artifacts.Proposal
	var author, content []byte
	err := r.Scan(&p.ID, &p.ArtifactID, &p.BaseVersion, &author, &p.Summary, &content, &p.Status, &p.ResolvedBy, &p.ResolvedNote, &p.ResolvedVersion, &p.CreatedAt, &p.ResolvedAt)
	unmarshal(author, &p.Author)
	p.Content = json.RawMessage(content)
	return p, err
}

func (a *ArtifactStore) AddProposal(ctx context.Context, org string, p artifacts.Proposal) error {
	_, err := a.S.execTag(ctx, org, `INSERT INTO artifact_proposals (id, org_id, artifact_id, base_version, author, summary, content, status, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		p.ID, org, p.ArtifactID, p.BaseVersion, jb(p.Author), p.Summary, []byte(p.Content), p.Status, p.CreatedAt)
	return err
}

func (a *ArtifactStore) ListProposals(ctx context.Context, org, artifactID string) ([]artifacts.Proposal, error) {
	return many(ctx, a.S, org, scanArtProposal, `SELECT `+artProposalCols+` FROM artifact_proposals WHERE org_id=$1 AND artifact_id=$2 ORDER BY created_at, id`, org, artifactID)
}

func (a *ArtifactStore) GetProposal(ctx context.Context, org, id string) (artifacts.Proposal, error) {
	p, err := one(ctx, a.S, org, scanArtProposal, `SELECT `+artProposalCols+` FROM artifact_proposals WHERE org_id=$1 AND id=$2`, org, id)
	return p, mapErr(err)
}

func (a *ArtifactStore) UpdateProposal(ctx context.Context, org string, p artifacts.Proposal) error {
	tag, err := a.S.execTag(ctx, org, `UPDATE artifact_proposals SET status=$3, resolved_by=$4, resolved_note=$5, resolved_version=$6, resolved_at=$7 WHERE org_id=$1 AND id=$2`,
		org, p.ID, p.Status, p.ResolvedBy, p.ResolvedNote, p.ResolvedVersion, p.ResolvedAt)
	if err == nil && tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return err
}

package peoplechat

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"aiworkforce/backend/internal/auth"
)

// PgStore implements Store on Postgres (migration 730). Every statement runs in auth.WithOrgTx, so the RLS policies
// scope it to the organization even if a WHERE clause were forgotten.
type PgStore struct{ Pool *pgxpool.Pool }

var _ Store = (*PgStore)(nil)

func (p *PgStore) tx(ctx context.Context, org string, fn func(tx pgx.Tx) error) error {
	return auth.WithOrgTx(ctx, p.Pool, org, fn)
}

const chanCols = `id, kind, name, scope_key, created_by, last_seq, last_message_at, archived, created_at`

func scanChannel(r pgx.Row) (Channel, error) {
	var c Channel
	err := r.Scan(&c.ID, &c.Kind, &c.Name, &c.ScopeKey, &c.CreatedBy, &c.LastSeq, &c.LastMessageAt, &c.Archived, &c.CreatedAt)
	return c, err
}

func (p *PgStore) EnsureOrgChannel(ctx context.Context, org, createdBy string) (Channel, error) {
	var out Channel
	err := p.tx(ctx, org, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO pc_channels (org_id, id, kind, name, scope_key, created_by)
			VALUES ($1, 'org:'||$1, 'org', 'general', 'general', $2) ON CONFLICT DO NOTHING`, org, createdBy)
		if err != nil {
			return err
		}
		out, err = scanChannel(tx.QueryRow(ctx, `SELECT `+chanCols+` FROM pc_channels WHERE org_id=$1 AND kind='org' AND scope_key='general'`, org))
		return err
	})
	return out, err
}

func (p *PgStore) CreateChannel(ctx context.Context, org string, ch Channel, members []Member) (Channel, bool, error) {
	var out Channel
	created := false
	err := p.tx(ctx, org, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `INSERT INTO pc_channels (org_id, id, kind, name, scope_key, created_by)
			VALUES ($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING`, org, ch.ID, ch.Kind, ch.Name, ch.ScopeKey, ch.CreatedBy)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 { // the (kind, scope_key) channel already exists: a DM opened twice
			out, err = scanChannel(tx.QueryRow(ctx, `SELECT `+chanCols+` FROM pc_channels WHERE org_id=$1 AND kind=$2 AND scope_key=$3`, org, ch.Kind, ch.ScopeKey))
			return err
		}
		created = true
		for _, m := range members {
			if _, err := tx.Exec(ctx, `INSERT INTO pc_members (org_id, channel_id, member_kind, member_id, role) VALUES ($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`,
				org, ch.ID, m.Kind, m.ID, roleOr(m.Role)); err != nil {
				return err
			}
		}
		out, err = scanChannel(tx.QueryRow(ctx, `SELECT `+chanCols+` FROM pc_channels WHERE org_id=$1 AND id=$2`, org, ch.ID))
		return err
	})
	return out, created, err
}

func roleOr(r string) string {
	if r == "owner" {
		return r
	}
	return "member"
}

func (p *PgStore) Channel(ctx context.Context, org, id string) (Channel, []Member, error) {
	var c Channel
	var ms []Member
	err := p.tx(ctx, org, func(tx pgx.Tx) error {
		var err error
		c, err = scanChannel(tx.QueryRow(ctx, `SELECT `+chanCols+` FROM pc_channels WHERE org_id=$1 AND id=$2`, org, id))
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		m, err := loadMembers(ctx, tx, org, []string{id})
		ms = m[id]
		return err
	})
	return c, ms, err
}

func loadMembers(ctx context.Context, tx pgx.Tx, org string, ids []string) (map[string][]Member, error) {
	out := map[string][]Member{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := tx.Query(ctx, `SELECT channel_id, member_kind, member_id, role, last_read_seq FROM pc_members
		WHERE org_id=$1 AND channel_id = ANY($2) ORDER BY joined_at, member_id`, org, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var ch string
		var m Member
		if err := rows.Scan(&ch, &m.Kind, &m.ID, &m.Role, &m.LastReadSeq); err != nil {
			return nil, err
		}
		out[ch] = append(out[ch], m)
	}
	return out, rows.Err()
}

func (p *PgStore) ChannelsOf(ctx context.Context, org, userID string, shared bool) ([]Channel, error) {
	var out []Channel
	err := p.tx(ctx, org, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+chanCols+` FROM pc_channels c
			WHERE c.org_id=$1 AND (c.kind='org'
			   OR ($3 AND c.kind IN ('department','project'))
			   OR EXISTS (SELECT 1 FROM pc_members m WHERE m.org_id=c.org_id AND m.channel_id=c.id AND m.member_kind='user' AND m.member_id=$2))
			ORDER BY c.last_message_at DESC NULLS LAST, c.name`, org, userID, shared)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			c, err := scanChannel(rows)
			if err != nil {
				return err
			}
			out = append(out, c)
		}
		return rows.Err()
	})
	return out, err
}

func (p *PgStore) Members(ctx context.Context, org string, ids []string) (map[string][]Member, error) {
	var out map[string][]Member
	err := p.tx(ctx, org, func(tx pgx.Tx) error {
		var err error
		out, err = loadMembers(ctx, tx, org, ids)
		return err
	})
	return out, err
}

func (p *PgStore) AddMember(ctx context.Context, org, channelID string, m Member) error {
	return p.tx(ctx, org, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO pc_members (org_id, channel_id, member_kind, member_id, role) VALUES ($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`,
			org, channelID, m.Kind, m.ID, roleOr(m.Role))
		return err
	})
}

func (p *PgStore) RemoveMember(ctx context.Context, org, channelID, kind, id string) error {
	return p.tx(ctx, org, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM pc_members WHERE org_id=$1 AND channel_id=$2 AND member_kind=$3 AND member_id=$4`, org, channelID, kind, id)
		return err
	})
}

func (p *PgStore) Append(ctx context.Context, org string, m Message) (Message, error) {
	err := p.tx(ctx, org, func(tx pgx.Tx) error {
		// the UPDATE serializes writers of one channel and hands out the next sequence
		err := tx.QueryRow(ctx, `UPDATE pc_channels SET last_seq = last_seq + 1, last_message_at = $3 WHERE org_id=$1 AND id=$2 RETURNING last_seq`,
			org, m.ChannelID, m.CreatedAt).Scan(&m.Seq)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		ment, _ := json.Marshal(m.Mentions)
		_, err = tx.Exec(ctx, `INSERT INTO pc_messages (org_id, id, channel_id, seq, author_kind, author_id, body, mentions, created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`, org, m.ID, m.ChannelID, m.Seq, m.AuthorKind, m.AuthorID, m.Body, ment, m.CreatedAt)
		return err
	})
	return m, err
}

func (p *PgStore) Messages(ctx context.Context, org, channelID string, before int64, limit int) ([]Message, error) {
	var out []Message
	err := p.tx(ctx, org, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id, channel_id, seq, author_kind, author_id, body, mentions, created_at FROM pc_messages
			WHERE org_id=$1 AND channel_id=$2 AND ($3 = 0 OR seq < $3) ORDER BY seq DESC LIMIT $4`, org, channelID, before, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var m Message
			var ment []byte
			if err := rows.Scan(&m.ID, &m.ChannelID, &m.Seq, &m.AuthorKind, &m.AuthorID, &m.Body, &ment, &m.CreatedAt); err != nil {
				return err
			}
			_ = json.Unmarshal(ment, &m.Mentions)
			if m.Mentions == nil {
				m.Mentions = []Mention{}
			}
			out = append(out, m)
		}
		return rows.Err()
	})
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 { // oldest first
		out[i], out[j] = out[j], out[i]
	}
	return out, err
}

func (p *PgStore) MarkRead(ctx context.Context, org, channelID, userID string, seq int64) error {
	return p.tx(ctx, org, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE pc_members SET last_read_seq = GREATEST(last_read_seq, $4)
			WHERE org_id=$1 AND channel_id=$2 AND member_kind='user' AND member_id=$3`, org, channelID, userID, seq)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 { // implicit membership of the all-hands channel: create the row holding the marker
			_, err = tx.Exec(ctx, `INSERT INTO pc_members (org_id, channel_id, member_kind, member_id, last_read_seq)
				SELECT $1, id, 'user', $3, $4 FROM pc_channels WHERE org_id=$1 AND id=$2 AND kind='org'
				ON CONFLICT (org_id, channel_id, member_kind, member_id) DO UPDATE SET last_read_seq = GREATEST(pc_members.last_read_seq, EXCLUDED.last_read_seq)`,
				org, channelID, userID, seq)
		}
		return err
	})
}

func (p *PgStore) MentionsUnread(ctx context.Context, org, channelID, userID string, afterSeq int64) (int64, error) {
	var n int64
	probe, _ := json.Marshal([]Mention{{Kind: MemberUser, ID: userID}})
	err := p.tx(ctx, org, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM pc_messages WHERE org_id=$1 AND channel_id=$2 AND seq > $3
			AND NOT (author_kind='user' AND author_id=$4) AND mentions @> $5::jsonb`, org, channelID, afterSeq, userID, string(probe)).Scan(&n)
	})
	return n, err
}

// Directory lists the people of the organization from the memberships (users and memberships are not under RLS:
// the join is restricted to org explicitly).
func (p *PgStore) Directory(ctx context.Context, org string) ([]Person, error) {
	var out []Person
	err := p.tx(ctx, org, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT u.id, u.name, m.role FROM memberships m JOIN users u ON u.id = m.user_id WHERE m.org_id=$1 ORDER BY lower(u.name), u.id`, org)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var x Person
			if err := rows.Scan(&x.ID, &x.Name, &x.Role); err != nil {
				return err
			}
			out = append(out, x)
		}
		return rows.Err()
	})
	return out, err
}

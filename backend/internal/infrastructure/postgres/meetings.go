package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"aiworkforce/backend/internal/meetings"
)

// MeetingsStore implements meetings.Store on Postgres (migrations 740 and 741). Every call runs in an
// organization-scoped transaction, so Row-Level Security applies.
type MeetingsStore struct{ S *Store }

var _ meetings.Store = (*MeetingsStore)(nil)

const meetingCols = `id, title, description, room, status, starts_at, ends_at, organizer, provider, join_url,
	attendees, agenda, external, started_at, ended_at, cancelled_at, created_at, updated_at`

func scanMeeting(org string, row pgx.Row) (meetings.Meeting, error) {
	var m meetings.Meeting
	var status string
	var att, ag, ext []byte
	err := row.Scan(&m.ID, &m.Title, &m.Description, &m.Room, &status, &m.Start, &m.End, &m.Organizer, &m.Provider, &m.JoinURL,
		&att, &ag, &ext, &m.StartedAt, &m.EndedAt, &m.CancelledAt, &m.CreatedAt, &m.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return m, meetings.ErrNotFound
	}
	if err != nil {
		return m, err
	}
	m.OrgID, m.Status = org, meetings.Status(status)
	m.Start, m.End = m.Start.UTC(), m.End.UTC()
	_ = json.Unmarshal(att, &m.Attendees)
	_ = json.Unmarshal(ag, &m.Agenda)
	_ = json.Unmarshal(ext, &m.External)
	return m, nil
}

func (p *MeetingsStore) CreateMeeting(ctx context.Context, m meetings.Meeting) error {
	return p.S.WithOrgTx(ctx, m.OrgID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO meetings (org_id, `+meetingCols+`) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)`,
			m.OrgID, m.ID, m.Title, m.Description, m.Room, string(m.Status), m.Start, m.End, m.Organizer, m.Provider, m.JoinURL,
			jb(nz(m.Attendees)), jb(nzA(m.Agenda)), jb(m.External), m.StartedAt, m.EndedAt, m.CancelledAt, m.CreatedAt, m.UpdatedAt)
		return err
	})
}

func nz(a []meetings.Attendee) []meetings.Attendee {
	if a == nil {
		return []meetings.Attendee{}
	}
	return a
}
func nzA(a []meetings.AgendaItem) []meetings.AgendaItem {
	if a == nil {
		return []meetings.AgendaItem{}
	}
	return a
}

func (p *MeetingsStore) GetMeeting(ctx context.Context, org, id string) (meetings.Meeting, error) {
	var m meetings.Meeting
	err := p.S.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		var e error
		m, e = scanMeeting(org, tx.QueryRow(ctx, `SELECT `+meetingCols+` FROM meetings WHERE org_id=$1 AND id=$2`, org, id))
		return e
	})
	return m, err
}

func (p *MeetingsStore) ListMeetings(ctx context.Context, org string, f meetings.Filter) ([]meetings.Meeting, error) {
	var out []meetings.Meeting
	err := p.S.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		q := `SELECT ` + meetingCols + ` FROM meetings WHERE org_id=$1`
		args := []any{org}
		add := func(cond string, v any) {
			args = append(args, v)
			q += " AND " + cond + "$" + itoa(len(args))
		}
		if !f.From.IsZero() {
			add("ends_at > ", f.From)
		}
		if !f.To.IsZero() {
			add("starts_at < ", f.To)
		}
		if f.Room != "" {
			add("room = ", f.Room)
		}
		if len(f.Status) > 0 {
			ss := make([]string, len(f.Status))
			for i, s := range f.Status {
				ss[i] = string(s)
			}
			add("status = ANY(", ss)
			q += ")"
		}
		if f.Person != "" {
			// organizer, or a person/guest attendee by id or e-mail (agents never match a person).
			args = append(args, f.Person)
			n := itoa(len(args))
			q += ` AND (organizer = $` + n + ` OR EXISTS (SELECT 1 FROM jsonb_array_elements(attendees) a
				WHERE a->>'kind' <> 'agent' AND (a->>'id' = $` + n + ` OR lower(a->>'email') = lower($` + n + `))))`
		}
		q += ` ORDER BY starts_at, id`
		if f.Limit > 0 {
			args = append(args, f.Limit)
			q += ` LIMIT $` + itoa(len(args))
		}
		rows, err := tx.Query(ctx, q, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			m, err := scanMeeting(org, rows)
			if err != nil {
				return err
			}
			out = append(out, m)
		}
		return rows.Err()
	})
	return out, err
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func (p *MeetingsStore) UpdateMeeting(ctx context.Context, m meetings.Meeting, want meetings.Status) error {
	return p.S.WithOrgTx(ctx, m.OrgID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE meetings SET title=$3, description=$4, room=$5, status=$6, starts_at=$7, ends_at=$8, organizer=$9, provider=$10,
			join_url=$11, attendees=$12, agenda=$13, external=$14, started_at=$15, ended_at=$16, cancelled_at=$17, updated_at=$18
			WHERE org_id=$1 AND id=$2 AND status=$19`,
			m.OrgID, m.ID, m.Title, m.Description, m.Room, string(m.Status), m.Start, m.End, m.Organizer, m.Provider, m.JoinURL,
			jb(nz(m.Attendees)), jb(nzA(m.Agenda)), jb(m.External), m.StartedAt, m.EndedAt, m.CancelledAt, m.UpdatedAt, string(want))
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			var exists bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM meetings WHERE org_id=$1 AND id=$2)`, m.OrgID, m.ID).Scan(&exists); err != nil {
				return err
			}
			if !exists {
				return meetings.ErrNotFound
			}
			return meetings.ErrState
		}
		return nil
	})
}

func (p *MeetingsStore) PutMinutes(ctx context.Context, m meetings.Minutes) error {
	return p.S.WithOrgTx(ctx, m.OrgID, func(tx pgx.Tx) error {
		doc := struct {
			Summary       string                `json:"summary"`
			Decisions     []string              `json:"decisions"`
			ActionItems   []meetings.ActionItem `json:"action_items"`
			Lang          string                `json:"lang"`
			SummarySource string                `json:"summary_source"`
			Truncated     bool                  `json:"truncated"`
		}{m.Summary, m.Decisions, m.ActionItems, m.Lang, m.SummarySource, m.Truncated}
		if doc.Decisions == nil {
			doc.Decisions = []string{}
		}
		if doc.ActionItems == nil {
			doc.ActionItems = []meetings.ActionItem{}
		}
		_, err := tx.Exec(ctx, `INSERT INTO meeting_minutes (org_id, meeting_id, doc, source, file_id, file_path, request_id, created_by, created_at, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
			ON CONFLICT (org_id, meeting_id) DO UPDATE SET doc=EXCLUDED.doc, source=EXCLUDED.source, file_id=EXCLUDED.file_id,
				file_path=EXCLUDED.file_path, request_id=EXCLUDED.request_id, updated_at=EXCLUDED.updated_at`,
			m.OrgID, m.MeetingID, jb(doc), m.Source, m.FileID, m.FilePath, m.RequestID, m.CreatedBy, m.CreatedAt, m.UpdatedAt)
		var pe interface{ SQLState() string }
		if errors.As(err, &pe) && pe.SQLState() == "23503" { // foreign key: no such meeting
			return meetings.ErrNotFound
		}
		return err
	})
}

func (p *MeetingsStore) GetMinutes(ctx context.Context, org, meetingID string) (meetings.Minutes, error) {
	var m meetings.Minutes
	err := p.S.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		var raw []byte
		err := tx.QueryRow(ctx, `SELECT doc, source, file_id, file_path, request_id, created_by, created_at, updated_at
			FROM meeting_minutes WHERE org_id=$1 AND meeting_id=$2`, org, meetingID).
			Scan(&raw, &m.Source, &m.FileID, &m.FilePath, &m.RequestID, &m.CreatedBy, &m.CreatedAt, &m.UpdatedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return meetings.ErrNotFound
		}
		if err != nil {
			return err
		}
		var doc struct {
			Summary       string                `json:"summary"`
			Decisions     []string              `json:"decisions"`
			ActionItems   []meetings.ActionItem `json:"action_items"`
			Lang          string                `json:"lang"`
			SummarySource string                `json:"summary_source"`
			Truncated     bool                  `json:"truncated"`
		}
		if err := json.Unmarshal(raw, &doc); err != nil {
			return err
		}
		m.OrgID, m.MeetingID, m.Summary, m.Decisions, m.ActionItems = org, meetingID, doc.Summary, doc.Decisions, doc.ActionItems
		m.Lang, m.SummarySource, m.Truncated = doc.Lang, doc.SummarySource, doc.Truncated
		return nil
	})
	return m, err
}

func (p *MeetingsStore) MinutesExist(ctx context.Context, org string, ids []string) (map[string]bool, error) {
	out := map[string]bool{}
	if len(ids) == 0 {
		return out, nil
	}
	err := p.S.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT meeting_id FROM meeting_minutes WHERE org_id=$1 AND meeting_id = ANY($2)`, org, ids)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return err
			}
			out[id] = true
		}
		return rows.Err()
	})
	return out, err
}

func (p *MeetingsStore) GetSettings(ctx context.Context, org string) (meetings.Settings, error) {
	var s meetings.Settings
	err := p.S.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		var raw []byte
		err := tx.QueryRow(ctx, `SELECT doc FROM meeting_settings WHERE org_id=$1`, org).Scan(&raw)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // no row: defaults
		}
		if err != nil {
			return err
		}
		return json.Unmarshal(raw, &s)
	})
	return s, err
}

func (p *MeetingsStore) PutSettings(ctx context.Context, org string, s meetings.Settings, by string) error {
	return p.S.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO meeting_settings (org_id, doc, updated_by, updated_at) VALUES ($1,$2,$3,now())
			ON CONFLICT (org_id) DO UPDATE SET doc=EXCLUDED.doc, updated_by=EXCLUDED.updated_by, updated_at=now()`, org, jb(s), by)
		return err
	})
}

func (p *MeetingsStore) CreateFeed(ctx context.Context, f meetings.Feed) error {
	return p.S.WithOrgTx(ctx, f.OrgID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO calendar_feeds (org_id, id, user_id, token_hash, label, created_at) VALUES ($1,$2,$3,$4,$5,$6)`,
			f.OrgID, f.ID, f.UserID, f.TokenHash, f.Label, f.CreatedAt)
		return err
	})
}

func (p *MeetingsStore) ListFeeds(ctx context.Context, org, user string) ([]meetings.Feed, error) {
	var out []meetings.Feed
	err := p.S.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id, user_id, label, created_at FROM calendar_feeds
			WHERE org_id=$1 AND user_id=$2 AND revoked_at IS NULL ORDER BY created_at, id`, org, user)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			f := meetings.Feed{OrgID: org}
			if err := rows.Scan(&f.ID, &f.UserID, &f.Label, &f.CreatedAt); err != nil {
				return err
			}
			out = append(out, f)
		}
		return rows.Err()
	})
	return out, err
}

func (p *MeetingsStore) RevokeFeed(ctx context.Context, org, user, id string, at time.Time) error {
	return p.S.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE calendar_feeds SET revoked_at=$4 WHERE org_id=$1 AND user_id=$2 AND id=$3 AND revoked_at IS NULL`, org, user, id, at)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return meetings.ErrNotFound
		}
		return nil
	})
}

func (p *MeetingsStore) FeedByHash(ctx context.Context, org, hash string) (meetings.Feed, error) {
	f := meetings.Feed{OrgID: org, TokenHash: hash}
	err := p.S.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `SELECT id, user_id, label, created_at FROM calendar_feeds WHERE org_id=$1 AND token_hash=$2 AND revoked_at IS NULL`, org, hash).
			Scan(&f.ID, &f.UserID, &f.Label, &f.CreatedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return meetings.ErrNotFound
		}
		return err
	})
	return f, err
}

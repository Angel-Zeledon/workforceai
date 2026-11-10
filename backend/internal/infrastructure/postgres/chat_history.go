package postgres

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

// Chat history on Postgres (application.ChatHistoryStore; migrations 570_chat_history.sql and
// 807_chat_people.sql). The General stream and the thread of an agent are queries over `messages`: no
// message is stored twice. threadPred is the SQL mirror of application.InThread plus the E0 visibility rule
// (application.VisibleTo): a person sees the shared rows (owner_id '') and their own private threads, never
// another person's.

var _ application.ChatHistoryStore = (*Store)(nil)

const chatConvs = `(m.conversation_id = 'office' OR m.conversation_id LIKE 'agent:%')`

// threadPred: $1 org, $2 agent id or ” for General, v = the placeholder of the viewer.
func threadPred(v string) string {
	return `m.org_id = $1 AND ` + chatConvs + ` AND m.owner_id IN ('', ` + v + `)
	AND ($2 = '' OR split_part(m.conversation_id, '@', 1) = 'agent:' || $2 OR m.from_id = $2 OR m.to_id = $2
	OR (m.from_id IN ('user','system') AND m.turn_id <> '' AND m.turn_id IN (
		SELECT t.turn_id FROM messages t WHERE t.org_id = $1 AND t.turn_id <> '' AND t.owner_id IN ('', ` + v + `)
		  AND (t.conversation_id = 'office' OR t.conversation_id LIKE 'agent:%') AND (t.from_id = $2 OR t.to_id = $2))))`
}

// unreadPred: what counts as unread for the viewer (application.UnreadFor): not what they wrote, not a user
// message without an author (written before E0).
func unreadPred(v string) string {
	return `NOT (m.from_id = 'user' AND m.author_id IN ('', ` + v + `))`
}

// readAtSQL is the viewer's marker of key: the newest of their own and the organization-wide baseline.
func readAtSQL(org, key, v string) string {
	return `COALESCE((SELECT max(r.read_at) FROM chat_reads r WHERE r.org_id = ` + org + ` AND r.key = ` + key +
		` AND r.user_id IN ('', ` + v + `)), '-infinity'::timestamptz)`
}

func likePattern(q string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + r.Replace(q) + "%"
}

func convSelect(v string) string {
	return `SELECT c.id, c.title, c.participants, c.request_id, c.last_message_at, c.archived_at, c.title_custom, c.owner_id,
	(SELECT count(*) FROM messages m WHERE m.org_id = c.org_id AND m.conversation_id = c.id),
	(SELECT count(*) FROM messages m WHERE m.org_id = c.org_id AND m.conversation_id = c.id AND ` + unreadPred(v) + `
		AND m.ts > ` + readAtSQL("c.org_id", "'conv:' || c.id", v) + `),
	COALESCE((SELECT m.text FROM messages m WHERE m.org_id = c.org_id AND m.conversation_id = c.id ORDER BY m.ts DESC, m.id DESC LIMIT 1), ''),
	COALESCE((SELECT m.from_id FROM messages m WHERE m.org_id = c.org_id AND m.conversation_id = c.id ORDER BY m.ts DESC, m.id DESC LIMIT 1), ''),
	COALESCE((SELECT m.to_id FROM messages m WHERE m.org_id = c.org_id AND m.conversation_id = c.id ORDER BY m.ts DESC, m.id DESC LIMIT 1), ''),
	COALESCE((SELECT m.text FROM messages m WHERE m.org_id = c.org_id AND m.conversation_id = c.id AND m.from_id = 'user' ORDER BY m.ts, m.id LIMIT 1), '')
	FROM conversations c`
}

func scanChatConv(r scanner) (application.ChatConversation, error) {
	var c application.ChatConversation
	var parts []byte
	var last, from, to, first string
	if err := r.Scan(&c.ID, &c.Title, &parts, &c.RequestID, &c.UpdatedAt, &c.ArchivedAt, &c.TitleCustom, &c.OwnerID, &c.MessageCount, &c.Unread, &last, &from, &to, &first); err != nil {
		return c, err
	}
	unmarshal(parts, &c.Participants)
	c.Participants = strs(c.Participants)
	c.LastMessageAt = c.UpdatedAt
	c.Kind, c.AgentID = application.ChatKind(c.ID)
	c.Archived = c.ArchivedAt != nil
	c.Preview, c.PreviewFrom, c.PreviewTo = application.Preview(last), from, to
	if strings.TrimSpace(c.Title) == "" {
		c.Title = application.Preview(first)
		if r := []rune(c.Title); len(r) > 80 {
			c.Title = string(r[:80])
		}
	}
	return c, nil
}

func (s *Store) ChatConversations(ctx context.Context, org string, q application.ChatConvQuery) ([]application.ChatConversation, bool, error) {
	pat := ""
	if q.Q != "" {
		pat = likePattern(q.Q)
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 500
	}
	rows, err := many(ctx, s, org, scanChatConv, convSelect("$8")+` WHERE c.org_id = $1 AND c.owner_id IN ('', $8)
		AND ($2 = 'all' OR (c.archived_at IS NOT NULL) = ($2 = 'only'))
		AND ($3 = '' OR ($3 = 'office' AND c.id = 'office') OR ($3 = 'agent' AND c.id LIKE 'agent:%')
			OR ($3 = 'request' AND c.id <> 'office' AND c.id NOT LIKE 'agent:%'))
		AND ($4 = '' OR split_part(c.id, '@', 1) = 'agent:' || $4 OR EXISTS (SELECT 1 FROM messages m WHERE m.org_id = c.org_id AND m.conversation_id = c.id
			AND (m.from_id = $4 OR m.to_id = $4)))
		AND ($5 = '' OR c.title ILIKE $5 ESCAPE '\' OR EXISTS (SELECT 1 FROM messages m WHERE m.org_id = c.org_id AND m.conversation_id = c.id
			AND m.text ILIKE $5 ESCAPE '\'))
		ORDER BY c.last_message_at DESC, c.id LIMIT $6 OFFSET $7`,
		org, q.Archived, q.Kind, q.AgentID, pat, limit+1, q.Offset, q.Viewer)
	if err != nil {
		return nil, false, err
	}
	more := len(rows) > limit
	if more {
		rows = rows[:limit]
	}
	return rows, more, nil
}

func (s *Store) ChatGetConversation(ctx context.Context, org, viewer, id string) (application.ChatConversation, error) {
	c, err := one(ctx, s, org, scanChatConv, convSelect("$3")+` WHERE c.org_id = $1 AND c.id = $2 AND c.owner_id IN ('', $3)`, org, id, viewer)
	return c, mapErr(err)
}

func (s *Store) ChatThreadMessages(ctx context.Context, org string, q application.ChatThreadQuery) ([]application.ChatMessage, bool, error) {
	agent := q.Thread
	if agent == application.ThreadGeneral {
		agent = ""
	}
	var out []application.ChatMessage
	more := false
	err := s.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		if q.BeforeID != "" {
			var ok bool
			if err := tx.QueryRow(ctx, `SELECT true FROM messages WHERE org_id = $1 AND id = $2 AND owner_id IN ('', $3)`, org, q.BeforeID, q.Viewer).Scan(&ok); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return domain.ErrNotFound
				}
				return err
			}
		}
		limit := q.Limit
		if limit <= 0 {
			limit = 50
		}
		rs, err := tx.Query(ctx, `SELECT m.id, m.conversation_id, m.from_id, m.to_id, m.kind, m.text, m.task_id, m.ts, m.turn_id, m.reply_to, m.request_id, m.files,
			m.author_id, m.author_name
			FROM messages m WHERE `+threadPred("$5")+`
			AND ($3 = '' OR (m.ts, m.id) < (SELECT b.ts, b.id FROM messages b WHERE b.org_id = $1 AND b.id = $3))
			ORDER BY m.ts DESC, m.id DESC LIMIT $4`, org, agent, q.BeforeID, limit+1, q.Viewer)
		if err != nil {
			return err
		}
		var msgs []domain.Message
		for rs.Next() {
			m, err := scanMessage(rs)
			if err != nil {
				rs.Close()
				return err
			}
			msgs = append(msgs, m)
		}
		rs.Close()
		if err := rs.Err(); err != nil {
			return err
		}
		if more = len(msgs) > limit; more {
			msgs = msgs[:limit]
		}
		// the agents of each turn (the badges)
		turnIDs := []string{}
		for _, m := range msgs {
			if m.TurnID != "" {
				turnIDs = append(turnIDs, m.TurnID)
			}
		}
		var part []domain.Message
		if len(turnIDs) > 0 {
			trs, err := tx.Query(ctx, `SELECT conversation_id, turn_id, from_id, to_id FROM messages m
				WHERE m.org_id = $1 AND m.turn_id = ANY($2) AND m.owner_id IN ('', $3) AND `+chatConvs, org, turnIDs, q.Viewer)
			if err != nil {
				return err
			}
			for trs.Next() {
				var p domain.Message
				if err := trs.Scan(&p.ConversationID, &p.TurnID, &p.From, &p.To); err != nil {
					trs.Close()
					return err
				}
				part = append(part, p)
			}
			trs.Close()
			if err := trs.Err(); err != nil {
				return err
			}
		}
		turns := application.TurnAgents(part)
		out = make([]application.ChatMessage, 0, len(msgs))
		for i := len(msgs) - 1; i >= 0; i-- { // oldest first
			out = append(out, application.WithAgentIDs(msgs[i], turns))
		}
		return nil
	})
	return out, more, err
}

func (s *Store) ChatThreads(ctx context.Context, org, viewer string, agentIDs, noShared []string) ([]application.ChatThread, error) {
	keys := append([]string{application.ThreadGeneral}, agentIDs...)
	hidden := make([]string, 0, len(noShared)) // D3: the stored shared 1:1 chats left out (agent:<id>, no owner)
	for _, id := range noShared {
		hidden = append(hidden, domain.ChatAgentConv(id))
	}
	out := make([]application.ChatThread, 0, len(keys))
	err := s.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		reads := map[string]time.Time{}
		rs, err := tx.Query(ctx, `SELECT key, max(read_at) FROM chat_reads WHERE org_id = $1 AND key LIKE 'thread:%' AND user_id IN ('', $2) GROUP BY key`, org, viewer)
		if err != nil {
			return err
		}
		for rs.Next() {
			var k string
			var at time.Time
			if err := rs.Scan(&k, &at); err != nil {
				rs.Close()
				return err
			}
			reads[k] = at
		}
		rs.Close()
		if err := rs.Err(); err != nil {
			return err
		}
		for _, key := range keys {
			agent := key
			if key == application.ThreadGeneral {
				agent = ""
			}
			t := application.ChatThread{Key: key, AgentID: agent}
			if err := tx.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE `+unreadPred("$4")+` AND m.ts > $3) FROM messages m WHERE `+threadPred("$4")+
				` AND NOT (m.conversation_id = ANY($5::text[]))`,
				org, agent, reads["thread:"+key], viewer, hidden).Scan(&t.MessageCount, &t.Unread); err != nil {
				return err
			}
			if t.MessageCount > 0 {
				var text string
				if err := tx.QueryRow(ctx, `SELECT m.text, m.from_id, m.to_id, m.conversation_id, m.ts FROM messages m WHERE `+threadPred("$3")+
					` AND NOT (m.conversation_id = ANY($4::text[])) ORDER BY m.ts DESC, m.id DESC LIMIT 1`,
					org, agent, viewer, hidden).Scan(&text, &t.PreviewFrom, &t.PreviewTo, &t.PreviewConv, &t.LastMessageAt); err != nil {
					return err
				}
				t.Preview = application.Preview(text)
			}
			out = append(out, t)
		}
		return nil
	})
	return out, err
}

func (s *Store) ChatMarkRead(ctx context.Context, org, viewer, key string, at time.Time) error {
	const up = ` ON CONFLICT (org_id, user_id, key) DO UPDATE SET read_at = GREATEST(chat_reads.read_at, EXCLUDED.read_at)`
	return s.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO chat_reads (org_id, user_id, key, read_at) VALUES ($1,$2,$3,$4)`+up, org, viewer, key, at); err != nil {
			return err
		}
		t, ok := strings.CutPrefix(key, "thread:")
		if !ok {
			return nil
		}
		// reading a thread reads the 1:1 chats it contains (the viewer's own and the earlier shared history)
		if t == application.ThreadGeneral {
			_, err := tx.Exec(ctx, `INSERT INTO chat_reads (org_id, user_id, key, read_at)
				SELECT org_id, $2, 'conv:' || id, $3 FROM conversations WHERE org_id = $1 AND owner_id IN ('', $2)
				AND (id = 'office' OR id LIKE 'agent:%')`+up, org, viewer, at)
			return err
		}
		convs := []string{domain.ChatAgentConv(t)}
		if viewer != "" {
			convs = append(convs, domain.ChatPrivateConv(t, viewer))
		}
		for _, c := range convs {
			if _, err := tx.Exec(ctx, `INSERT INTO chat_reads (org_id, user_id, key, read_at) VALUES ($1,$2,$3,$4)`+up, org, viewer, "conv:"+c, at); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) ChatPatchConversation(ctx context.Context, org, id string, p application.ChatConvPatch) error {
	tag, err := s.execTag(ctx, org, `UPDATE conversations SET
		title = COALESCE($3::text, title),
		title_custom = title_custom OR $3::text IS NOT NULL,
		archived_at = CASE WHEN $4::boolean IS NULL THEN archived_at WHEN $4::boolean THEN COALESCE(archived_at, now()) ELSE NULL END
		WHERE org_id = $1 AND id = $2`, org, id, p.Title, p.Archived)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (s *Store) ChatDeleteConversation(ctx context.Context, org, id string) (int, error) {
	n := 0
	err := s.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM conversations WHERE org_id = $1 AND id = $2`, org, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return domain.ErrNotFound
		}
		tag, err = tx.Exec(ctx, `DELETE FROM messages WHERE org_id = $1 AND conversation_id = $2`, org, id)
		if err != nil {
			return err
		}
		n = int(tag.RowsAffected())
		_, err = tx.Exec(ctx, `DELETE FROM chat_reads WHERE org_id = $1 AND key = $2`, org, "conv:"+id)
		return err
	})
	return n, err
}

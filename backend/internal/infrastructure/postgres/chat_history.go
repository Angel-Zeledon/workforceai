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

// Chat history on Postgres (application.ChatHistoryStore; migration
// 570_chat_history.sql). The General stream and the thread of an agent are
// queries over `messages`: no message is stored twice. threadPred is the SQL
// mirror of application.InThread ($1 org, $2 agent id or '' for General).

var _ application.ChatHistoryStore = (*Store)(nil)

const chatConvs = `(m.conversation_id = 'office' OR m.conversation_id LIKE 'agent:%')`

const threadPred = `m.org_id = $1 AND ` + chatConvs + ` AND ($2 = '' OR m.conversation_id = 'agent:' || $2 OR m.from_id = $2 OR m.to_id = $2
	OR (m.from_id IN ('user','system') AND m.turn_id <> '' AND m.turn_id IN (
		SELECT t.turn_id FROM messages t WHERE t.org_id = $1 AND t.turn_id <> ''
		  AND (t.conversation_id = 'office' OR t.conversation_id LIKE 'agent:%') AND (t.from_id = $2 OR t.to_id = $2))))`

func likePattern(q string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + r.Replace(q) + "%"
}

const convSelect = `SELECT c.id, c.title, c.participants, c.request_id, c.last_message_at, c.archived_at, c.title_custom,
	(SELECT count(*) FROM messages m WHERE m.org_id = c.org_id AND m.conversation_id = c.id),
	(SELECT count(*) FROM messages m WHERE m.org_id = c.org_id AND m.conversation_id = c.id AND m.from_id <> 'user'
		AND m.ts > COALESCE((SELECT r.read_at FROM chat_reads r WHERE r.org_id = c.org_id AND r.key = 'conv:' || c.id), '-infinity'::timestamptz)),
	COALESCE((SELECT m.text FROM messages m WHERE m.org_id = c.org_id AND m.conversation_id = c.id ORDER BY m.ts DESC, m.id DESC LIMIT 1), ''),
	COALESCE((SELECT m.from_id FROM messages m WHERE m.org_id = c.org_id AND m.conversation_id = c.id ORDER BY m.ts DESC, m.id DESC LIMIT 1), ''),
	COALESCE((SELECT m.text FROM messages m WHERE m.org_id = c.org_id AND m.conversation_id = c.id AND m.from_id = 'user' ORDER BY m.ts, m.id LIMIT 1), '')
	FROM conversations c`

func scanChatConv(r scanner) (application.ChatConversation, error) {
	var c application.ChatConversation
	var parts []byte
	var last, from, first string
	if err := r.Scan(&c.ID, &c.Title, &parts, &c.RequestID, &c.UpdatedAt, &c.ArchivedAt, &c.TitleCustom, &c.MessageCount, &c.Unread, &last, &from, &first); err != nil {
		return c, err
	}
	unmarshal(parts, &c.Participants)
	c.Participants = strs(c.Participants)
	c.LastMessageAt = c.UpdatedAt
	c.Kind, c.AgentID = application.ChatKind(c.ID)
	c.Archived = c.ArchivedAt != nil
	c.Preview, c.PreviewFrom = application.Preview(last), from
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
	rows, err := many(ctx, s, org, scanChatConv, convSelect+` WHERE c.org_id = $1
		AND ($2 = 'all' OR (c.archived_at IS NOT NULL) = ($2 = 'only'))
		AND ($3 = '' OR ($3 = 'office' AND c.id = 'office') OR ($3 = 'agent' AND c.id LIKE 'agent:%')
			OR ($3 = 'request' AND c.id <> 'office' AND c.id NOT LIKE 'agent:%'))
		AND ($4 = '' OR c.id = 'agent:' || $4 OR EXISTS (SELECT 1 FROM messages m WHERE m.org_id = c.org_id AND m.conversation_id = c.id
			AND (m.from_id = $4 OR m.to_id = $4)))
		AND ($5 = '' OR c.title ILIKE $5 ESCAPE '\' OR EXISTS (SELECT 1 FROM messages m WHERE m.org_id = c.org_id AND m.conversation_id = c.id
			AND m.text ILIKE $5 ESCAPE '\'))
		ORDER BY c.last_message_at DESC, c.id LIMIT $6 OFFSET $7`,
		org, q.Archived, q.Kind, q.AgentID, pat, limit+1, q.Offset)
	if err != nil {
		return nil, false, err
	}
	more := len(rows) > limit
	if more {
		rows = rows[:limit]
	}
	return rows, more, nil
}

func (s *Store) ChatGetConversation(ctx context.Context, org, id string) (application.ChatConversation, error) {
	c, err := one(ctx, s, org, scanChatConv, convSelect+` WHERE c.org_id = $1 AND c.id = $2`, org, id)
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
			if err := tx.QueryRow(ctx, `SELECT true FROM messages WHERE org_id = $1 AND id = $2`, org, q.BeforeID).Scan(&ok); err != nil {
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
		rs, err := tx.Query(ctx, `SELECT m.id, m.conversation_id, m.from_id, m.to_id, m.kind, m.text, m.task_id, m.ts, m.turn_id, m.reply_to, m.request_id, m.files
			FROM messages m WHERE `+threadPred+`
			AND ($3 = '' OR (m.ts, m.id) < (SELECT b.ts, b.id FROM messages b WHERE b.org_id = $1 AND b.id = $3))
			ORDER BY m.ts DESC, m.id DESC LIMIT $4`, org, agent, q.BeforeID, limit+1)
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
				WHERE m.org_id = $1 AND m.turn_id = ANY($2) AND `+chatConvs, org, turnIDs)
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

func (s *Store) ChatThreads(ctx context.Context, org string, agentIDs []string) ([]application.ChatThread, error) {
	keys := append([]string{application.ThreadGeneral}, agentIDs...)
	out := make([]application.ChatThread, 0, len(keys))
	err := s.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		reads := map[string]time.Time{}
		rs, err := tx.Query(ctx, `SELECT key, read_at FROM chat_reads WHERE org_id = $1 AND key LIKE 'thread:%'`, org)
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
			if err := tx.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE m.from_id <> 'user' AND m.ts > $3) FROM messages m WHERE `+threadPred,
				org, agent, reads["thread:"+key]).Scan(&t.MessageCount, &t.Unread); err != nil {
				return err
			}
			if t.MessageCount > 0 {
				var text string
				if err := tx.QueryRow(ctx, `SELECT m.text, m.from_id, m.ts FROM messages m WHERE `+threadPred+` ORDER BY m.ts DESC, m.id DESC LIMIT 1`,
					org, agent).Scan(&text, &t.PreviewFrom, &t.LastMessageAt); err != nil {
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

func (s *Store) ChatMarkRead(ctx context.Context, org, key string, at time.Time) error {
	const up = ` ON CONFLICT (org_id, key) DO UPDATE SET read_at = GREATEST(chat_reads.read_at, EXCLUDED.read_at)`
	return s.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO chat_reads (org_id, key, read_at) VALUES ($1,$2,$3)`+up, org, key, at); err != nil {
			return err
		}
		t, ok := strings.CutPrefix(key, "thread:")
		if !ok {
			return nil
		}
		// reading a thread reads the 1:1 chats it contains
		if t == application.ThreadGeneral {
			_, err := tx.Exec(ctx, `INSERT INTO chat_reads (org_id, key, read_at)
				SELECT org_id, 'conv:' || id, $2 FROM conversations WHERE org_id = $1 AND (id = 'office' OR id LIKE 'agent:%')`+up, org, at)
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO chat_reads (org_id, key, read_at) VALUES ($1,$2,$3)`+up, org, "conv:"+domain.ChatAgentConv(t), at)
		return err
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

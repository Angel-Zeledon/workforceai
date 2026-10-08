// Package postgres implements application.Store on PostgreSQL (pgx).
//
// Multi-tenancy: every tenant table is under Row-Level Security (migrations
// 202/203). Each store call runs in its own short transaction that starts with
// set_config('app.org_id', <org>, true) (see WithOrgTx), and the pool connects
// as a plain NOBYPASSRLS role (Options.AppRole), so the database itself enforces
// isolation even if a query forgets its `WHERE org_id = ...`.
package postgres

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/domain"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

type Store struct{ pool *pgxpool.Pool }

// Options configures Open.
type Options struct {
	// URL is the application connection string.
	URL string
	// MigrateURL (optional) is the owner/DDL connection used only to apply
	// migrations; defaults to URL.
	MigrateURL string
	// AppRole (optional) is the role every application connection switches to
	// with SET ROLE. It must not be a superuser, own the tables or have
	// BYPASSRLS (migration 203 creates app_user). Empty disables the switch.
	AppRole string
}

// New connects with the default app role ("app_user") and applies the embedded migrations.
func New(ctx context.Context, url string) (*Store, error) {
	return Open(ctx, Options{URL: url, AppRole: "app_user"})
}

// Open applies the embedded migrations (with MigrateURL/URL) and returns a
// store whose pool runs as AppRole.
func Open(ctx context.Context, o Options) (*Store, error) {
	if o.URL == "" {
		return nil, errors.New("postgres: URL is required")
	}
	if err := Migrate(ctx, firstNonEmpty(o.MigrateURL, o.URL)); err != nil {
		return nil, err
	}
	cfg, err := pgxpool.ParseConfig(o.URL)
	if err != nil {
		return nil, fmt.Errorf("parse postgres url: %w", err)
	}
	if o.AppRole != "" {
		stmt := "SET ROLE " + pgx.Identifier{o.AppRole}.Sanitize()
		cfg.AfterConnect = func(ctx context.Context, c *pgx.Conn) error {
			_, err := c.Exec(ctx, stmt)
			return err
		}
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect postgres: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres (role %q): %w", o.AppRole, err)
	}
	return &Store{pool: pool}, nil
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func (s *Store) Close() { s.pool.Close() }

// Pool exposes the (RLS-restricted) pool, e.g. for the auth store, whose
// tables (users, memberships, refresh_tokens) are deliberately outside RLS.
func (s *Store) Pool() *pgxpool.Pool { return s.pool }

// WithOrgTx runs fn in a transaction scoped to orgID for Row-Level Security
// (set_config('app.org_id', orgID, true)). It commits when fn returns nil.
func (s *Store) WithOrgTx(ctx context.Context, orgID string, fn func(tx pgx.Tx) error) error {
	return auth.WithOrgTx(ctx, s.pool, orgID, fn)
}

// Migrate applies the embedded migrations in name order, once each.
func Migrate(ctx context.Context, url string) error {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return fmt.Errorf("connect postgres: %w", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		return fmt.Errorf("ping postgres: %w", err)
	}
	return migrate(ctx, pool)
}

func migrate(ctx context.Context, pool *pgxpool.Pool) error {
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		// Serialize concurrent starters.
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(727274)`); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`); err != nil {
			return err
		}
		for _, n := range names {
			var done bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version=$1)`, n).Scan(&done); err != nil {
				return err
			}
			if done {
				continue
			}
			sql, err := migrationsFS.ReadFile("migrations/" + n)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, string(sql)); err != nil {
				return fmt.Errorf("migration %s: %w", n, err)
			}
			if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, n); err != nil {
				return err
			}
		}
		return nil
	})
}

// ---- helpers ----

type scanner interface{ Scan(dest ...any) error }

func jb(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return []byte("null")
	}
	return b
}

func strs(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func unmarshal(raw []byte, dst any) {
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, dst)
	}
}

func mapErr(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	return err
}

// exec runs one statement in an org-scoped transaction.
func (s *Store) exec(ctx context.Context, orgID, sql string, args ...any) error {
	_, err := s.execTag(ctx, orgID, sql, args...)
	return err
}

func (s *Store) execTag(ctx context.Context, orgID, sql string, args ...any) (tag pgconn.CommandTag, err error) {
	err = s.WithOrgTx(ctx, orgID, func(tx pgx.Tx) (e error) {
		tag, e = tx.Exec(ctx, sql, args...)
		return e
	})
	return tag, err
}

// one scans a single row of an org-scoped query.
func one[T any](ctx context.Context, s *Store, orgID string, scan func(scanner) (T, error), sql string, args ...any) (T, error) {
	var v T
	err := s.WithOrgTx(ctx, orgID, func(tx pgx.Tx) (e error) {
		v, e = scan(tx.QueryRow(ctx, sql, args...))
		return e
	})
	return v, err
}

// many scans all rows of an org-scoped query (never nil).
func many[T any](ctx context.Context, s *Store, orgID string, scan func(scanner) (T, error), sql string, args ...any) ([]T, error) {
	out := []T{}
	err := s.WithOrgTx(ctx, orgID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, sql, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			v, err := scan(rows)
			if err != nil {
				return err
			}
			out = append(out, v)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ---- seed / reset ----

func (s *Store) Seed(ctx context.Context, org domain.Organization, agents []domain.Agent) error {
	return s.WithOrgTx(ctx, org.ID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO organizations (id, name, slug, budget_usd) VALUES ($1,$2,$3,$4)
			ON CONFLICT (id) DO UPDATE SET budget_usd = EXCLUDED.budget_usd`, org.ID, org.Name, org.Slug, org.BudgetUSD); err != nil {
			return err
		}
		for i, a := range agents {
			if _, err := tx.Exec(ctx, `INSERT INTO agents (org_id, id, name, role, title, description, persona, responsibilities, state, activity, progress, tools, permissions, autonomy, position)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,0,$11,$12,$13,$14) ON CONFLICT (org_id, id) DO NOTHING`,
				org.ID, a.ID, a.Name, a.Role, a.Title, a.Description, a.Persona, jb(strs(a.Responsibilities)), string(a.State), a.Activity,
				jb(strs(a.Tools)), jb(strs(a.Permissions)), a.Autonomy, i); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) Reset(ctx context.Context, orgID string) error {
	return s.WithOrgTx(ctx, orgID, func(tx pgx.Tx) error {
		for _, t := range []string{"tasks", "requests", "messages", "conversations", "approvals", "reports", "memories", "events", "activity", "usage_entries"} {
			if _, err := tx.Exec(ctx, "DELETE FROM "+t+" WHERE org_id=$1", orgID); err != nil {
				return err
			}
		}
		// Request caps are execution data; agent caps are configuration and survive a reset.
		if _, err := tx.Exec(ctx, `DELETE FROM budget_caps WHERE org_id=$1 AND scope='request'`, orgID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE agents SET state='idle', activity='Disponible', current_task_id=NULL, progress=0 WHERE org_id=$1`, orgID)
		return err
	})
}

// ---- agents ----

const agentCols = `id, name, role, title, description, persona, responsibilities, state, activity, current_task_id, progress, tools, permissions, autonomy`

func scanAgent(r scanner) (domain.Agent, error) {
	var a domain.Agent
	var resp, tools, perms []byte
	var state string
	err := r.Scan(&a.ID, &a.Name, &a.Role, &a.Title, &a.Description, &a.Persona, &resp, &state, &a.Activity, &a.CurrentTaskID, &a.Progress, &tools, &perms, &a.Autonomy)
	a.State = domain.AgentState(state)
	unmarshal(resp, &a.Responsibilities)
	unmarshal(tools, &a.Tools)
	unmarshal(perms, &a.Permissions)
	a.Responsibilities, a.Tools, a.Permissions = strs(a.Responsibilities), strs(a.Tools), strs(a.Permissions)
	return a, err
}

func (s *Store) ListAgents(ctx context.Context, orgID string) ([]domain.Agent, error) {
	return many(ctx, s, orgID, scanAgent, `SELECT `+agentCols+` FROM agents WHERE org_id=$1 ORDER BY position, id`, orgID)
}

// CreateAgent adds an agent at the end of the office (hiring from a role template).
func (s *Store) CreateAgent(ctx context.Context, orgID string, a domain.Agent) error {
	tag, err := s.execTag(ctx, orgID, `INSERT INTO agents (org_id, id, name, role, title, description, persona, responsibilities, state, activity, progress, tools, permissions, autonomy, position)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,0,$11,$12,$13,(SELECT COALESCE(MAX(position)+1, 0) FROM agents WHERE org_id=$1)) ON CONFLICT (org_id, id) DO NOTHING`,
		orgID, a.ID, a.Name, a.Role, a.Title, a.Description, a.Persona, jb(strs(a.Responsibilities)), string(a.State), a.Activity,
		jb(strs(a.Tools)), jb(strs(a.Permissions)), a.Autonomy)
	if err == nil && tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: agent %s already exists", domain.ErrConflict, a.ID)
	}
	return err
}

func (s *Store) GetAgent(ctx context.Context, orgID, id string) (domain.Agent, error) {
	a, err := one(ctx, s, orgID, scanAgent, `SELECT `+agentCols+` FROM agents WHERE org_id=$1 AND id=$2`, orgID, id)
	return a, mapErr(err)
}

func (s *Store) UpdateAgentState(ctx context.Context, orgID, id string, st domain.AgentState, activity string, taskID *string, progress int) error {
	return s.exec(ctx, orgID, `UPDATE agents SET state=$3, activity=$4, current_task_id=$5, progress=$6 WHERE org_id=$1 AND id=$2`,
		orgID, id, string(st), activity, taskID, progress)
}

// ---- tasks ----

const taskCols = `id, request_id, workflow_id, title, description, agent_id, status, depends_on, parent_task_id, depth, cost_usd, output, created_at, started_at, finished_at, assigned_reason`

func scanTask(r scanner) (domain.Task, error) {
	var t domain.Task
	var status string
	var deps, out []byte
	err := r.Scan(&t.ID, &t.RequestID, &t.WorkflowID, &t.Title, &t.Description, &t.AgentID, &status, &deps, &t.ParentTaskID, &t.Depth, &t.CostUSD, &out, &t.CreatedAt, &t.StartedAt, &t.FinishedAt, &t.AssignedReason)
	t.Status = domain.TaskStatus(status)
	unmarshal(deps, &t.DependsOn)
	t.DependsOn = strs(t.DependsOn)
	if len(out) > 0 {
		var o domain.StructuredOutput
		unmarshal(out, &o)
		o.Normalize()
		t.Output = &o
	}
	return t, err
}

func (s *Store) CreateTask(ctx context.Context, orgID string, t domain.Task) error {
	return s.exec(ctx, orgID, `INSERT INTO tasks (id, org_id, request_id, workflow_id, title, description, agent_id, status, depends_on, parent_task_id, depth, output, created_at, started_at, finished_at, assigned_reason)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`,
		t.ID, orgID, t.RequestID, t.WorkflowID, t.Title, t.Description, t.AgentID, string(t.Status), jb(strs(t.DependsOn)), t.ParentTaskID, t.Depth, outJSON(t.Output), t.CreatedAt, t.StartedAt, t.FinishedAt, t.AssignedReason)
}

func outJSON(o *domain.StructuredOutput) []byte {
	if o == nil {
		return nil
	}
	return jb(o)
}

func (s *Store) GetTask(ctx context.Context, orgID, id string) (domain.Task, error) {
	t, err := one(ctx, s, orgID, scanTask, `SELECT `+taskCols+` FROM tasks WHERE org_id=$1 AND id=$2`, orgID, id)
	return t, mapErr(err)
}

func (s *Store) UpdateTask(ctx context.Context, orgID string, t domain.Task) error {
	tag, err := s.execTag(ctx, orgID, `UPDATE tasks SET status=$3, output=$4, started_at=$5, finished_at=$6 WHERE org_id=$1 AND id=$2`,
		orgID, t.ID, string(t.Status), outJSON(t.Output), t.StartedAt, t.FinishedAt)
	if err == nil && tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return err
}

func (s *Store) ListTasks(ctx context.Context, orgID, agentID, status string) ([]domain.Task, error) {
	return many(ctx, s, orgID, scanTask, `SELECT `+taskCols+` FROM tasks WHERE org_id=$1 AND ($2='' OR agent_id=$2) AND ($3='' OR status=$3) ORDER BY created_at DESC, id`, orgID, agentID, status)
}

func (s *Store) ListTasksByRequest(ctx context.Context, orgID, requestID string) ([]domain.Task, error) {
	return many(ctx, s, orgID, scanTask, `SELECT `+taskCols+` FROM tasks WHERE org_id=$1 AND request_id=$2 ORDER BY created_at, id`, orgID, requestID)
}

// ---- requests ----

const reqCols = `id, text, status, plan, report_id, cost_usd, created_at`

func scanRequest(r scanner) (domain.Request, error) {
	var q domain.Request
	var status string
	var plan []byte
	err := r.Scan(&q.ID, &q.Text, &status, &plan, &q.ReportID, &q.CostUSD, &q.CreatedAt)
	q.Status = domain.RequestStatus(status)
	unmarshal(plan, &q.Plan)
	return q, err
}

func (s *Store) CreateRequest(ctx context.Context, orgID string, r domain.Request) error {
	return s.exec(ctx, orgID, `INSERT INTO requests (id, org_id, text, status, plan, report_id, created_at) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		r.ID, orgID, r.Text, string(r.Status), jb(r.Plan), r.ReportID, r.CreatedAt)
}

func (s *Store) GetRequest(ctx context.Context, orgID, id string) (domain.Request, error) {
	r, err := one(ctx, s, orgID, scanRequest, `SELECT `+reqCols+` FROM requests WHERE org_id=$1 AND id=$2`, orgID, id)
	return r, mapErr(err)
}

func (s *Store) UpdateRequest(ctx context.Context, orgID string, r domain.Request) error {
	return s.exec(ctx, orgID, `UPDATE requests SET status=$3, plan=$4, report_id=$5 WHERE org_id=$1 AND id=$2`,
		orgID, r.ID, string(r.Status), jb(r.Plan), r.ReportID)
}

func (s *Store) ListRequests(ctx context.Context, orgID string) ([]domain.Request, error) {
	return many(ctx, s, orgID, scanRequest, `SELECT `+reqCols+` FROM requests WHERE org_id=$1 ORDER BY created_at DESC`, orgID)
}

func (s *Store) AddCost(ctx context.Context, orgID, requestID, taskID string, usd float64) error {
	return s.WithOrgTx(ctx, orgID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE requests SET cost_usd = cost_usd + $3 WHERE org_id=$1 AND id=$2`, orgID, requestID, usd); err != nil {
			return err
		}
		if taskID == "" {
			return nil
		}
		_, err := tx.Exec(ctx, `UPDATE tasks SET cost_usd = cost_usd + $3 WHERE org_id=$1 AND id=$2`, orgID, taskID, usd)
		return err
	})
}

func (s *Store) OrgCost(ctx context.Context, orgID string) (float64, error) {
	return one(ctx, s, orgID, func(r scanner) (c float64, err error) { err = r.Scan(&c); return },
		`SELECT COALESCE(SUM(cost_usd),0) FROM requests WHERE org_id=$1`, orgID)
}

// ---- cost ledger / budget caps ----

func (s *Store) AddUsage(ctx context.Context, orgID string, u domain.UsageEntry) error {
	if u.TS.IsZero() {
		u.TS = time.Now().UTC()
	}
	return s.exec(ctx, orgID, `INSERT INTO usage_entries (id, org_id, request_id, task_id, agent_id, kind, model, input_tokens, output_tokens, cost_usd, tools, ts)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		u.ID, orgID, u.RequestID, u.TaskID, u.AgentID, string(u.Kind), u.Model, u.InputTokens, u.OutputTokens, u.CostUSD, jb(strs(u.Tools)), u.TS)
}

func (s *Store) ListUsage(ctx context.Context, orgID string) ([]domain.UsageEntry, error) {
	return many(ctx, s, orgID, func(r scanner) (domain.UsageEntry, error) {
		var u domain.UsageEntry
		var kind string
		var tools []byte
		err := r.Scan(&u.ID, &u.RequestID, &u.TaskID, &u.AgentID, &kind, &u.Model, &u.InputTokens, &u.OutputTokens, &u.CostUSD, &tools, &u.TS)
		u.Kind = domain.UsageKind(kind)
		unmarshal(tools, &u.Tools)
		u.Tools = strs(u.Tools)
		return u, err
	}, `SELECT id, request_id, task_id, agent_id, kind, model, input_tokens, output_tokens, cost_usd, tools, ts FROM usage_entries WHERE org_id=$1 ORDER BY ts, id`, orgID)
}

func (s *Store) SetBudgetCap(ctx context.Context, orgID string, scope domain.BudgetScope, scopeID string, capUSD float64) error {
	if capUSD <= 0 {
		return s.exec(ctx, orgID, `DELETE FROM budget_caps WHERE org_id=$1 AND scope=$2 AND scope_id=$3`, orgID, string(scope), scopeID)
	}
	return s.exec(ctx, orgID, `INSERT INTO budget_caps (org_id, scope, scope_id, cap_usd) VALUES ($1,$2,$3,$4)
		ON CONFLICT (org_id, scope, scope_id) DO UPDATE SET cap_usd=EXCLUDED.cap_usd, updated_at=now()`,
		orgID, string(scope), scopeID, capUSD)
}

func (s *Store) ListBudgetCaps(ctx context.Context, orgID string) ([]domain.BudgetCap, error) {
	return many(ctx, s, orgID, func(r scanner) (domain.BudgetCap, error) {
		var c domain.BudgetCap
		var scope string
		err := r.Scan(&scope, &c.ScopeID, &c.CapUSD)
		c.Scope = domain.BudgetScope(scope)
		return c, err
	}, `SELECT scope, scope_id, cap_usd FROM budget_caps WHERE org_id=$1 ORDER BY scope, scope_id`, orgID)
}

// ---- conversations / messages ----

func scanConv(r scanner) (domain.Conversation, error) {
	var c domain.Conversation
	var parts []byte
	err := r.Scan(&c.ID, &c.Title, &parts, &c.RequestID, &c.LastMessageAt)
	unmarshal(parts, &c.Participants)
	c.Participants = strs(c.Participants)
	return c, err
}

func (s *Store) CreateConversation(ctx context.Context, orgID string, c domain.Conversation) error {
	return s.exec(ctx, orgID, `INSERT INTO conversations (id, org_id, title, participants, request_id, last_message_at) VALUES ($1,$2,$3,$4,$5,$6)`,
		c.ID, orgID, c.Title, jb(strs(c.Participants)), c.RequestID, c.LastMessageAt)
}

func (s *Store) GetConversation(ctx context.Context, orgID, id string) (domain.Conversation, error) {
	c, err := one(ctx, s, orgID, scanConv, `SELECT id, title, participants, request_id, last_message_at FROM conversations WHERE org_id=$1 AND id=$2`, orgID, id)
	return c, mapErr(err)
}

func (s *Store) ListConversations(ctx context.Context, orgID string) ([]domain.Conversation, error) {
	return many(ctx, s, orgID, scanConv, `SELECT id, title, participants, request_id, last_message_at FROM conversations WHERE org_id=$1 ORDER BY last_message_at DESC`, orgID)
}

func (s *Store) AddMessage(ctx context.Context, orgID string, m domain.Message) error {
	return s.WithOrgTx(ctx, orgID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO messages (id, org_id, conversation_id, from_id, to_id, kind, text, task_id, ts, turn_id, reply_to, request_id) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
			m.ID, orgID, m.ConversationID, m.From, m.To, m.Kind, m.Text, m.TaskID, m.TS, m.TurnID, m.ReplyTo, m.RequestID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE conversations SET last_message_at=$3 WHERE org_id=$1 AND id=$2`, orgID, m.ConversationID, m.TS); err != nil {
			return err
		}
		for _, p := range []string{m.From, m.To} {
			if p == "all" || p == "system" {
				continue
			}
			if _, err := tx.Exec(ctx, `UPDATE conversations SET participants = participants || to_jsonb($3::text)
				WHERE org_id=$1 AND id=$2 AND NOT participants ? $3::text`, orgID, m.ConversationID, p); err != nil {
				return err
			}
		}
		return nil
	})
}

const msgCols = `id, conversation_id, from_id, to_id, kind, text, task_id, ts, turn_id, reply_to, request_id`

func scanMessage(r scanner) (domain.Message, error) {
	var m domain.Message
	err := r.Scan(&m.ID, &m.ConversationID, &m.From, &m.To, &m.Kind, &m.Text, &m.TaskID, &m.TS, &m.TurnID, &m.ReplyTo, &m.RequestID)
	return m, err
}

func (s *Store) ListMessages(ctx context.Context, orgID, conversationID string) ([]domain.Message, error) {
	return many(ctx, s, orgID, scanMessage, `SELECT `+msgCols+` FROM messages WHERE org_id=$1 AND conversation_id=$2 ORDER BY ts, id`, orgID, conversationID)
}

// ListMessagesPage implements application.MessagePager: the `limit` messages
// older than beforeID ("" = the newest), oldest first, and whether older ones remain.
func (s *Store) ListMessagesPage(ctx context.Context, orgID, conversationID, beforeID string, limit int) ([]domain.Message, bool, error) {
	if limit <= 0 {
		all, err := s.ListMessages(ctx, orgID, conversationID)
		return all, false, err
	}
	rows, err := many(ctx, s, orgID, scanMessage, `SELECT `+msgCols+` FROM messages
		WHERE org_id=$1 AND conversation_id=$2
		  AND ($3 = '' OR (ts, id) < (SELECT ts, id FROM messages WHERE org_id=$1 AND id=$3))
		ORDER BY ts DESC, id DESC LIMIT $4`, orgID, conversationID, beforeID, limit+1)
	if err != nil {
		return nil, false, err
	}
	more := len(rows) > limit
	if more {
		rows = rows[:limit]
	}
	slices.Reverse(rows)
	return rows, more, nil
}

var _ application.MessagePager = (*Store)(nil)

// ---- approvals ----

const apprCols = `id, task_id, agent_id, action, title, details, risk, status, note, created_at, resolved_at,
	required_approvals, required_role, requested_by, no_self_approval, policy_rule, decisions`

func scanApproval(r scanner) (domain.Approval, error) {
	var a domain.Approval
	var st string
	var decisions []byte
	err := r.Scan(&a.ID, &a.TaskID, &a.AgentID, &a.Action, &a.Title, &a.Details, &a.Risk, &st, &a.Note, &a.CreatedAt, &a.ResolvedAt,
		&a.RequiredApprovals, &a.RequiredRole, &a.RequestedBy, &a.NoSelfApproval, &a.PolicyRule, &decisions)
	a.Status = domain.ApprovalStatus(st)
	unmarshal(decisions, &a.Decisions)
	if a.Decisions == nil {
		a.Decisions = []domain.ApprovalDecision{}
	}
	return a, err
}

func (s *Store) CreateApproval(ctx context.Context, orgID string, a domain.Approval) error {
	if a.RequiredApprovals < 1 {
		a.RequiredApprovals = 1
	}
	if a.Decisions == nil {
		a.Decisions = []domain.ApprovalDecision{}
	}
	return s.exec(ctx, orgID, `INSERT INTO approvals (id, org_id, task_id, agent_id, action, title, details, risk, status, note, created_at,
		required_approvals, required_role, requested_by, no_self_approval, policy_rule, decisions)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)`,
		a.ID, orgID, a.TaskID, a.AgentID, a.Action, a.Title, a.Details, a.Risk, string(a.Status), a.Note, a.CreatedAt,
		a.RequiredApprovals, a.RequiredRole, a.RequestedBy, a.NoSelfApproval, a.PolicyRule, jb(a.Decisions))
}

func (s *Store) GetApproval(ctx context.Context, orgID, id string) (domain.Approval, error) {
	a, err := one(ctx, s, orgID, scanApproval, `SELECT `+apprCols+` FROM approvals WHERE org_id=$1 AND id=$2`, orgID, id)
	return a, mapErr(err)
}

// UpdateApproval only changes approvals that are still pending, so two
// concurrent decisions cannot both win. It also stores the decisions received
// so far (double approval keeps the approval pending after the first one).
func (s *Store) UpdateApproval(ctx context.Context, orgID string, a domain.Approval) error {
	if a.Decisions == nil {
		a.Decisions = []domain.ApprovalDecision{}
	}
	tag, err := s.execTag(ctx, orgID, `UPDATE approvals SET status=$3, note=$4, resolved_at=$5, decisions=$6 WHERE org_id=$1 AND id=$2 AND status='pending'`,
		orgID, a.ID, string(a.Status), a.Note, a.ResolvedAt, jb(a.Decisions))
	if err == nil && tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: approval already resolved", domain.ErrConflict)
	}
	return err
}

func (s *Store) ListApprovals(ctx context.Context, orgID, status string) ([]domain.Approval, error) {
	return many(ctx, s, orgID, scanApproval, `SELECT `+apprCols+` FROM approvals WHERE org_id=$1 AND ($2='' OR status=$2) ORDER BY created_at DESC`, orgID, status)
}

// ---- reports ----

const repCols = `id, request_id, title, summary, sections, contributors, cost_usd, created_at`

func scanReport(r scanner) (domain.Report, error) {
	var p domain.Report
	var sec, con []byte
	err := r.Scan(&p.ID, &p.RequestID, &p.Title, &p.Summary, &sec, &con, &p.CostUSD, &p.CreatedAt)
	unmarshal(sec, &p.Sections)
	unmarshal(con, &p.Contributors)
	if p.Sections == nil {
		p.Sections = []domain.Section{}
	}
	p.Contributors = strs(p.Contributors)
	return p, err
}

func (s *Store) CreateReport(ctx context.Context, orgID string, r domain.Report) error {
	if r.Sections == nil {
		r.Sections = []domain.Section{}
	}
	return s.exec(ctx, orgID, `INSERT INTO reports (id, org_id, request_id, title, summary, sections, contributors, cost_usd, created_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		r.ID, orgID, r.RequestID, r.Title, r.Summary, jb(r.Sections), jb(strs(r.Contributors)), r.CostUSD, r.CreatedAt)
}

func (s *Store) GetReport(ctx context.Context, orgID, id string) (domain.Report, error) {
	r, err := one(ctx, s, orgID, scanReport, `SELECT `+repCols+` FROM reports WHERE org_id=$1 AND id=$2`, orgID, id)
	return r, mapErr(err)
}

func (s *Store) ListReports(ctx context.Context, orgID string) ([]domain.Report, error) {
	return many(ctx, s, orgID, scanReport, `SELECT `+repCols+` FROM reports WHERE org_id=$1 ORDER BY created_at DESC`, orgID)
}

// ---- memory ----

func (s *Store) SetMemory(ctx context.Context, orgID, agentID string, m domain.Memory) error {
	return s.exec(ctx, orgID, `INSERT INTO memories (org_id, agent_id, scope, key, value) VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (org_id, agent_id, scope, key) DO UPDATE SET value=EXCLUDED.value, updated_at=now()`,
		orgID, agentID, m.Scope, m.Key, m.Value)
}

func (s *Store) ListMemory(ctx context.Context, orgID, agentID string) ([]domain.Memory, error) {
	return many(ctx, s, orgID, func(r scanner) (domain.Memory, error) {
		var m domain.Memory
		err := r.Scan(&m.Key, &m.Value, &m.Scope)
		return m, err
	}, `SELECT key, value, scope FROM memories WHERE org_id=$1 AND agent_id=$2 ORDER BY scope, key`, orgID, agentID)
}

// ---- events / activity / audit ----

func (s *Store) SaveEvent(ctx context.Context, orgID string, e domain.Event) error {
	var agent *string
	if e.AgentID != "" {
		agent = &e.AgentID
	}
	return s.exec(ctx, orgID, `INSERT INTO events (id, org_id, type, agent_id, payload, ts) VALUES ($1,$2,$3,$4,$5,$6)`,
		e.ID, orgID, e.Type, agent, jb(e.Payload), e.TS)
}

func (s *Store) AddActivity(ctx context.Context, orgID string, a domain.ActivityItem) error {
	return s.exec(ctx, orgID, `INSERT INTO activity (id, org_id, ts, agent_id, kind, text) VALUES ($1,$2,$3,$4,$5,$6)`,
		a.ID, orgID, a.TS, a.AgentID, a.Kind, a.Text)
}

func (s *Store) ListActivity(ctx context.Context, orgID, agentID string, limit int) ([]domain.ActivityItem, error) {
	return many(ctx, s, orgID, func(r scanner) (domain.ActivityItem, error) {
		var a domain.ActivityItem
		err := r.Scan(&a.ID, &a.TS, &a.AgentID, &a.Kind, &a.Text)
		return a, err
	}, `SELECT id, ts, agent_id, kind, text FROM activity WHERE org_id=$1 AND ($2='' OR agent_id=$2) ORDER BY ts DESC, id LIMIT $3`, orgID, agentID, limit)
}

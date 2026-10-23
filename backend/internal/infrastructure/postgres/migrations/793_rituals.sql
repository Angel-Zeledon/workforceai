-- Rituals (B4, docs/architecture/rituals.md): the daily stand-up and the weekly retrospective.
--
-- rituals          one stand-up or retro: who took part and what each one said (entries as jsonb, built
--                  deterministically from tasks and approvals), the optional team summary and why it was skipped.
--                  A scheduled occurrence is claimed by inserting its period key (local date for a stand-up, ISO week
--                  for a retro): the partial unique index makes two instances unable to hold the same one twice.
-- ritual_proposals improvements a retro suggests to an agent about ITS OWN work. Only suggestions: a person accepts
--                  or dismisses them (compare-and-set on status). Accepting an instruction note goes through the agent
--                  work profile (versioned, audited); nothing here grants tools, permissions or autonomy.
-- ritual_settings  per organization: schedule of each ritual (both off by default) and whether a model may write
--                  the team summary.
--
-- Idempotent. Tenant isolation follows 202_rls_policies.sql. Rituals and proposals are history: the application
-- role cannot delete them.
CREATE TABLE IF NOT EXISTS rituals (
    org_id          TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    id              TEXT NOT NULL,
    kind            TEXT NOT NULL CHECK (kind IN ('standup', 'retro')),
    origin          TEXT NOT NULL CHECK (origin IN ('scheduled', 'manual')),
    period_key      TEXT NOT NULL DEFAULT '',
    status          TEXT NOT NULL CHECK (status IN ('held', 'skipped')),
    skip_reason     TEXT NOT NULL DEFAULT '',
    locale          TEXT NOT NULL DEFAULT 'es',
    window_from     TIMESTAMPTZ,
    window_to       TIMESTAMPTZ,
    entries         JSONB NOT NULL DEFAULT '[]'::jsonb,
    absent          JSONB NOT NULL DEFAULT '[]'::jsonb,
    summary         TEXT NOT NULL DEFAULT '',
    summary_source  TEXT NOT NULL DEFAULT '',
    created_by      TEXT NOT NULL DEFAULT '',
    held_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, id)
);
CREATE INDEX IF NOT EXISTS rituals_recent_idx ON rituals (org_id, kind, held_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS rituals_scheduled_once_idx ON rituals (org_id, kind, period_key) WHERE origin = 'scheduled';

CREATE TABLE IF NOT EXISTS ritual_proposals (
    org_id       TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    id           TEXT NOT NULL,
    ritual_id    TEXT NOT NULL,
    agent_id     TEXT NOT NULL,
    kind         TEXT NOT NULL CHECK (kind IN ('instruction_note', 'skill_suggestion')),
    signal       TEXT NOT NULL,
    title        TEXT NOT NULL,
    text         TEXT NOT NULL,
    evidence     JSONB NOT NULL DEFAULT '[]'::jsonb,
    status       TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'accepted', 'dismissed')),
    applied      BOOLEAN NOT NULL DEFAULT false,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at  TIMESTAMPTZ,
    resolved_by  TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (org_id, id)
);
CREATE INDEX IF NOT EXISTS ritual_proposals_recent_idx ON ritual_proposals (org_id, created_at DESC);
CREATE INDEX IF NOT EXISTS ritual_proposals_ritual_idx ON ritual_proposals (org_id, ritual_id);

CREATE TABLE IF NOT EXISTS ritual_settings (
    org_id      TEXT PRIMARY KEY REFERENCES organizations(id) ON DELETE CASCADE,
    settings    JSONB NOT NULL DEFAULT '{}'::jsonb,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

DO $$
DECLARE
    t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY['rituals', 'ritual_proposals', 'ritual_settings']
    LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE  ROW LEVEL SECURITY', t);
        EXECUTE format('DROP POLICY IF EXISTS tenant_isolation ON %I', t);
        EXECUTE format(
            'CREATE POLICY tenant_isolation ON %I
                 USING      (org_id = app_current_org())
                 WITH CHECK (org_id = app_current_org())', t);
    END LOOP;
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app_user') THEN
        GRANT SELECT, INSERT, UPDATE ON rituals, ritual_proposals TO app_user;
        REVOKE DELETE ON rituals, ritual_proposals FROM app_user;
        GRANT SELECT, INSERT, UPDATE, DELETE ON ritual_settings TO app_user;
    END IF;
END
$$;

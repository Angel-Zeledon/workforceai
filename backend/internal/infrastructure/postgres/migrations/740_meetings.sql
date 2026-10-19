-- Meetings (docs/architecture/meetings.md): rooms in the office, scheduled and live meetings, minutes.
--
-- meetings: one row per meeting. attendees (people and agents), agenda items and the calendar-sync
--   reference live in jsonb documents (they are always read and written with the meeting). Video is NOT
--   ours: join_url is a Jitsi / Meet / Zoom / Teams link (provider says which). status is the state
--   machine scheduled -> live -> ended, scheduled -> cancelled (enforced by the service).
-- meeting_minutes: the minutes of a meeting (summary, decisions, action items as jsonb), the drive
--   file they were saved to and the request that carries the tasks created from the action items.
-- Idempotent. Tenant isolation follows 202_rls_policies.sql.

CREATE TABLE IF NOT EXISTS meetings (
    org_id       TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    id           TEXT NOT NULL,
    title        TEXT NOT NULL CHECK (char_length(title) BETWEEN 1 AND 200),
    description  TEXT NOT NULL DEFAULT '' CHECK (char_length(description) <= 8000),
    room         TEXT NOT NULL DEFAULT '' CHECK (char_length(room) <= 80),
    status       TEXT NOT NULL CHECK (status IN ('scheduled','live','ended','cancelled')),
    starts_at    TIMESTAMPTZ NOT NULL,
    ends_at      TIMESTAMPTZ NOT NULL,
    organizer    TEXT NOT NULL DEFAULT '',
    provider     TEXT NOT NULL DEFAULT 'jitsi' CHECK (provider IN ('jitsi','meet','zoom','teams','custom','none')),
    join_url     TEXT NOT NULL DEFAULT '' CHECK (char_length(join_url) <= 2000),
    attendees    JSONB NOT NULL DEFAULT '[]'::jsonb,
    agenda       JSONB NOT NULL DEFAULT '[]'::jsonb,
    external     JSONB NOT NULL DEFAULT '{}'::jsonb,
    started_at   TIMESTAMPTZ,
    ended_at     TIMESTAMPTZ,
    cancelled_at TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, id),
    CHECK (ends_at > starts_at)
);
CREATE INDEX IF NOT EXISTS meetings_time_idx ON meetings (org_id, starts_at);
CREATE INDEX IF NOT EXISTS meetings_status_idx ON meetings (org_id, status);

CREATE TABLE IF NOT EXISTS meeting_minutes (
    org_id      TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    meeting_id  TEXT NOT NULL,
    doc         JSONB NOT NULL,
    source      TEXT NOT NULL DEFAULT 'notes' CHECK (source IN ('notes','transcript','upload')),
    file_id     TEXT NOT NULL DEFAULT '',
    file_path   TEXT NOT NULL DEFAULT '',
    request_id  TEXT NOT NULL DEFAULT '',
    created_by  TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, meeting_id),
    FOREIGN KEY (org_id, meeting_id) REFERENCES meetings (org_id, id) ON DELETE CASCADE
);

DO $$
DECLARE
    t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY['meetings', 'meeting_minutes']
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
        GRANT SELECT, INSERT, UPDATE, DELETE ON meetings, meeting_minutes TO app_user;
    END IF;
END
$$;

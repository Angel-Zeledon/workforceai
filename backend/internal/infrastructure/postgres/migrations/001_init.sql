-- Every table carries org_id (Phase 2 multi-tenant ready).

CREATE TABLE IF NOT EXISTS organizations (
    id         TEXT PRIMARY KEY,
    org_id     TEXT GENERATED ALWAYS AS (id) STORED,
    name       TEXT NOT NULL,
    slug       TEXT NOT NULL UNIQUE,
    budget_usd DOUBLE PRECISION NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS agents (
    org_id           TEXT NOT NULL REFERENCES organizations(id),
    id               TEXT NOT NULL,
    name             TEXT NOT NULL,
    role             TEXT NOT NULL,
    title            TEXT NOT NULL,
    description      TEXT NOT NULL DEFAULT '',
    persona          TEXT NOT NULL DEFAULT '',
    responsibilities JSONB NOT NULL DEFAULT '[]',
    state            TEXT NOT NULL DEFAULT 'idle',
    activity         TEXT NOT NULL DEFAULT '',
    current_task_id  TEXT,
    progress         INT NOT NULL DEFAULT 0,
    tools            JSONB NOT NULL DEFAULT '[]',
    permissions      JSONB NOT NULL DEFAULT '[]',
    autonomy         TEXT NOT NULL DEFAULT 'rules',
    position         INT NOT NULL DEFAULT 0,
    PRIMARY KEY (org_id, id)
);

CREATE TABLE IF NOT EXISTS requests (
    id         TEXT PRIMARY KEY,
    org_id     TEXT NOT NULL REFERENCES organizations(id),
    text       TEXT NOT NULL,
    status     TEXT NOT NULL,
    plan       JSONB NOT NULL DEFAULT '{}',
    report_id  TEXT,
    cost_usd   DOUBLE PRECISION NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS requests_org_idx ON requests (org_id, created_at DESC);

CREATE TABLE IF NOT EXISTS tasks (
    id             TEXT PRIMARY KEY,
    org_id         TEXT NOT NULL REFERENCES organizations(id),
    request_id     TEXT NOT NULL,
    workflow_id    TEXT,
    title          TEXT NOT NULL,
    description    TEXT NOT NULL DEFAULT '',
    agent_id       TEXT NOT NULL,
    status         TEXT NOT NULL,
    depends_on     JSONB NOT NULL DEFAULT '[]',
    parent_task_id TEXT,
    depth          INT NOT NULL DEFAULT 1,
    cost_usd       DOUBLE PRECISION NOT NULL DEFAULT 0,
    output         JSONB,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at     TIMESTAMPTZ,
    finished_at    TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS tasks_org_agent_idx ON tasks (org_id, agent_id);
CREATE INDEX IF NOT EXISTS tasks_org_request_idx ON tasks (org_id, request_id);

CREATE TABLE IF NOT EXISTS conversations (
    id              TEXT PRIMARY KEY,
    org_id          TEXT NOT NULL REFERENCES organizations(id),
    title           TEXT NOT NULL,
    participants    JSONB NOT NULL DEFAULT '[]',
    request_id      TEXT,
    last_message_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS messages (
    id              TEXT PRIMARY KEY,
    org_id          TEXT NOT NULL REFERENCES organizations(id),
    conversation_id TEXT NOT NULL,
    from_id         TEXT NOT NULL,
    to_id           TEXT NOT NULL,
    kind            TEXT NOT NULL,
    text            TEXT NOT NULL,
    task_id         TEXT,
    ts              TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS messages_conv_idx ON messages (org_id, conversation_id, ts);

CREATE TABLE IF NOT EXISTS approvals (
    id          TEXT PRIMARY KEY,
    org_id      TEXT NOT NULL REFERENCES organizations(id),
    task_id     TEXT NOT NULL,
    agent_id    TEXT NOT NULL,
    action      TEXT NOT NULL,
    title       TEXT NOT NULL,
    details     TEXT NOT NULL DEFAULT '',
    risk        TEXT NOT NULL,
    status      TEXT NOT NULL,
    note        TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS approvals_org_status_idx ON approvals (org_id, status);

CREATE TABLE IF NOT EXISTS reports (
    id           TEXT PRIMARY KEY,
    org_id       TEXT NOT NULL REFERENCES organizations(id),
    request_id   TEXT NOT NULL,
    title        TEXT NOT NULL,
    summary      TEXT NOT NULL DEFAULT '',
    sections     JSONB NOT NULL DEFAULT '[]',
    contributors JSONB NOT NULL DEFAULT '[]',
    cost_usd     DOUBLE PRECISION NOT NULL DEFAULT 0,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS memories (
    org_id     TEXT NOT NULL REFERENCES organizations(id),
    agent_id   TEXT NOT NULL,
    scope      TEXT NOT NULL,
    key        TEXT NOT NULL,
    value      TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, agent_id, scope, key)
);

CREATE TABLE IF NOT EXISTS events (
    id       TEXT PRIMARY KEY,
    org_id   TEXT NOT NULL REFERENCES organizations(id),
    type     TEXT NOT NULL,
    agent_id TEXT,
    payload  JSONB,
    ts       TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS events_org_ts_idx ON events (org_id, ts DESC);

CREATE TABLE IF NOT EXISTS audit_logs (
    id        TEXT PRIMARY KEY,
    org_id    TEXT NOT NULL REFERENCES organizations(id),
    actor     TEXT NOT NULL,
    action    TEXT NOT NULL,
    entity    TEXT NOT NULL DEFAULT '',
    entity_id TEXT NOT NULL DEFAULT '',
    details   JSONB,
    ts        TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS audit_org_ts_idx ON audit_logs (org_id, ts DESC);

CREATE TABLE IF NOT EXISTS activity (
    id       TEXT PRIMARY KEY,
    org_id   TEXT NOT NULL REFERENCES organizations(id),
    ts       TIMESTAMPTZ NOT NULL DEFAULT now(),
    agent_id TEXT,
    kind     TEXT NOT NULL,
    text     TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS activity_org_ts_idx ON activity (org_id, ts DESC);

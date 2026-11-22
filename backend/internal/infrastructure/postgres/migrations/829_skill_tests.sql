-- F8 "Probar una habilidad con un caso" (docs/architecture/skills.md): test cases saved against a skill (not against a
-- version) and the record of every test run, which names the version that was tested.
--   skill_test_cases  a request in plain words plus, optionally, what the person expects to see. Editable and
--                     deletable (the application caps them per skill and bounds their size).
--   skill_test_runs   append-only: one row per run of a case by an agent with one version of the skill loaded. The
--                     answer is stored bounded and redacted; `blocked` lists the tools the agent asked for that were
--                     NOT run (tool.action only, never arguments: a test has no effects). `pair_id` joins the two runs
--                     of a side-by-side comparison (tested version vs the active one).
-- Additive and idempotent. Tenant isolation follows 202_rls_policies.sql.

CREATE TABLE IF NOT EXISTS skill_test_cases (
    org_id     TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    id         TEXT NOT NULL,
    skill_id   TEXT NOT NULL,
    title      TEXT NOT NULL DEFAULT '' CHECK (char_length(title) <= 120),
    prompt     TEXT NOT NULL CHECK (char_length(prompt) BETWEEN 1 AND 4000),
    expect     TEXT NOT NULL DEFAULT '' CHECK (char_length(expect) <= 2000),
    created_by TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, id),
    FOREIGN KEY (org_id, skill_id) REFERENCES skills (org_id, id)
);
CREATE INDEX IF NOT EXISTS skill_test_cases_skill_idx ON skill_test_cases (org_id, skill_id, created_at);

CREATE TABLE IF NOT EXISTS skill_test_runs (
    org_id       TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    id           TEXT NOT NULL,
    skill_id     TEXT NOT NULL,
    case_id      TEXT NOT NULL,
    case_title   TEXT NOT NULL DEFAULT '',
    pair_id      TEXT NOT NULL DEFAULT '',
    version      INT  NOT NULL,
    active       BOOLEAN NOT NULL DEFAULT false, -- the version was the active one when it ran
    agent_id     TEXT NOT NULL,
    status       TEXT NOT NULL CHECK (status IN ('done', 'failed')),
    fail_code    TEXT NOT NULL DEFAULT '',
    output       TEXT NOT NULL DEFAULT '' CHECK (char_length(output) <= 12000),
    used_skill   BOOLEAN NOT NULL DEFAULT false,
    blocked      JSONB NOT NULL DEFAULT '[]',
    estimate_usd NUMERIC(12,6) NOT NULL DEFAULT 0,
    cost_usd     NUMERIC(12,6) NOT NULL DEFAULT 0,
    input_tokens INT NOT NULL DEFAULT 0,
    output_tokens INT NOT NULL DEFAULT 0,
    duration_ms  INT NOT NULL DEFAULT 0,
    model        TEXT NOT NULL DEFAULT '',
    created_by   TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, id),
    FOREIGN KEY (org_id, skill_id) REFERENCES skills (org_id, id)
);
CREATE INDEX IF NOT EXISTS skill_test_runs_skill_idx ON skill_test_runs (org_id, skill_id, created_at DESC);
CREATE INDEX IF NOT EXISTS skill_test_runs_case_idx ON skill_test_runs (org_id, case_id, created_at DESC);

DO $$
DECLARE
    t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY['skill_test_cases', 'skill_test_runs']
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
        GRANT SELECT, INSERT, UPDATE, DELETE ON skill_test_cases TO app_user;
        GRANT SELECT, INSERT ON skill_test_runs TO app_user;
        REVOKE UPDATE, DELETE ON skill_test_runs FROM app_user;
    END IF;
END
$$;

-- Customer district (B12, docs/architecture/customer-district.md): the customers (clients) of an organization,
-- the per-customer agent clones ("Contador · Ferretería López") and what is scoped to a customer.
--
--   customers             one row per customer; archived_at hides it from new work (never hard-deleted by the
--                         application, so the customer tags already stored keep isolating its data)
--   customer_assignments  a clone: the same agent (role, tools, autonomy, pause, spend caps) working for one
--                         customer under a display name; one per (customer, agent)
--   customer_links        what belongs to a customer: a request (stored BEFORE the request is created, so no task
--                         runs unscoped) or a drive folder a person linked; one customer per object
--   memory_docs.customer_id  the customer the remembered work was done for: a customer-scoped task only recalls
--                         that customer's documents (plus the notes a person wrote without one)
--
-- Projects carry their customer in their JSONB document (projects.data->>'customer_id'); artifacts already have
-- customer_id (280). Idempotent. Tenant isolation follows 202_rls_policies.sql.

CREATE TABLE IF NOT EXISTS customers (
    org_id      TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    id          TEXT NOT NULL,
    name        TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 120),
    tax_id      TEXT NOT NULL DEFAULT '' CHECK (char_length(tax_id) <= 40),
    color       TEXT NOT NULL DEFAULT '' CHECK (color = '' OR color ~ '^#[0-9a-fA-F]{6}$'),
    notes       TEXT NOT NULL DEFAULT '' CHECK (char_length(notes) <= 2000),
    folder_id   TEXT NOT NULL DEFAULT '',
    created_by  TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    archived_at TIMESTAMPTZ,
    PRIMARY KEY (org_id, id)
);
-- Two active customers of one organization cannot share a name (case-insensitive).
CREATE UNIQUE INDEX IF NOT EXISTS customers_active_name_idx ON customers (org_id, lower(name)) WHERE archived_at IS NULL;

CREATE TABLE IF NOT EXISTS customer_assignments (
    org_id       TEXT NOT NULL,
    id           TEXT NOT NULL,
    customer_id  TEXT NOT NULL,
    agent_id     TEXT NOT NULL,
    display_name TEXT NOT NULL CHECK (char_length(display_name) BETWEEN 1 AND 160),
    created_by   TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, id),
    UNIQUE (org_id, customer_id, agent_id),
    FOREIGN KEY (org_id, customer_id) REFERENCES customers (org_id, id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS customer_links (
    org_id      TEXT NOT NULL,
    kind        TEXT NOT NULL CHECK (kind IN ('request', 'drive_folder')),
    ref_id      TEXT NOT NULL,
    customer_id TEXT NOT NULL,
    created_by  TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, kind, ref_id),
    FOREIGN KEY (org_id, customer_id) REFERENCES customers (org_id, id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS customer_links_customer_idx ON customer_links (org_id, customer_id, kind, created_at DESC);

ALTER TABLE memory_docs ADD COLUMN IF NOT EXISTS customer_id TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS memory_docs_customer_idx ON memory_docs (org_id, customer_id) WHERE customer_id <> '';

DO $$
BEGIN
    ALTER TABLE customers ENABLE ROW LEVEL SECURITY;
    ALTER TABLE customers FORCE ROW LEVEL SECURITY;
    DROP POLICY IF EXISTS tenant_isolation ON customers;
    CREATE POLICY tenant_isolation ON customers USING (org_id = app_current_org()) WITH CHECK (org_id = app_current_org());
    ALTER TABLE customer_assignments ENABLE ROW LEVEL SECURITY;
    ALTER TABLE customer_assignments FORCE ROW LEVEL SECURITY;
    DROP POLICY IF EXISTS tenant_isolation ON customer_assignments;
    CREATE POLICY tenant_isolation ON customer_assignments USING (org_id = app_current_org()) WITH CHECK (org_id = app_current_org());
    ALTER TABLE customer_links ENABLE ROW LEVEL SECURITY;
    ALTER TABLE customer_links FORCE ROW LEVEL SECURITY;
    DROP POLICY IF EXISTS tenant_isolation ON customer_links;
    CREATE POLICY tenant_isolation ON customer_links USING (org_id = app_current_org()) WITH CHECK (org_id = app_current_org());
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app_user') THEN
        -- Customers are archived, never deleted, by the application (their tags keep isolating stored data).
        GRANT SELECT, INSERT, UPDATE ON customers TO app_user;
        REVOKE DELETE ON customers FROM app_user;
        GRANT SELECT, INSERT, DELETE ON customer_assignments TO app_user;
        GRANT SELECT, INSERT, DELETE ON customer_links TO app_user;
    END IF;
END
$$;

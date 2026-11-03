-- E1 "Alta y agentes por rol" (docs/architecture/equipos-oficinas.md §6, plan docs/superpowers/plans/2026-11-03-e1-alta-agentes-por-rol.md).
--
-- org_setup: who the organization is for (solo / team / company), written only by the new sign-up. An organization
-- with a row here starts with the agents of its people's professions instead of the 7 fixed ones.
-- member_profiles: the profession and team (department) of each person in each organization, from the sign-up, an
-- invitation, the verified-domain join or the person's own welcome; provisioned_at = their agents and team are set up.
-- signup_intents: what a person who registers to join their company by its verified domain chose, kept until the
-- verification link is opened (the address is proven only then).
-- invitations gain the team, the profession and the "responsible of the team" mark.
--
-- Like memberships (201_auth_tables.sql), these tables are NOT under RLS: they are read before a tenant is known
-- (verification, accepting an invitation) and only the auth module touches them. Additive and idempotent.

CREATE TABLE IF NOT EXISTS org_setup (
    org_id        TEXT PRIMARY KEY REFERENCES organizations(id) ON DELETE CASCADE,
    audience      TEXT NOT NULL CHECK (audience IN ('solo', 'team', 'company')),
    locale        TEXT NOT NULL DEFAULT 'es',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    setup_done_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS member_profiles (
    org_id         TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    user_id        TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    profession     TEXT NOT NULL DEFAULT '' CHECK (char_length(profession) <= 40),
    department_id  TEXT NOT NULL DEFAULT '' CHECK (char_length(department_id) <= 80),
    team_name      TEXT NOT NULL DEFAULT '' CHECK (char_length(team_name) <= 120),
    lead           BOOLEAN NOT NULL DEFAULT false,
    via            TEXT NOT NULL DEFAULT '' CHECK (via IN ('', 'signup', 'invitation', 'domain', 'sso', 'self')),
    invited_by     TEXT NOT NULL DEFAULT '',
    provisioned_at TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, user_id)
);
CREATE INDEX IF NOT EXISTS member_profiles_user_idx ON member_profiles (user_id);

CREATE TABLE IF NOT EXISTS signup_intents (
    user_id    TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    audience   TEXT NOT NULL DEFAULT 'solo' CHECK (audience IN ('solo', 'team', 'company')),
    profession TEXT NOT NULL DEFAULT '' CHECK (char_length(profession) <= 40),
    org_name   TEXT NOT NULL DEFAULT '' CHECK (char_length(org_name) <= 200),
    team_name  TEXT NOT NULL DEFAULT '' CHECK (char_length(team_name) <= 120),
    locale     TEXT NOT NULL DEFAULT 'es',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE invitations ADD COLUMN IF NOT EXISTS department_id TEXT NOT NULL DEFAULT '';
ALTER TABLE invitations ADD COLUMN IF NOT EXISTS profession TEXT NOT NULL DEFAULT '';
ALTER TABLE invitations ADD COLUMN IF NOT EXISTS lead BOOLEAN NOT NULL DEFAULT false;

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app_user') THEN
        GRANT SELECT, INSERT, UPDATE ON org_setup, member_profiles TO app_user;
        GRANT SELECT, INSERT, UPDATE, DELETE ON signup_intents TO app_user;
    END IF;
END
$$;

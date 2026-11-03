-- Folder access per agent (docs/superpowers/specs/2026-11-03-agentes-ubican-archivos-y-accesos-design.md, part 3):
-- a rule of drive_grants now carries a level, Ninguno / Leer / Editar (none | read | edit), inherited downwards; the
-- most specific rule wins over the defaults (desk and projects Edit, /Shared Read). Existing rows are 'read', so every
-- agent keeps what it sees today. Additive and idempotent.

ALTER TABLE drive_grants DROP CONSTRAINT IF EXISTS drive_grants_mode_check;
ALTER TABLE drive_grants ADD CONSTRAINT drive_grants_mode_check CHECK (mode IN ('none', 'read', 'edit'));
-- one active rule per agent and folder (SetAgentAccess revokes the previous one before adding)
CREATE INDEX IF NOT EXISTS drive_grants_agent_path_idx ON drive_grants (org_id, agent_id, lower(path)) WHERE revoked_at IS NULL;
-- fs.recent: files touched recently in an organization, newest first
CREATE INDEX IF NOT EXISTS drive_files_recent_idx ON drive_files (org_id, updated_at DESC) WHERE deleted_at IS NULL;

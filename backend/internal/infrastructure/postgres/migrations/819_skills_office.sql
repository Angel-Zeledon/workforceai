-- Skills of a department (phase D3, sealed departments): a skill may belong to one department (office). "" = the
-- whole company (every skill that existed before). A skill of a private office is only listed to its members, and
-- only the agents of that office can have it. office_id is the office id, which is the id of its department in the
-- org chart; no foreign key because a closed office keeps its skills (only administrators then find them).
--
-- Additive and idempotent. The skills table already has org RLS (510_skills.sql). Down:
-- backend/migrations/down/819_skills_office_down.sql.

ALTER TABLE skills ADD COLUMN IF NOT EXISTS office_id TEXT NOT NULL DEFAULT '';

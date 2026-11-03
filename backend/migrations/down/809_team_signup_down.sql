-- Reverts 809_team_signup.sql.
ALTER TABLE invitations DROP COLUMN IF EXISTS lead;
ALTER TABLE invitations DROP COLUMN IF EXISTS profession;
ALTER TABLE invitations DROP COLUMN IF EXISTS department_id;
DROP TABLE IF EXISTS signup_intents;
DROP TABLE IF EXISTS member_profiles;
DROP TABLE IF EXISTS org_setup;

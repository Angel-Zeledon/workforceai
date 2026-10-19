-- Rollback of 703_scim.sql. SCIM tokens stop working and the provisioning map is dropped; the user accounts and
-- memberships SCIM created stay.
DROP TABLE IF EXISTS scim_users;
DROP TABLE IF EXISTS scim_tokens;

-- Rollback of 300_invitations.sql. Drops every invitation (pending links stop working).
DROP TABLE IF EXISTS invitations;

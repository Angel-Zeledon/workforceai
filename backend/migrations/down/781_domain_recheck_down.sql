-- Rollback of 781_domain_recheck.sql.
ALTER TABLE org_domains DROP COLUMN IF EXISTS lapsed_since;

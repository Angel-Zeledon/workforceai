-- Rollback of 798_client_portal.sql. Every client portal link stops working at once (the tokens cannot
-- be recovered) and the firm's branding (name, color, logo) is lost. Projects and deliverables are not
-- affected.
DROP TABLE IF EXISTS portal_links;
DROP TABLE IF EXISTS portal_branding;

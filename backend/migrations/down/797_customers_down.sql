-- Rollback of 797_customers.sql. Customers, their clones and links are dropped and memory documents lose their
-- customer tag: every request becomes unscoped again (the behaviour before B12). Project documents keep a
-- harmless "customer_id" key in their JSONB that the older code ignores.
DROP INDEX IF EXISTS memory_docs_customer_idx;
ALTER TABLE memory_docs DROP COLUMN IF EXISTS customer_id;
DROP TABLE IF EXISTS customer_links;
DROP TABLE IF EXISTS customer_assignments;
DROP TABLE IF EXISTS customers;

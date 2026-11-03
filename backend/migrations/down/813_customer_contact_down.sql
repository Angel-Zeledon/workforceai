-- Reverts 813_customer_contact.sql.
DROP INDEX IF EXISTS customers_email_idx;
ALTER TABLE customers DROP COLUMN IF EXISTS phone;
ALTER TABLE customers DROP COLUMN IF EXISTS email;
ALTER TABLE customers DROP COLUMN IF EXISTS contact_name;

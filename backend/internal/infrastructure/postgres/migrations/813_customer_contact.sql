-- Projects for many customers (docs/superpowers/specs/2026-11-03-proyectos-por-cliente-y-ayuda-design.md, part 1).
-- A customer is a person or a company (its name); these optional contact fields say who to talk to. When a list is
-- imported, a row is "the same customer" when its e-mail matches (then the tax id, then the normalized name).
-- Additive and idempotent: existing customers keep empty values.
ALTER TABLE customers ADD COLUMN IF NOT EXISTS contact_name TEXT NOT NULL DEFAULT '' CHECK (char_length(contact_name) <= 120);
ALTER TABLE customers ADD COLUMN IF NOT EXISTS email        TEXT NOT NULL DEFAULT '' CHECK (char_length(email) <= 254);
ALTER TABLE customers ADD COLUMN IF NOT EXISTS phone        TEXT NOT NULL DEFAULT '' CHECK (char_length(phone) <= 40);
CREATE INDEX IF NOT EXISTS customers_email_idx ON customers (org_id, email) WHERE email <> '';

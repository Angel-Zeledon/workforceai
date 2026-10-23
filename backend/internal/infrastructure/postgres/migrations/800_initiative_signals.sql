-- Metric signals of proactive employees (B2, docs/architecture/initiative.md "Señales de métricas").
--
-- initiative_settings.signals holds, per organization, which metric signals are on and their thresholds
-- ({"metric_change": {...}, "spend_pace": {...}, "approval_aging": {...}, "quality_drop": {...}}); '{}' means the
-- defaults (application.DefaultSignalSettings). initiatives.params keeps the values behind a suggestion's text
-- (amounts, names, periods) so the UI can render it in the viewer's language.
--
-- Additive and idempotent: both columns have defaults, existing rows keep working. Both tables already have
-- row level security by organization (787_initiatives.sql); new columns are covered by the same policies.
ALTER TABLE initiative_settings ADD COLUMN IF NOT EXISTS signals JSONB NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE initiatives ADD COLUMN IF NOT EXISTS params JSONB NOT NULL DEFAULT '{}'::jsonb;

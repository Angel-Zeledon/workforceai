-- Per-request parallelism override (W1) persisted with the run meta, so a request
-- resumed by restart recovery keeps its project's max_parallel. 0 = MAX_PARALLEL.
-- Idempotent.
ALTER TABLE request_runs ADD COLUMN IF NOT EXISTS max_parallel INTEGER NOT NULL DEFAULT 0;

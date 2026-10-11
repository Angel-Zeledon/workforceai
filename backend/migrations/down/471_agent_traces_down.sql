-- Rollback of 471_agent_traces.sql (stored traces and the trace policy are dropped).
DROP TABLE IF EXISTS agent_traces;
DROP TABLE IF EXISTS trace_settings;

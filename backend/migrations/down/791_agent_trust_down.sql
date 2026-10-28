-- Rollback of 791_agent_trust.sql. The ladder's records and the proposal history are lost; the agents keep the
-- autonomy level they have (agents.autonomy is not touched), and the next evaluation starts tracking again.
DROP TABLE IF EXISTS trust_proposals;
DROP TABLE IF EXISTS agent_trust;

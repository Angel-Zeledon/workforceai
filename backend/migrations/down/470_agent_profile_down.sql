-- Rollback of 470_agent_profile.sql (profiles and their history are dropped).
DROP TABLE IF EXISTS agent_profile_versions;
DROP TABLE IF EXISTS agent_profiles;

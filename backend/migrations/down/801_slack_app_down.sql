-- Rollback of 801_slack_app.sql. The organizations' Slack apps (sealed secrets), user links, pending link codes,
-- interaction dedupe rows and posted-message references are lost; the channel stops answering (404) until the
-- migration is applied again and an admin pastes the credentials again.
DROP TABLE IF EXISTS slack_approval_messages;
DROP TABLE IF EXISTS slack_interactions;
DROP TABLE IF EXISTS slack_link_codes;
DROP TABLE IF EXISTS slack_user_links;
DROP TABLE IF EXISTS slack_app_settings;

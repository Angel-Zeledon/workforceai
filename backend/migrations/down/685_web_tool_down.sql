-- Rollback of 685_web_tool.sql (the web setting and its call ledger are dropped; with no setting the web tool is off).
DROP TABLE IF EXISTS web_calls;
DROP TABLE IF EXISTS web_settings;

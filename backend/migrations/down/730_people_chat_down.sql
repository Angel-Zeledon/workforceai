-- Rollback of 730_people_chat.sql. Deletes the people channels, their members and messages (agent chats are untouched).
DROP TABLE IF EXISTS pc_messages;
DROP TABLE IF EXISTS pc_members;
DROP TABLE IF EXISTS pc_channels;

-- Down of 835_meeting_office.sql: a meeting forgets the office it was created from (its tasks start requests from the base).
ALTER TABLE meetings DROP COLUMN IF EXISTS office_id;

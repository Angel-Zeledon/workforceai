-- Down of 834_schedule_office.sql: a schedule forgets the office it was created from (it starts requests from the base).
ALTER TABLE schedules DROP COLUMN IF EXISTS office_id;

-- Rollback of 784_notify_outbox.sql. Queued notifications that were not delivered yet are lost.
DROP TABLE IF EXISTS notify_outbox;

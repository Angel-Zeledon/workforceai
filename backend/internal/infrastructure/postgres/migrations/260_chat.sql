-- Chat layer: conversations with routing (office channel, 1:1 chats) and the
-- reason behind every task assignment.
-- Spec: docs/architecture/chat-routing.md.
--
-- messages gains the chat metadata: turn_id groups the replies to one user
-- message, reply_to is the message they answer and request_id the request a
-- task turn created. Rows written before this migration keep the defaults.
-- tasks gains assigned_reason (why this agent got the task; '' when unknown).
--
-- Chat conversations have deterministic ids ('office', 'agent:<id>') that repeat
-- in every organization, so conversations become unique per (org_id, id)
-- instead of per id. Nothing references conversations.id with a foreign key.
--
-- Idempotent. Tenant isolation is unchanged (202_rls_policies.sql).

ALTER TABLE messages ADD COLUMN IF NOT EXISTS turn_id    TEXT NOT NULL DEFAULT '';
ALTER TABLE messages ADD COLUMN IF NOT EXISTS reply_to   TEXT;
ALTER TABLE messages ADD COLUMN IF NOT EXISTS request_id TEXT;
ALTER TABLE tasks    ADD COLUMN IF NOT EXISTS assigned_reason TEXT NOT NULL DEFAULT '';

DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'conversations'::regclass AND contype = 'p' AND array_length(conkey, 1) = 1
    ) THEN
        ALTER TABLE conversations DROP CONSTRAINT conversations_pkey;
        ALTER TABLE conversations ADD PRIMARY KEY (org_id, id);
    END IF;
END
$$;

CREATE INDEX IF NOT EXISTS messages_turn_idx ON messages (org_id, conversation_id, turn_id) WHERE turn_id <> '';

-- v0.3.5: chat/compare messages sent as synthetic prompts of an exact token
-- length (0 = typed by the user). Used to show them collapsed.
ALTER TABLE chat_messages ADD COLUMN synthetic_tokens INTEGER NOT NULL DEFAULT 0;

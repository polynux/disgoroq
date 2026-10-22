-- One-time migration from bot_reply_votes to message_votes, run by hand with
-- sqlite3 after the bot restarted with the new schema (which creates
-- message_votes). Not re-runnable: the DROP fails once the table is gone.
--
-- Replies voted on since the restart were adopted without provider/model;
-- the upsert fills those from the legacy row and keeps the newer counts.
-- Legacy rows get the bot's author ID/name from any newer bot-reply row.
BEGIN;

INSERT INTO message_votes (
    message_id, guild_id, channel_id, author_id, author_name, is_bot_reply,
    content, reply_to_message_id, reply_to_content, provider, model,
    good_count, bad_count, message_created_at, created_at, updated_at
)
SELECT
    message_id, guild_id, channel_id,
    COALESCE((SELECT author_id FROM message_votes WHERE is_bot_reply = 1 AND author_id != '' LIMIT 1), ''),
    COALESCE((SELECT author_name FROM message_votes WHERE is_bot_reply = 1 AND author_id != '' LIMIT 1), ''),
    1,
    content, reply_to_message_id, reply_to_content, provider, model,
    good_count, bad_count,
    ((CAST(message_id AS INTEGER) >> 22) + 1420070400000) / 1000,
    created_at, updated_at
FROM bot_reply_votes WHERE true
ON CONFLICT(message_id) DO UPDATE SET
    provider = excluded.provider, model = excluded.model,
    reply_to_message_id = excluded.reply_to_message_id,
    reply_to_content = excluded.reply_to_content, created_at = excluded.created_at
WHERE message_votes.provider = '';

DROP TABLE bot_reply_votes;

COMMIT;

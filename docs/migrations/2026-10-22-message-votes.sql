-- One-time migration after the new message_votes table and indexes exist.
INSERT INTO message_votes (
    message_id, guild_id, channel_id, author_id, author_name, is_bot_reply,
    content, reply_to_message_id, reply_to_content, provider, model,
    good_count, bad_count, message_created_at, created_at, updated_at
)
SELECT
    message_id, guild_id, channel_id, '', '', 1,
    content, reply_to_message_id, reply_to_content, provider, model,
    good_count, bad_count,
    ((CAST(message_id AS INTEGER) >> 22) + 1420070400000) / 1000,
    created_at, updated_at
FROM bot_reply_votes;

DROP TABLE bot_reply_votes;

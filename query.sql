-- name: GetGuildSettings :many
SELECT * FROM guild_settings WHERE id = ?;

-- name: GetGuildSetting :one
SELECT value FROM guild_settings WHERE guild_id = ? AND name = ?;

-- name: GetAllGuilds :many
SELECT DISTINCT guild_id FROM guild_settings;

-- name: SetGuildSetting :exec
INSERT OR REPLACE INTO guild_settings (guild_id, name, value) VALUES (?, ?, ?);

-- name: DeleteGuildSetting :exec
DELETE FROM guild_settings WHERE guild_id = ? AND name = ?;

-- name: InsertEvent :exec
INSERT INTO bot_events (timestamp, event_type, guild_id, channel_id, message_id, user_id, details, duration_ms, error)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetEvents :many
SELECT timestamp, event_type, guild_id, channel_id, message_id, user_id, details, duration_ms, error
FROM bot_events
WHERE ? = 0 OR guild_id = ?
ORDER BY timestamp DESC
LIMIT ?;

-- name: GetEventsByType :many
SELECT timestamp, event_type, guild_id, channel_id, message_id, user_id, details, duration_ms, error
FROM bot_events
WHERE event_type = ?
ORDER BY timestamp DESC
LIMIT ?;

-- name: GetEventsByMessage :many
SELECT timestamp, event_type, guild_id, channel_id, message_id, user_id, details, duration_ms, error
FROM bot_events
WHERE message_id = ?
ORDER BY timestamp ASC;

-- name: DeleteOldEvents :exec
DELETE FROM bot_events
WHERE timestamp < datetime('now', '-' || ? || ' days');

-- Message Buffer Queries

-- name: InsertMessageBuffer :exec
INSERT INTO message_buffer (
    guild_id, channel_id, message_id, user_id, author_nick, 
    content, has_image, image_description, timestamp, processed
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetUnprocessedMessageCount :one
SELECT COUNT(*) as count
FROM message_buffer 
WHERE guild_id = ? AND user_id = ? AND processed = 0;

-- name: GetUnprocessedMessages :many
SELECT id, guild_id, channel_id, message_id, user_id, author_nick, 
       content, has_image, image_description, timestamp, processed
FROM message_buffer 
WHERE guild_id = ? AND user_id = ? AND processed = 0
ORDER BY timestamp ASC
LIMIT ?;

-- name: MarkMessagesAsProcessed :exec
UPDATE message_buffer 
SET processed = 1 
WHERE id IN (
    SELECT id FROM message_buffer mb 
    WHERE mb.guild_id = ? AND mb.user_id = ? AND mb.processed = 0 
    ORDER BY mb.timestamp ASC 
    LIMIT ?
);

-- name: DeleteProcessedMessages :exec
DELETE FROM message_buffer 
WHERE processed = 1 AND timestamp < ?;

-- Summary Queries

-- name: InsertSummary :exec
INSERT INTO conversation_summaries (
    guild_id, user_id, summary_text, message_count, 
    start_message_id, end_message_id, created_at, updated_at, embedding
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetLatestSummaryForUser :one
SELECT id, guild_id, user_id, summary_text, message_count, 
       start_message_id, end_message_id, created_at, updated_at, embedding
FROM conversation_summaries 
WHERE guild_id = ? AND user_id = ? 
ORDER BY updated_at DESC 
LIMIT 1;

-- name: GetLatestSummariesForUsers :many
SELECT cs.id, cs.guild_id, cs.user_id, cs.summary_text, cs.message_count, 
       cs.start_message_id, cs.end_message_id, cs.created_at, cs.updated_at, cs.embedding
FROM conversation_summaries cs
INNER JOIN (
    SELECT user_id, MAX(updated_at) as max_updated_at
    FROM conversation_summaries 
    WHERE conversation_summaries.guild_id = ? AND conversation_summaries.user_id IN (/*SLICE:user_ids*/?)
    GROUP BY user_id
) latest ON cs.user_id = latest.user_id AND cs.updated_at = latest.max_updated_at
WHERE cs.guild_id = ?;

-- name: GetSummariesByGuild :many
SELECT id, guild_id, user_id, summary_text, message_count, 
       start_message_id, end_message_id, created_at, updated_at, embedding
FROM conversation_summaries 
WHERE guild_id = ? 
ORDER BY updated_at DESC 
LIMIT ? OFFSET ?;

-- name: UpdateSummary :exec
UPDATE conversation_summaries 
SET summary_text = ?, message_count = ?, end_message_id = ?, updated_at = ?, embedding = ?
WHERE id = ?;

-- name: VectorSearchSummaries :many
SELECT id, guild_id, user_id, summary_text, message_count, 
       start_message_id, end_message_id, created_at, updated_at,
       vector_distance_cos(embedding, vector32(?)) as distance
FROM conversation_summaries
WHERE guild_id = ? AND embedding IS NOT NULL
ORDER BY distance ASC
LIMIT ?;

-- name: VectorSearchSummariesByUser :many
SELECT id, guild_id, user_id, summary_text, message_count, 
       start_message_id, end_message_id, created_at, updated_at,
       vector_distance_cos(embedding, vector32(?)) as distance
FROM conversation_summaries
WHERE guild_id = ? AND user_id = ? AND embedding IS NOT NULL
ORDER BY distance ASC
LIMIT ?;

-- name: GetSummaryStatsByGuild :one
SELECT 
    COUNT(*) as total_summaries,
    COUNT(DISTINCT user_id) as unique_users,
    SUM(message_count) as total_messages_summarized,
    MAX(updated_at) as last_summary_update
FROM conversation_summaries 
WHERE guild_id = ?;

-- name: DeleteSummariesForUser :exec
DELETE FROM conversation_summaries 
WHERE guild_id = ? AND user_id = ?;

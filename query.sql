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

-- name: GetGuildSettings :many
SELECT * FROM guild_settings WHERE id = ?;

-- name: GetGuildSetting :one
SELECT value FROM guild_settings WHERE guild_id = ? AND name = ?;

-- name: GetAllGuilds :many
SELECT DISTINCT guild_id FROM guild_settings;

-- name: SetGuildSetting :exec
INSERT OR REPLACE INTO guild_settings (guild_id, name, value) VALUES (?, ?, ?);

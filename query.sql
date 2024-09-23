-- name: GetGuildSettings :many
SELECT * FROM guild_settings WHERE id = ?;

-- name: GetGuildSettingsByName :one
SELECT * FROM guild_settings WHERE guild_id = ? AND name = ?;

-- name: GetAllGuilds :many
SELECT DISTINCT guild_id FROM guild_settings;

-- name: SetGuildSetting :exec
INSERT OR REPLACE INTO guild_settings (guild_id, name, value) VALUES (?, ?, ?);

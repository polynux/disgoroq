// Code generated by sqlc. DO NOT EDIT.
// versions:
//   sqlc v1.25.0
// source: query.sql

package db

import (
	"context"
)

const deleteGuildSetting = `-- name: DeleteGuildSetting :exec
DELETE FROM guild_settings WHERE guild_id = ? AND name = ?
`

type DeleteGuildSettingParams struct {
	GuildID string
	Name    string
}

func (q *Queries) DeleteGuildSetting(ctx context.Context, arg DeleteGuildSettingParams) error {
	_, err := q.db.ExecContext(ctx, deleteGuildSetting, arg.GuildID, arg.Name)
	return err
}

const getAllGuilds = `-- name: GetAllGuilds :many
SELECT DISTINCT guild_id FROM guild_settings
`

func (q *Queries) GetAllGuilds(ctx context.Context) ([]string, error) {
	rows, err := q.db.QueryContext(ctx, getAllGuilds)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []string
	for rows.Next() {
		var guild_id string
		if err := rows.Scan(&guild_id); err != nil {
			return nil, err
		}
		items = append(items, guild_id)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

const getGuildSetting = `-- name: GetGuildSetting :one
SELECT value FROM guild_settings WHERE guild_id = ? AND name = ?
`

type GetGuildSettingParams struct {
	GuildID string
	Name    string
}

func (q *Queries) GetGuildSetting(ctx context.Context, arg GetGuildSettingParams) (string, error) {
	row := q.db.QueryRowContext(ctx, getGuildSetting, arg.GuildID, arg.Name)
	var value string
	err := row.Scan(&value)
	return value, err
}

const getGuildSettings = `-- name: GetGuildSettings :many
SELECT id, guild_id, name, value FROM guild_settings WHERE id = ?
`

func (q *Queries) GetGuildSettings(ctx context.Context, id int64) ([]GuildSetting, error) {
	rows, err := q.db.QueryContext(ctx, getGuildSettings, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []GuildSetting
	for rows.Next() {
		var i GuildSetting
		if err := rows.Scan(
			&i.ID,
			&i.GuildID,
			&i.Name,
			&i.Value,
		); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

const setGuildSetting = `-- name: SetGuildSetting :exec
INSERT OR REPLACE INTO guild_settings (guild_id, name, value) VALUES (?, ?, ?)
`

type SetGuildSettingParams struct {
	GuildID string
	Name    string
	Value   string
}

func (q *Queries) SetGuildSetting(ctx context.Context, arg SetGuildSettingParams) error {
	_, err := q.db.ExecContext(ctx, setGuildSetting, arg.GuildID, arg.Name, arg.Value)
	return err
}

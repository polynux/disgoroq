CREATE TABLE IF NOT EXISTS guild_settings (
    id INTEGER PRIMARY KEY,
    guild_id TEXT NOT NULL,
    name TEXT NOT NULL,
    value TEXT NOT NULL,
    UNIQUE(guild_id, name)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_guild_settings_guild_id_name
ON guild_settings(guild_id, name);

CREATE TABLE IF NOT EXISTS bot_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    timestamp INTEGER NOT NULL,
    event_type TEXT NOT NULL,
    guild_id TEXT,
    channel_id TEXT,
    message_id TEXT,
    user_id TEXT,
    details TEXT,
    duration_ms INTEGER,
    error TEXT
);

CREATE INDEX IF NOT EXISTS idx_bot_events_timestamp
ON bot_events(timestamp DESC);

CREATE INDEX IF NOT EXISTS idx_bot_events_guild_id
ON bot_events(guild_id);

CREATE INDEX IF NOT EXISTS idx_bot_events_event_type
ON bot_events(event_type);

CREATE INDEX IF NOT EXISTS idx_bot_events_message_id
ON bot_events(message_id);



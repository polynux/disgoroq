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

-- Stores conversation summaries with vector embeddings
CREATE TABLE IF NOT EXISTS conversation_summaries (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    guild_id TEXT NOT NULL,
    user_id TEXT NOT NULL,  -- Author whose conversation is summarized
    summary_text TEXT NOT NULL,  -- Compressed summary
    message_count INTEGER NOT NULL,  -- Number of messages summarized
    start_message_id TEXT,  -- First Discord message ID in this summary
    end_message_id TEXT,  -- Last Discord message ID in this summary
    created_at INTEGER NOT NULL,  -- Unix timestamp
    updated_at INTEGER NOT NULL,  -- Unix timestamp (for incremental updates)
    embedding F32_BLOB  -- Vector embedding (768 dims for nomic-embed-text)
);

-- Indexes for efficient retrieval
CREATE INDEX IF NOT EXISTS idx_summaries_guild_user 
ON conversation_summaries(guild_id, user_id, updated_at DESC);

CREATE INDEX IF NOT EXISTS idx_summaries_created 
ON conversation_summaries(guild_id, created_at DESC);

-- Vector index for semantic search (DiskANN)
CREATE INDEX IF NOT EXISTS idx_summaries_vector 
ON conversation_summaries(libsql_vector_idx(embedding));

-- Temporary buffer for messages before summarization
CREATE TABLE IF NOT EXISTS message_buffer (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    guild_id TEXT NOT NULL,
    channel_id TEXT NOT NULL,
    message_id TEXT NOT NULL UNIQUE,  -- Discord message ID
    user_id TEXT NOT NULL,
    author_nick TEXT NOT NULL,
    content TEXT NOT NULL,
    has_image BOOLEAN DEFAULT 0,
    image_description TEXT,  -- From vision model
    timestamp INTEGER NOT NULL,
    processed BOOLEAN DEFAULT 0  -- Whether it's been summarized
);

CREATE INDEX IF NOT EXISTS idx_buffer_guild_user 
ON message_buffer(guild_id, user_id, processed, timestamp ASC);

CREATE INDEX IF NOT EXISTS idx_buffer_channel 
ON message_buffer(channel_id, processed, timestamp ASC);

CREATE INDEX IF NOT EXISTS idx_buffer_message_id
ON message_buffer(message_id);

CREATE TABLE IF NOT EXISTS attachment_cache (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    cache_kind TEXT NOT NULL,
    attachment_key TEXT NOT NULL,
    source_url TEXT NOT NULL DEFAULT '',
    filename TEXT NOT NULL DEFAULT '',
    content_type TEXT NOT NULL DEFAULT '',
    size_bytes INTEGER NOT NULL DEFAULT 0,
    provider TEXT NOT NULL,
    model TEXT NOT NULL,
    instruction_version TEXT NOT NULL,
    content TEXT NOT NULL,
    created_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
    updated_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
    UNIQUE(cache_kind, attachment_key, provider, model, instruction_version)
);

CREATE INDEX IF NOT EXISTS idx_attachment_cache_lookup
ON attachment_cache(cache_kind, attachment_key, provider, model, instruction_version);

-- Voice settings per guild
CREATE TABLE IF NOT EXISTS voice_settings (
    guild_id TEXT PRIMARY KEY,
    auto_join BOOLEAN DEFAULT FALSE,
    auto_join_channel TEXT,
    voice_enabled BOOLEAN DEFAULT TRUE,
    created_at INTEGER DEFAULT (strftime('%s', 'now')),
    updated_at INTEGER DEFAULT (strftime('%s', 'now'))
);

CREATE INDEX IF NOT EXISTS idx_voice_settings_guild_id
ON voice_settings(guild_id);

# DisgoroQ

DisgoroQ is a Discord bot written in Go, integrating with GROQ and SQLite.

## Project Structure
```
.
├── .air.toml
├── .env.example
├── .gitignore
├── Makefile
├── db/                  # SQLc generated code
├── database/            # Database operations and repository
├── handlers/            # Discord message handlers
├── scheduler/           # Scheduled tasks (horoscope, farting friday, event cleanup)
├── logger/              # Structured logging setup
├── ai/                  # AI providers and context builders
├── commands/            # Discord slash commands
├── horoscope/           # Horoscope scraping functionality
├── utils/               # Database initialization utilities
├── go.mod
├── go.sum
├── main.go
├── query.sql            # SQL queries for sqlc
├── schema.sql           # Database schema
└── sqlc.yaml            # sqlc configuration
```

## Dependencies

I use go 1.23.0 for this project. The following libraries are used:
- discordgo: Discord API library for Go
- groq-go: GROQ client for Go
- go-libsql: SQLite driver for Go
- godotenv: Load environment variables from .env files
- zap: Fast, structured logging library
- gocron: Job scheduling library
- go-co-op/gocron: Cron-like job scheduling
- sqlc: Type-safe SQL generation
- mockery: Mock generation for interfaces
- testify: Assertion library for testing

## Getting Started

1. Clone the repository
2. Copy `.env.example` to `.env` and fill in your Discord bot token and other necessary credentials
3. Run `go mod download` to install dependencies
4. Build and run the bot with `go run main.go`

## Configuration

Environment variables are used for configuration. See `.env.example` for required variables.

### Logging Configuration

The bot features a comprehensive logging system with environment-based controls:

#### Core Logging Settings

| Variable | Default | Description |
|----------|---------|-------------|
| `LOG_ENABLED` | `true` | Enable/disable all logging output |
| `LOG_LEVEL` | `info` | Log verbosity: `debug`, `info`, `warn`, `error` |
| `LOG_ENCODING` | `json` | Log format: `json` or `console` |

#### Database Event Logging

| Variable | Default | Description |
|----------|---------|-------------|
| `LOG_TO_DB` | `false` | Enable automatic database event logging |
| `EVENT_LOGGING_ENABLED` | `true` | Enable event storage in database |
| `EVENT_RETENTION_DAYS` | `7` | Days to retain events before cleanup |
| `DB_LOG_LEVEL` | `info` | Database log level: `none`, `error`, `warn`, `info`, `debug`, `all` |

#### Usage Examples

```bash
# Development logging with console output
LOG_ENABLED=true
LOG_LEVEL=debug
LOG_ENCODING=console
DB_LOG_LEVEL=debug

# Production logging with database events (errors only)
LOG_ENABLED=true
LOG_LEVEL=info
LOG_ENCODING=json
LOG_TO_DB=true
EVENT_LOGGING_ENABLED=true
EVENT_RETENTION_DAYS=30
DB_LOG_LEVEL=error

# Production logging with database events (standard)
LOG_ENABLED=true
LOG_LEVEL=info
LOG_ENCODING=json
LOG_TO_DB=true
EVENT_LOGGING_ENABLED=true
EVENT_RETENTION_DAYS=7
DB_LOG_LEVEL=info

# Minimal logging for performance
LOG_ENABLED=true
LOG_LEVEL=warn
LOG_TO_DB=false
EVENT_LOGGING_ENABLED=false
DB_LOG_LEVEL=none

# Complete audit trail
LOG_ENABLED=true
LOG_LEVEL=debug
LOG_ENCODING=json
LOG_TO_DB=true
EVENT_LOGGING_ENABLED=true
EVENT_RETENTION_DAYS=90
DB_LOG_LEVEL=all
```

## Development

1. Install [Air](https://github.com/air-verse/air) for live reloading: `go install github.com/air-verse/air@latest`
2. Run `air` in the project directory

## Event Logging

The bot includes comprehensive event logging for debugging and monitoring:

### Automatic Event Logging

When `LOG_TO_DB=true`, the following events are automatically logged:
- Message processing and AI responses
- Command executions and interactions
- Scheduled task executions (horoscope, farting friday)
- Database operations and cleanup
- Error conditions and failures

### Manual Event Logging

Use the wrapper functions in your code:

```go
import "polynux/disgoroq/logger"

// Log simple events
logger.Info("User joined guild", 
    zap.String("user_id", userID),
    zap.String("guild_id", guildID))

logger.Error("Failed to process message",
    zap.Error(err),
    zap.String("message_id", msgID))

// Log structured events to database
logger.LogMessageEvent(ctx, 
    database.EventContextBuilt,
    guildID, channelID, messageID, userID,
    &database.EventDetails{
        MessagesCount: 10,
        ImageCount: 2,
    })
```

### Database Log Levels

The `DB_LOG_LEVEL` environment variable controls which events are stored in the database:

#### Log Level Hierarchy

| Level | Events Logged | Use Case |
|-------|---------------|----------|
| `none` | No events | Disable all database logging |
| `error` | Only errors and failures | Production monitoring |
| `warn` | Errors + warnings | Enhanced monitoring |
| `info` | Errors + warnings + info events | Standard logging |
| `debug` | All events including debug | Development/debugging |
| `all` | All events (same as debug) | Complete audit trail |

#### Event Type Mapping

**Error Level Events:**
- `ai_call_failed` - AI API call failed
- `context_failed` - Failed to build message context
- `response_failed` - Failed to send response to Discord

**Warning Level Events:**
- `rate_limited` - Rate limit hit for guild
- `empty_response` - AI returned empty response
- `threshold_skipped` - Random threshold skipped responding

**Info Level Events:**
- `message_received` - Incoming Discord message
- `context_built` - Message context prepared for AI
- `response_sent` - Response successfully sent to Discord

**Debug Level Events:**
- `ai_call_start` - Started AI API call
- `ai_call_success` - AI call completed successfully
- `state_off` - Bot is disabled for this guild

#### Usage Examples

```bash
# Production: Only log errors and failures
DB_LOG_LEVEL=error

# Development: Log everything for debugging
DB_LOG_LEVEL=debug

# Performance: Minimal logging
DB_LOG_LEVEL=warn

# Complete audit trail
DB_LOG_LEVEL=all
```

### Event Types

- `message_received` - Incoming Discord message
- `threshold_skipped` - Random threshold skipped responding
- `state_off` - Bot is disabled for this guild
- `rate_limited` - Rate limit hit
- `context_built` - Message context prepared for AI
- `context_failed` - Failed to build context
- `ai_call_start` - Started AI API call
- `ai_call_success` - AI call completed successfully
- `ai_call_failed` - AI call failed
- `empty_response` - AI returned empty response
- `response_sent` - Response sent to Discord
- `response_failed` - Failed to send response
- `ai_retrying` - AI retry attempt in progress
- `ai_fallback` - Fallback to secondary provider triggered
- `provider_switch` - Provider changed during fallback

### Event Management

- Events are automatically cleaned up based on retention policy (default: 7 days)
- Configure retention with `EVENT_RETENTION_DAYS` environment variable
- Scheduled cleanup job runs daily at 3 AM
- Events include detailed context (guild, channel, user, duration, error information)

## Logging Wrapper Functions

The bot provides wrapper functions that respect environment settings:

```go
import "polynux/disgoroq/logger"

// Basic logging (respects LOG_ENABLED setting)
logger.Debug("Debug information")
logger.Info("Information message")
logger.Warn("Warning message") 
logger.Error("Error occurred")
logger.Fatal("Fatal error")

// With structured fields
logger.Info("User action",
    zap.String("user_id", userID),
    zap.String("action", "join"))

// Automatic event logging (respects EVENT_LOGGING_ENABLED setting)
logger.LogEvent(ctx, &database.BotEvent{
    EventType: database.EventMessageReceived,
    GuildID:   guildID,
    ChannelID: channelID,
    UserID:    userID,
})

// Convenience function for message events
logger.LogMessageEvent(ctx, 
    database.EventContextBuilt,
    guildID, channelID, messageID, userID,
    &database.EventDetails{
        MessagesCount: 10,
        ImageCount:    2,
    })
```

### Environment-Based Behavior

The wrapper functions automatically respect these environment settings:

- **LOG_ENABLED=false**: All logging calls are no-ops (high performance)
- **LOG_LEVEL=warn**: Debug/Info calls are ignored, Warn/Error work normally
- **LOG_TO_DB=true**: Error logs are automatically stored in database
- **EVENT_LOGGING_ENABLED=false**: Database event logging is disabled
- **DB_LOG_LEVEL=none**: No events are stored in database (even if LOG_TO_DB=true)

### Automatic Event Logging

When `LOG_TO_DB=true`, the wrapper functions automatically create and log events to the database:

#### **Error Logging**
```go
// This automatically creates a database event
goog.logger.Error("AI API call failed", 
    zap.Error(err),
    zap.String("guild_id", guildID),
    zap.String("user_id", userID))

// Creates: EventAICallFailed with error details
```

#### **Warning Logging**
```go
// This automatically creates a database event
goog.logger.Warn("Rate limit exceeded",
    zap.String("guild_id", guildID),
    zap.String("channel_id", channelID))

// Creates: EventRateLimited with context
```

#### **Info Logging**
```go
// This creates a database event only if guild context is provided
goog.logger.Info("User joined guild",
    zap.String("guild_id", guildID),
    zap.String("user_id", userID))

// Creates: EventMessageReceived (only with guild context)

// This does NOT create a database event (no context)
goog.logger.Info("Application started")
```

#### **Debug Logging**
```go
// This creates events only when DB_LOG_LEVEL=debug or all
goog.logger.Debug("Processing message",
    zap.String("guild_id", guildID))

// Creates: EventAICallStart (only in debug mode)
```

#### **Context Extraction**
The automatic logging extracts these fields from zap fields:
- `guild_id` → `event.GuildID`
- `channel_id` → `event.ChannelID` 
- `message_id` → `event.MessageID`
- `user_id` → `event.UserID`
- `error` → `event.Error`

#### **Event Type Mapping**
| Log Level | Event Type Created | Conditions |
|-----------|-------------------|------------|
| `Error()` | `EventAICallFailed` | When error field present |
| `Error()` | `EventResponseFailed` | When no error field |
| `Warn()` | `EventRateLimited` | Always |
| `Info()` | `EventMessageReceived` | Only with guild context |
| `Debug()` | `EventAICallStart` | Only when DB_LOG_LEVEL=debug/all |
| `Fatal()` | `EventResponseFailed` | Always (before exit) |

## AI Provider Resilience System

DisgoroQ now includes a comprehensive AI provider resilience system with automatic retry logic and fallback support to handle empty responses and API failures.

### Features

- **Automatic Retry Logic**: Retries failed or empty AI responses with exponential backoff
- **Provider Fallback**: Automatically switches to backup providers (e.g., Ollama) when primary provider fails
- **Empty Response Detection**: Validates AI responses and retries when content is empty or invalid
- **Comprehensive Logging**: Detailed logging of retry attempts, fallback switches, and failures
- **Configurable Behavior**: Full control over retry counts, delays, and fallback behavior via environment variables

### Architecture

```
Message Handler → AI Service → Provider Chain → Retry Wrapper (per provider)
                                           ↓
                                    Primary: Retry Wrapper (Groq, model="llama-3-70b-versatile")
                                           ↓ (immediate fallback on failure)
                                    Fallback: Retry Wrapper (Ollama, model="dolphin3")
```

#### Model Configuration

Each provider now has its own model configuration:

- **Groq**: Uses `GROQ_MODEL` for chat and `GROQ_VISION_MODEL` for vision
- **Ollama**: Uses `OLLAMA_MODEL` for chat and `OLLAMA_VISION_MODEL` for vision

Models are configured at service initialization and automatically injected during API calls. Callers no longer need to specify models - the system uses the appropriate model for each provider automatically.

### Configuration

#### Retry Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `AI_MAX_RETRIES` | 2 | Number of retry attempts (0-10) |
| `AI_RETRY_INITIAL_DELAY_MS` | 500 | Initial retry delay in milliseconds (100-5000) |
| `AI_RETRY_MAX_DELAY_MS` | 5000 | Maximum retry delay in milliseconds (1000-30000) |
| `AI_RETRY_BACKOFF` | 2.0 | Exponential backoff multiplier (1.0-5.0) |
| `AI_RETRY_ON_EMPTY` | true | Retry on empty responses |
| `AI_RETRY_ON_ERROR` | true | Retry on API errors |

#### AI Model Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `GROQ_MODEL` | llama-3-70b-versatile | Groq chat model |
| `GROQ_VISION_MODEL` | meta-llama/llama-4-scout-17b-16e-instruct | Groq vision model |

#### Fallback Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `AI_FALLBACK_ENABLED` | true | Enable provider fallback |
| `AI_MIN_RESPONSE_LENGTH` | 1 | Minimum valid response length (1-100) |

#### Ollama Configuration (Optional Fallback)

| Variable | Default | Description |
|----------|---------|-------------|
| `OLLAMA_ENABLED` | false | Enable Ollama fallback provider |
| `OLLAMA_API_URL` | http://localhost:11434 | Ollama API URL |
| `OLLAMA_MODEL` | dolphin3 | Ollama chat model |
| `OLLAMA_VISION_MODEL` | llava | Ollama vision model |

### Event Types

The system logs additional events for monitoring retry and fallback behavior:

- `ai_retrying` - Retry attempt in progress (debug level)
- `ai_fallback` - Fallback to secondary provider triggered (info level)
- `provider_switch` - Provider changed during fallback (info level)

### Usage Examples

#### Basic Configuration
```bash
# Enable retry with 2 attempts and fallback to Ollama
AI_MAX_RETRIES=2
AI_FALLBACK_ENABLED=true

# Configure models for each provider
GROQ_MODEL="llama-3-70b-versatile"
OLLAMA_ENABLED=true
OLLAMA_API_URL="http://localhost:11434"
OLLAMA_MODEL="dolphin3"
```

#### Production Configuration
```bash
# Conservative retry settings for production
AI_MAX_RETRIES=1
AI_RETRY_INITIAL_DELAY_MS=1000
AI_RETRY_MAX_DELAY_MS=3000

# Enable fallback with custom models
AI_FALLBACK_ENABLED=true
OLLAMA_ENABLED=true
GROQ_MODEL="llama-3-70b-versatile"
OLLAMA_MODEL="dolphin3"
```

#### Development Configuration
```bash
# Aggressive retry settings for debugging
AI_MAX_RETRIES=5
AI_RETRY_INITIAL_DELAY_MS=100
AI_RETRY_MAX_DELAY_MS=5000
AI_RETRY_BACKOFF=1.5

# Enable detailed logging
DB_LOG_LEVEL=debug  # Log all retry and fallback events

# Test different models
GROQ_MODEL="llama-3-70b-versatile"
GROQ_VISION_MODEL="meta-llama/llama-4-scout-17b-16e-instruct"
```

#### High Availability Configuration
```bash
# Maximum resilience configuration
AI_MAX_RETRIES=3
AI_RETRY_ON_EMPTY=true
AI_RETRY_ON_ERROR=true
AI_FALLBACK_ENABLED=true

# Configure different models for primary and fallback
OLLAMA_ENABLED=true
GROQ_MODEL="llama-3-70b-versatile"
OLLAMA_MODEL="llama2"  # Use a different model for fallback
GROQ_VISION_MODEL="meta-llama/llama-4-scout-17b-16e-instruct"
```

### Error Messages

The system provides user-friendly error messages when all retry and fallback attempts are exhausted:

- **Without fallback**: "Désolé, j'ai des soucis techniques là... 🤖💀"
- **With fallback**: "Désolé, tous mes systèmes sont en rade... 🤖💀"

### Monitoring

Use these database queries to monitor the resilience system:

```sql
-- Monitor retry success rate
SELECT 
  COUNT(CASE WHEN event_type = 'ai_call_success' THEN 1 END) as successes,
  COUNT(CASE WHEN event_type = 'ai_retrying' THEN 1 END) as retries,
  COUNT(CASE WHEN event_type = 'ai_fallback' THEN 1 END) as fallbacks
FROM bot_events 
WHERE timestamp > unixepoch('now', '-1 day');

-- Monitor fallback usage by provider
SELECT 
  JSON_EXTRACT(details, '$.provider') as provider,
  COUNT(*) as fallback_count
FROM bot_events 
WHERE event_type = 'ai_fallback'
  AND timestamp > unixepoch('now', '-7 days')
GROUP BY provider;

-- Monitor empty response rate
SELECT 
  COUNT(CASE WHEN event_type = 'empty_response' THEN 1 END) as empty_responses,
  COUNT(CASE WHEN event_type = 'ai_call_success' THEN 1 END) as successful_calls
FROM bot_events 
WHERE timestamp > unixepoch('now', '-1 day');
```

## Technical Implementation

### SQLC Integration

The logging system leverages SQLC-generated type-safe database queries:

- **EventRepository** uses SQLC's `InsertEvent` query for type-safe event insertion
- **Automatic parameter mapping** from Go structs to SQL parameters
- **Null safety** with `sql.NullString` and `sql.NullInt64` for optional fields
- **Transaction support** through SQLC's `WithTx()` method

### Database Log Level Filtering

The `DB_LOG_LEVEL` system implements efficient event filtering:

- **Pre-insertion filtering** - Events are filtered before database operations
- **Hierarchical levels** - Higher levels automatically include lower level events
- **Unknown event handling** - Unmapped events default to info level
- **Performance optimized** - No database calls for filtered-out events

### Configuration System

Environment-based configuration with intelligent defaults:

- **Case-insensitive parsing** for log levels (`ERROR`, `error`, `Error` all work)
- **Boolean flexibility** - Supports `true`, `1`, `yes`, `on`, `enabled`
- **Graceful degradation** - Uses sensible defaults if .env file is missing
- **Runtime validation** - Invalid values fall back to safe defaults

### Performance Considerations

- **Pre-filtering**: Events are filtered before database operations
- **Context-aware**: Info events only logged when contextual data present
- **Debug mode**: Debug logging only active when explicitly enabled
- **Null repository**: Graceful handling when no event repository configured

### Example Usage Scenarios

#### **Production Monitoring**
```bash
# Monitor errors and warnings only
LOG_TO_DB=true
DB_LOG_LEVEL=warn
EVENT_RETENTION_DAYS=30
```

#### **Development Debugging**
```bash
# Log everything for debugging
LOG_TO_DB=true
DB_LOG_LEVEL=debug
EVENT_RETENTION_DAYS=7
```

#### **High Performance**
```bash
# Minimal database logging
LOG_TO_DB=true
DB_LOG_LEVEL=error
EVENT_RETENTION_DAYS=3
```

## Testing

### Running Tests

The project includes a comprehensive test suite covering the main packages:

```bash
# Run all unit tests
make test-unit

# Run integration tests (when available)
make test-integration

# Run all tests
make test-all
# or just
make test
```

### Test Coverage

Tests are organized by package:

- **database/** - Database repository tests (libsql, in-memory DB)
- **horoscope/** - Horoscope scraper tests (mock HTTP servers)
- **ai/** - AI provider and context builder tests (mocks)
- **commands/** - Command registry and handler tests (mocks)

### Adding New Tests

When adding new features, follow these guidelines:

1. Use Go's standard `testing` package
2. Use `testify/assert` and `testify/require` for better assertions
3. Use `mockery` to generate mocks for interfaces
4. Keep tests focused and independent
5. Mock external dependencies (HTTP, Discord, AI providers)
6. Use in-memory databases for database tests

### Test Tools

- **Go testing**: Standard Go testing framework
- **testify**: Assertion library for Go (already installed as dependency)
- **mockery**: Mock generation tool (install with `go install github.com/vektra/mockery/v2@latest`)

## License

[GPL-3.0 License](LICENSE)

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.


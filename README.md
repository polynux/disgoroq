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

#### Usage Examples

```bash
# Development logging with console output
LOG_ENABLED=true
LOG_LEVEL=debug
LOG_ENCODING=console

# Production logging with database events
LOG_ENABLED=true
LOG_LEVEL=info
LOG_ENCODING=json
LOG_TO_DB=true
EVENT_LOGGING_ENABLED=true
EVENT_RETENTION_DAYS=30

# Minimal logging for performance
LOG_ENABLED=true
LOG_LEVEL=warn
LOG_TO_DB=false
EVENT_LOGGING_ENABLED=false
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


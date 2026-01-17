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

## Development

1. Install [Air](https://github.com/air-verse/air) for live reloading: `go install github.com/air-verse/air@latest`
2. Run `air` in the project directory

## Event Logging

The bot includes comprehensive event logging for debugging and monitoring:

- All bot events are logged to the `bot_events` table
- Events include: message processing, AI calls, errors, response sending
- Events are automatically cleaned up based on retention policy (default: 7 days)
- Configure retention with `EVENT_RETENTION_DAYS` environment variable
- Scheduled cleanup job runs daily at 3 AM

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


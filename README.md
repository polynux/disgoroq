# DisgoroQ

DisgoroQ is a Discord bot written in Go, integrating with GROQ and SQLite.

## Project Structure
```
.
├── .air.toml
├── .env.example
├── .gitignore
├── db/
├── go.mod
├── go.sum
├── main.go
├── query.sql
├── schema.sql
├── sqlc.yaml
└── utils/
```

## Dependencies

I use go 1.23.0 for this project. The following libraries are used:
- discordgo: Discord API library for Go
- groq-go: GROQ client for Go
- go-libsql: SQLite driver for Go
- godotenv: Load environment variables from .env files

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


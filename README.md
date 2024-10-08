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

## License

[GPL-3.0 License](LICENSE)

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.


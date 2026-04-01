# DisgoroQ

DisgoroQ is a Go Discord bot with AI chat, image-aware context, guild-scoped memory, reengage checks, manual horoscope delivery, scheduled Farting Friday posts, and optional voice chat.

## Requirements

- Go 1.24+
- A Discord bot token
- GROQ API access
- A configured database
- For voice: `libdave`, the STT sidecar, and the TTS service

## Configuration

- `config.yaml` is the source of truth for runtime behavior.
- Environment variables are for secrets and values interpolated into `config.yaml`.
- Guild settings stored in the database are explicit overrides set through commands.
- Horoscope stays manual-only.

Typical interpolated secrets:

- `DISCORD_TOKEN`
- `GROQ_API_KEY`
- `DB_URL`
- `DB_TOKEN`

Start from `config.example.yaml`, then review `config.yaml` for runtime defaults like AI, memory, reengage, and voice.

## Build And Run

- Build: `make build`
- Test: `make test`
- Run: `go run .`
- Run in local/dev mode: `go run . -local`
- Live reload: `air`
- Regenerate SQLC after query changes: `go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.30.0 generate`

## Voice

- Voice is optional and disabled by default.
- Voice requires the external STT socket service and TTS HTTP service configured under `voice` in `config.yaml`.
- The build and test workflow expects the local `libdave` pkg-config path used by the Makefile.
- Per-guild voice usage is still gated by stored `voice_enabled` settings.

## Notes

- Local slash command registration uses `discord.dev_guild_ids` from config.
- Runtime logging goes through the `logger` package.
- System flow notes for memory and voice live in `SYSTEMS.md`.

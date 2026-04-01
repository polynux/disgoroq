# Project Knowledge Base

## Overview

DisgoroQ is a Go Discord bot with:

- AI chat through GROQ with Ollama fallback support.
- Message and attachment context building.
- Guild-scoped conversation memory.
- Reengage checks for inactive channels.
- Manual horoscope delivery.
- Scheduled Farting Friday posts and log cleanup.
- Optional voice chat with STT and TTS.

## Source Of Truth

- Runtime behavior belongs in `config.yaml`.
- Secrets come from environment variables referenced by config.
- Generated SQL code in `db/` is not edited directly.
- Shared runtime logging goes through `logger`.

## Where To Look

| Task | Location | Notes |
|------|----------|-------|
| Startup and wiring | `main.go` | Builds config, DB, AI, handlers, scheduler, voice |
| Message replies | `handlers/message.go` | Main text reply flow |
| Slash commands | `commands/` | Registry, permissions, command handlers |
| Guild settings | `database/repository.go` | App-facing settings repository |
| Memory | `memory/` | Buffering, summaries, retrieval |
| Voice | `voice/` and `handlers/voice.go` | Orchestration, audio, voice state events |
| Scheduling | `scheduler/` | Farting Friday, cleanup, reengage checks |
| AI providers | `ai/` | GROQ, Ollama, context building |
| Config | `config/` | Defaults, loading, validation |
| SQL queries | `query.sql` and `db/` | sqlc source and generated code |

## Current Behavior Notes

- Horoscope is not scheduled; it is manual-only.
- Local/dev slash command registration uses guild commands and requires `discord.dev_guild_ids`.
- Reengage activity is tracked from all non-bot guild messages, not only when the bot replies.
- Voice join now respects per-guild `voice_enabled`.
- Voice playback uses configured default voice, text fallback policy, max text length, stream buffer size, and frame timing.

## Conventions

- Prefer small edits over broad refactors.
- Keep behavior config-driven when the setting already exists.
- Avoid direct `fmt` or stdlib `log` runtime logging in app code.
- Use repositories instead of bypassing them for guild settings.
- Keep manual code edits out of generated files.

## Known Risks

- `main.go` is still large and centralizes a lot of wiring.
- Some older tests are stale or blocked by driver/linker issues.
- Voice behavior depends on external STT/TTS services being reachable.
- Horoscope scraping remains brittle because it depends on a third-party site.

## Useful Commands

```bash
go run .
go run . -local
go test ./...
go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.30.0 generate
air
```

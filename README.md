# DisgoroQ

DisgoroQ is a Go Discord bot with AI chat, image-aware context, guild-scoped memory, reengage checks, manual horoscope delivery, scheduled Farting Friday posts, and optional voice chat.

## Requirements

- Go 1.24+
- A Discord bot token
- GROQ API access, a reachable Ollama instance, or an OpenCode Go API key depending on `ai.primary_provider`
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
- `OPENCODE_API_KEY`
- `DB_URL`
- `DB_TOKEN`

Start from `config.example.yaml`, then review `config.yaml` for runtime defaults like AI, memory, reengage, and voice.

For AI, `ai.primary_provider` defaults to `groq`. Set it to `ollama` and enable `ai.ollama.enabled` to run Ollama by default without requiring `GROQ_API_KEY`, or set it to `opencode` and enable `ai.opencode.enabled` with `OPENCODE_API_KEY` to use OpenCode Go's OpenAI-compatible `chat/completions` endpoint.

When image attachments are present, DisgoroQ now keeps raw attachment refs in chat context. If the selected provider is using the same supported multimodal model for chat and vision, attachments are sent inline to chat; otherwise the bot falls back to a separate vision-to-text description step before chat.

Each AI provider section also exposes `thinking_enabled`:

- `ai.ollama.thinking_enabled` maps directly to Ollama's `think` request flag.
- `ai.groq.thinking_enabled` keeps Groq's default reasoning behavior when true and sends a lower/no reasoning effort hint when false for supported models.
- `ai.opencode.thinking_enabled` keeps the provider default when true and sends `reasoning_effort: none` when false on OpenCode chat-completions requests.

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

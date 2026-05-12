# DisgoroQ

DisgoroQ is a Go Discord bot with AI chat, image-aware context, guild-scoped memory, reengage checks, manual horoscope delivery, scheduled Farting Friday posts, and optional voice chat.

## Requirements

- Go 1.24+
- A Discord bot token
- GROQ API access, a reachable Ollama instance, an OpenCode Go API key, or an OpenRouter API key depending on `ai.primary_provider`
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
- `OPENROUTER_API_KEY`
- `DB_URL`
- `DB_TOKEN`

Start from `config.example.yaml`, then review `config.yaml` for runtime defaults like AI, memory, reengage, and voice.

For AI, `ai.primary_provider` defaults to `groq`. Set it to `ollama` and enable `ai.ollama.enabled` to run Ollama by default without requiring `GROQ_API_KEY`, set it to `opencode` and enable `ai.opencode.enabled` with `OPENCODE_API_KEY` to use OpenCode Go's OpenAI-compatible `chat/completions` endpoint, or set it to `openrouter` and enable `ai.openrouter.enabled` with `OPENROUTER_API_KEY` to use OpenRouter's OpenAI-compatible `/api/v1/chat/completions` endpoint.

When image attachments are present, DisgoroQ keeps raw attachment refs in chat context. If the selected provider is using the same supported multimodal model for chat and vision, attachments are sent inline to chat; otherwise the bot falls back to a separate vision-to-text description step before chat. Detached image descriptions and document summaries are cached in the database per attachment fingerprint, provider, model, and prompt version so repeated requests do not keep paying the same preprocessing cost.

Each AI provider section also exposes `thinking_enabled`:

- `ai.ollama.thinking_enabled` maps directly to Ollama's `think` request flag.
- `ai.groq.thinking_enabled` keeps Groq's default reasoning behavior when true and sends a lower/no reasoning effort hint when false for supported models.
- `ai.opencode.thinking_enabled` keeps the provider default when true and sends `reasoning_effort: none` when false on OpenCode chat-completions requests.
- `ai.openrouter.thinking_enabled` keeps the provider default when true and sends OpenRouter's normalized `reasoning: { effort: "none", exclude: true }` when false.

### Tool Calling And Zero-Cost Web Search

DisgoroQ now supports provider-agnostic tool calling for chat models. The built-in tools are:

- `web_fetch` for fetching a known URL and converting its content into markdown-friendly text.
- `web_search` for zero-API-cost search through a self-hosted `SearXNG` instance.

Typical bot-side config:

```yaml
ai:
  tools:
    enabled: true
    max_rounds: 3
    max_calls_per_round: 2
    max_calls_total: 4
    timeout_ms: 10000
    web:
      enabled: true
    search:
      enabled: true
      provider: "searxng"
      base_url: "http://127.0.0.1:8888"
      max_results: 5
      default_language: "fr"
      safe_search: 2
```

Recommended `SearXNG` tweaks for this bot:

- Prefer `server.bind_address: "127.0.0.1"` when the bot runs on the same host. Keep `0.0.0.0` only if the bot must reach SearXNG from another container or machine.
- Keep `valkey` enabled if available. It helps search responsiveness and reduces repeated upstream work.
- `server.limiter: false` is acceptable for a private bot-only instance. Turn it on if the instance is exposed beyond your local/private network.
- **Enable JSON results** in SearXNG. DisgoroQ calls `/search?format=json`, and SearXNG defaults to `search.formats: [html]`, which causes `403 FORBIDDEN` until `json` is allowed too:

```yaml
search:
  safe_search: 2
  formats:
    - html
    - json
```

- `search.safe_search` is fine as a backend default, but DisgoroQ also sends its own `ai.tools.search.safe_search` value on each request. Keep them aligned if you want predictable behavior.
- `server.image_proxy`, `search.autocomplete`, and most UI plugins do not matter for DisgoroQ's JSON search flow.
- Your current engine set is enough to start. If you want broader/fallback coverage, add one more non-API engine such as `wikipedia`, `qwant`, or `startpage`.

Security note: keep `server.secret_key` private. If a real secret key was pasted into chat or another public place, rotate it.

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

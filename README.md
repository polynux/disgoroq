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

When image attachments are present, DisgoroQ keeps raw attachment refs in chat context. Set optional `ai.vision_provider` (`groq`, `ollama`, `opencode`, or `openrouter`) to route image descriptions through that enabled provider first (using its `vision_model`), then fall back to the other enabled providers without calling the vision provider twice; leave it empty to start with the active chat provider as before. When `ai.vision_provider` is set to a provider other than `ollama`, `ai.ollama.vision_model` may be empty. Inline image attachments apply only when the active chat provider is also the vision provider (or `ai.vision_provider` is empty) and uses the same supported multimodal model for chat and vision; otherwise images are described by the vision provider and passed to chat as text. Detached image descriptions and document summaries are cached in the database per attachment fingerprint, provider, model, and prompt version so repeated requests do not keep paying the same preprocessing cost.

Each AI provider section also exposes `thinking_enabled`:

- `ai.ollama.thinking_enabled` maps directly to Ollama's `think` request flag.
- `ai.groq.thinking_enabled` keeps Groq's default reasoning behavior when true and sends a lower/no reasoning effort hint when false for supported models.
- `ai.opencode.thinking_enabled` keeps the provider default when true. When false it sends the lowest reasoning effort each model accepts: `none` for DeepSeek, Kimi K3, GLM-5.3 and MiniMax M3, `minimal` for Muse Spark (it rejects `none`), and no hint for other models.
- `ai.openrouter.thinking_enabled` keeps the provider default when true and sends OpenRouter's normalized `reasoning: { effort: "none", exclude: true }` when false.

### OpenCode Go (Fallback, Vision, And Summaries)

OpenCode Go defaults to `https://opencode.ai/inference/go/v1` with `deepseek-v4.1-flash` (current models: `deepseek-v4.1-flash` and `muse-spark-1.3-contributor`). You can enable it for chat fallback and vision alongside another primary provider, and reuse `ai.opencode.api_key` and `ai.opencode.base_url` for memory summaries:

```yaml
ai:
  primary_provider: "groq"
  fallback_enabled: true
  opencode:
    enabled: true
    base_url: "https://opencode.ai/inference/go/v1"
    api_key: "${OPENCODE_API_KEY}"
    model: "deepseek-v4.1-flash"
    vision_model: "deepseek-v4.1-flash"

memory:
  enabled: true
  summary_provider: "opencode"
  summary_model: "deepseek-v4.1-flash"
```

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

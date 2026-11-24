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

With `ai.ollama.completion: true`, Ollama sends the recent channel as raw `name: text` lines to `/api/generate` for models trained on Discord logs. `ai.ollama.completion_speaker` sets the bot's speaker tag (`§bot` for Luciole; empty to use the display name), `ai.ollama.completion_prompt_chars` caps the prompt, `ai.ollama.completion_bot_lines` caps how many of the bot's own lines stay in it (at least 1, default 4; error replies and command outputs are always excluded), and `ai.ollama.completion_media_tags` chooses the media-tag contract (`v0` placeholders by default, `v1` descriptive tags). Completion mode never answers another bot or a webhook.

With `media_library.enabled: true`, DisgoroQ indexes GIF and described-link URLs posted by humans in guilds (including links whose embed descriptions arrive via `MESSAGE_UPDATE`, using `memory.ollama_url` and `memory.embedding_model`, defaulting to `nomic-embed-text`) and resolves completion-mode media tags (`[gif]`, `[gif: ...]`, `[youtube: ...]`, `[tweet: ...]`, `[tiktok: ...]`, `[insta: ...]`, `[son: ...]`, `[steam: ...]`) into real URLs. Uploaded GIF attachments are intentionally not live-indexed because they are expiring Discord CDN links excluded from posting by default. Settings under `media_library`: `enabled` (default `false`), `min_similarity` (default `0.80`), `min_trigger_similarity` (default `0.80`), `cross_guild` (default `false`, falls back to other guilds' entries when true), `allow_discord_cdn` (default `false`, excludes `cdn.discordapp.com` / `media.discordapp.net` links; when `true`, chosen Discord CDN URLs are refreshed to fresh signed URLs via Discord's `/attachments/refresh-urls` endpoint before posting while keeping canonical URLs in the DB), `repeat_window` (default `20`, max `200`, skips URLs posted by the bot in the last N bot messages in the channel), `lookup_timeout_ms` (default `3000`, per-lookup timeout independent of the completion context; raise to ~`6000` when `validate_links` is enabled so embedding, CDN refresh, and up to 3 checks at ~1.5 s each fit within the budget), `random_gif_fallback` (default `false`, picks a random eligible same-guild GIF — preferring `uses >= 2` — when a GIF tag has no match, junk words, or an embedder error/timeout), `gif_on_request` (default `false`, resolves a GIF via trigger search on the answered message, then random fallback if enabled, whenever the answered message asks for a `gif`/`gifs` and the reply has no media URL after resolution), and `validate_links` (default `false`, checks chosen URLs with `HEAD` following redirects — falling back to `GET` + `Range: bytes=0-0` on 405/501 — refreshing Discord CDN URLs first, deletes confirmed dead 404/410 or unrefreshable CDN rows and retries up to 3 candidates before random fallback, while keeping and posting URLs on transient/unknown errors). Import existing history with `disgoroq -import-media-library <file.jsonl> [-local]` and tune thresholds with `disgoroq -media-search "<text>" -guild <id> [-trigger] [-local]`.

When `ai.fallback_enabled` is true, set optional `ai.fallback_order` (a list of provider names from `groq`, `ollama`, `opencode`, `openrouter`, e.g. `["opencode", "groq"]`) to control the order fallback providers are tried. Chat always starts with `ai.primary_provider`, then tries enabled providers listed in `ai.fallback_order`, then appends any remaining enabled providers in the default order (`groq`, `ollama`, `opencode`, `openrouter`); an empty list keeps the default order. Vision follows the same order after `ai.vision_provider` (and for per-wrapper vision fallback when `ai.vision_provider` is empty). Unknown names and duplicates fail config validation, while listing the primary provider or a disabled provider is allowed and logged at startup.

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

# PROJECT KNOWLEDGE BASE

**Generated:** 2026-01-16
**Commit:** N/A
**Branch:** N/A

## OVERVIEW
DisgoroQ is a Discord bot written in Go that integrates with GROQ AI for conversational responses, uses SQLite for guild settings storage, scrapes horoscopes from horoscope.com, and sends scheduled notifications including a "Farting Friday" feature.

## STRUCTURE
```
.
├── main.go          # Main bot logic, commands, message handling
├── db/              # Database layer (sqlc generated)
├── horoscope/       # Horoscope scraping functionality
├── utils/           # Database initialization utilities
├── .env.example     # Environment configuration template
├── go.mod           # Go module dependencies
├── go.sum           # Dependency checksums
├── schema.sql       # Database schema
├── query.sql        # SQL queries for sqlc
├── sqlc.yaml        # sqlc configuration
└── .air.toml        # Air live reload config
```

## WHERE TO LOOK
| Task | Location | Notes |
|------|----------|-------|
| Bot commands & slash commands | main.go (lines 40-173) | Command definitions and handlers |
| Message processing logic | main.go (lines 844-1076) | messageCreate function with AI integration |
| Database operations | db/ | Type-safe queries via sqlc |
| Scheduled tasks | main.go (lines 500-532) | schedule function for horoscope/farting friday |
| Horoscope scraping | horoscope/ | Web scraping from horoscope.com |
| Image description | main.go (lines 803-842) | describeImage function using GROQ vision |

## CODE MAP
Main symbols (from code analysis):
- main() - Bot initialization and event loop
- messageCreate() - Handles incoming Discord messages
- sendHoroscope() - Processes and sends daily horoscopes
- sendFartingFriday() - Sends scheduled farting friday notification
- askGroq() - GROQ API integration for text generation
- describeImage() - GROQ vision API for image analysis

## CONVENTIONS
- Uses sqlc for type-safe SQL query generation
- Environment variables for configuration (.env)
- Standard Go project structure with packages in subdirectories
- Generated code in db/ package (DO NOT EDIT)
- Context usage for database operations
- Standard Go naming conventions

## ANTI-PATTERNS (THIS PROJECT)
- Large main.go file (1202 lines) - consider splitting into multiple files
- No unit tests present
- No CI/CD pipeline configured
- Hardcoded URLs and limits in code

## UNIQUE STYLES
- "Brainrot" AI persona with Gen Z slang and memes
- Delirant horoscope transformation using GROQ
- Scheduled "Farting Friday" notifications with embedded content
- Image processing with short descriptions for context
- Guild-specific settings stored in SQLite

## COMMANDS
```bash
go run main.go              # Run the bot
go mod download             # Install dependencies
air                         # Development with live reload
```

## NOTES
- Requires DISCORD_TOKEN, GROQ_API_KEY, DB_URL, DB_TOKEN in .env.local
- Uses libsql for SQLite with optional remote sync
- Horoscope scraping may be brittle due to website changes
- Bot responds randomly based on configurable threshold
- Rate limiting implemented with last_message timestamp</content>
<parameter name="filePath">./AGENTS.md
# CRITICAL MISSING FEATURES - ACTION REQUIRED

## Branch: merge/integration
## Status: INCOMPLETE - Missing scheduler emoji support and env vars

---

## MISSING FEATURES IDENTIFIED

### 1. ⚠️ SCHEDULER EMOJI SUPPORT (CRITICAL)

**Current State (merge/integration):**
- scheduler/scheduler.go: ❌ No emojiManager field
- scheduler/horoscope.go: ❌ No HOROSCOPE_INCLUDE_EMOJIS support
- main.go: ❌ Not passing emoji manager to scheduler

**Expected (from fallback):**
- scheduler/scheduler.go: ✅ Has emojiManager field
- scheduler/horoscope.go: ✅ Checks HOROSCOPE_INCLUDE_EMOJIS env var
- scheduler/horoscope.go: ✅ Appends custom emojis to horoscope prompt
- main.go: ✅ Passes emoji manager to scheduler.New()

**Files to modify:**
1. scheduler/scheduler.go - Add emojiManager field and parameter
2. scheduler/horoscope.go - Add emoji support to SendHoroscope()
3. main.go - Pass emojiManager to scheduler.New()

---

### 2. ⚠️ MISSING ENVIRONMENT VARIABLES (HIGH)

**Current .env.example missing:**
```
# AI Model Configuration
GROQ_MODEL="openai/gpt-oss-20b"                                # Groq chat model
GROQ_VISION_MODEL="meta-llama/llama-4-scout-17b-16e-instruct"  # Groq vision model
OLLAMA_VISION_MODEL="llava"        # Ollama vision model

# Emoji Configuration
EMOJI_CACHE_TTL_MINUTES=60         # How long to cache emoji data (minutes)
HOROSCOPE_INCLUDE_EMOJIS=true      # Include custom emojis in horoscope prompts (true/false)
```

**Note:** GROQ_MODEL, GROQ_VISION_MODEL, OLLAMA_VISION_MODEL are in code but not documented in .env.example

**Files to modify:**
1. .env.example - Add all missing environment variables

---

### 3. ⚠️ HARDCODED MODEL IN HOROSCOPE (MEDIUM)

**Current (merge/integration):**
```go
response, err := s.aiService.Chat(context.Background(), &ai.ChatRequest{
    Model:        "llama-3-70b-versatile", // Hardcoded!
```

**Expected (from fallback):**
```go
response, err := s.aiService.Chat(context.Background(), &ai.ChatRequest{
    // No Model field - uses provider's configured model
```

The model should come from the AI service configuration, not be hardcoded.

---

## CORRECT IMPLEMENTATION CHECKLIST

### M1: Fix Scheduler Emoji Support
- [ ] scheduler/scheduler.go: Add emojiManager field to Scheduler struct
- [ ] scheduler/scheduler.go: Update New() to accept emojiManager parameter
- [ ] scheduler/horoscope.go: Add HOROSCOPE_INCLUDE_EMOJIS check
- [ ] scheduler/horoscope.go: Add emoji list to horoscope instructions
- [ ] main.go: Pass emojiManager to scheduler.New()

### M2: Update .env.example
- [ ] Add GROQ_MODEL
- [ ] Add GROQ_VISION_MODEL
- [ ] Add OLLAMA_VISION_MODEL
- [ ] Add EMOJI_CACHE_TTL_MINUTES
- [ ] Add HOROSCOPE_INCLUDE_EMOJIS

### M3: Fix Hardcoded Model
- [ ] scheduler/horoscope.go: Remove hardcoded Model field
- [ ] scheduler/horoscope.go: Let AI service use configured model

---

## VERIFICATION AFTER FIXES

Run these checks after implementing:
1. `go build ./...` - Must pass
2. Check scheduler has emojiManager field
3. Check horoscope uses HOROSCOPE_INCLUDE_EMOJIS
4. Check .env.example has all variables
5. Check no hardcoded models in scheduler

---

## PRIORITY: HIGH

**These are NOT optional enhancements - they are core features from fallback that are missing.**

The scheduler emoji support is a complete feature that needs to be ported.

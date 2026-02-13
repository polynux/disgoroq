# Merge Plan: refactoring + fallback Branches

## Executive Summary

We need to merge two substantially different branches:
- **refactoring**: Contains the complete memory system with AI summarization
- **fallback**: Contains emoji management, document/GIF processing, and improved AI handling

**Status**: Both branches have diverged significantly. The fallback branch actually removed the memory system entirely.

---

## Key Differences Analysis

### 1. **Architecture Changes**

#### refactoring branch:
```
+ memory/ package (complete system)
  - Vector embeddings with Ollama
  - AI summarization
  - Message buffering
  - Context integration
+ Database schema updates (message_buffer, conversation_summaries)
+ SQLC queries for memory operations
```

#### fallback branch:
```
+ emoji/ package
  - Emoji shortcode conversion (:name: → <:name:id>)
+ ai/document_processor.go
  - Document summarization
+ ai/gif_processor.go
  - GIF analysis and description
- REMOVED: entire memory/ package
- REMOVED: database schema for memory
- MODIFIED: message handling flow
```

### 2. **Critical File Conflicts**

| File | refactoring | fallback | Strategy |
|------|-------------|----------|----------|
| `handlers/message.go` | Memory buffering + context | Emoji conversion, no memory | **Merge both** |
| `main.go` | Memory service initialization | Emoji manager initialization | **Merge both** |
| `commands/commands.go` | /forcesummary command | Prompt commands, no memory | **Merge both** |
| `ai/service.go` | Base implementation | Document/GIF processing | **Merge features** |
| `schema.sql` | Memory tables | No memory tables | **Keep both** |
| `go.mod` | Memory dependencies | Different deps | **Merge** |

### 3. **Handler Flow Differences**

**refactoring (current):**
```
1. Buffer message for memory (async)
2. Check filters (threshold, rate limit)
3. Build memory context
4. Call AI with context
5. Send response
```

**fallback:**
```
1. Check filters
2. Call AI
3. Convert emoji shortcodes
4. Send response
```

---

## Merge Strategy Options

### **Option A: Cherry-Pick Approach (RECOMMENDED)**

**Process:**
1. Keep `refactoring` as base (memory system is complex to recreate)
2. Cherry-pick specific commits from `fallback`:
   - Emoji manager functionality
   - Document/GIF processors
   - Improved prompt handling
   - Command enhancements

**Pros:**
- Preserves memory system work
- Clean history
- Selective integration

**Cons:**
- Manual work for each feature
- May miss interdependent changes

**Commands:**
```bash
# Stay on refactoring branch
git checkout refactoring

# Cherry-pick emoji functionality
git cherry-pick c864497  # emoji manager
git cherry-pick 8b256f0  # emoji integration

# Cherry-pick document/GIF processing
# (Need to identify commits)

# Cherry-pick prompt commands
git cherry-pick b2c998d
git cherry-pick c79f005
```

---

### **Option B: Feature Branch Integration**

**Process:**
1. Create new integration branch from `refactoring`
2. Manually port fallback features
3. Resolve conflicts systematically

**Steps:**
```bash
# Create integration branch
git checkout -b merge/integration refactoring

# Port emoji package
cp -r /home/polynux/dev/disgoroq-temp/emoji/ .

# Port document/GIF processors
cp /home/polynux/dev/disgoroq-temp/ai/document_processor.go ai/
cp /home/polynux/dev/disgoroq-temp/ai/gif_processor.go ai/

# Update handlers/message.go manually
# Update main.go manually
# Update commands/commands.go manually
```

---

### **Option C: Three-Way Merge with Conflict Resolution**

**Process:**
1. Attempt automatic merge
2. Resolve conflicts manually
3. Test thoroughly

**Risk Level**: HIGH
- fallback removed memory system entirely
- Automatic merge will be messy

---

## Detailed Integration Plan (Option B - Recommended)

### Phase 1: Preparation

1. **Create backup branches:**
   ```bash
   git checkout refactoring
   git branch backup/refactoring-pre-merge
   
   cd /home/polynux/dev/disgoroq-temp
   git checkout fallback
   git branch backup/fallback-pre-merge
   ```

2. **Create integration branch:**
   ```bash
   git checkout refactoring
   git checkout -b merge/integration
   ```

### Phase 2: Port Non-Conflicting Features

**A. Emoji System (from fallback):**
```bash
# Copy emoji package
cp -r /home/polynux/dev/disgoroq-temp/emoji/ .

# Update go.mod if needed
go mod tidy
```

**B. Document/GIF Processors (from fallback):**
```bash
# Copy new AI processors
cp /home/polynux/dev/disgoroq-temp/ai/document_processor.go ai/
cp /home/polynux/dev/disgoroq-temp/ai/gif_processor.go ai/

# Copy tests
cp /home/polynux/dev/disgoroq-temp/ai/document_processor_test.go ai/
cp /home/polynux/dev/disgoroq-temp/ai/gif_processor_test.go ai/
```

### Phase 3: Merge Handler Logic

**File: `handlers/message.go`**

We need BOTH:
1. Memory buffering (from refactoring)
2. Emoji conversion (from fallback)

**Proposed merged structure:**
```go
func (h *MessageHandler) Handle(s *discordgo.Session, m *discordgo.MessageCreate) {
    // 1. Memory buffering (from refactoring)
    if h.memoryService != nil && m.GuildID != "" {
        go func() { ... }()
    }
    
    // 2. Existing filter logic
    // ... (threshold checks, etc.)
    
    // 3. Build context (memory + prompts)
    instructions := GetDefaultPrompt(botMember.Nick)
    // Add memory context if available
    // Add custom prompt if set
    
    // 4. Call AI
    response := h.aiService.Chat(...)
    
    // 5. Convert emojis (from fallback)
    if h.emojiManager != nil {
        response = h.emojiManager.ConvertShortcodesToDiscordEmojis(response)
    }
    
    // 6. Send response
    s.ChannelMessageSend(m.ChannelID, response)
}
```

### Phase 4: Update main.go

**Integration:**
```go
// Initialize both systems
var memoryService memory.Service
// ... memory initialization ...

emojiManager := emoji.NewManager() // From fallback

// Pass both to handlers
messageHandler := handlers.NewMessageHandler(
    dg, 
    aiService, 
    repo, 
    memoryService,  // From refactoring
    emojiManager,    // From fallback
)
```

### Phase 5: Update Commands

**Merge command registrations:**
- Keep: /forcesummary (from refactoring)
- Add: prompt commands (from fallback)
- Add: emoji-related commands if any

### Phase 6: Database Schema

**Keep both:**
- Memory tables (from refactoring)
- Existing tables

**Note**: fallback removed memory tables, we need to keep them.

### Phase 7: Testing

1. **Test memory system:**
   - Buffer messages
   - Create summaries
   - Verify context usage

2. **Test emoji conversion:**
   - Send :name: patterns
   - Verify conversion to <:name:id>

3. **Test document/GIF processing:**
   - Upload documents
   - Upload GIFs
   - Verify AI processing

4. **Test combined flow:**
   - Memory + emoji conversion together

---

## Conflict Resolution Guide

### handlers/message.go Conflicts

**Conflict 1: Memory vs EmojiManager field**
```go
// refactoring:
memoryService memory.Service

// fallback:
emojiManager *emoji.Manager

// Resolution: Keep both
memoryService  memory.Service
emojiManager   *emoji.Manager
```

**Conflict 2: Constructor parameters**
```go
// refactoring:
func NewMessageHandler(..., memoryService memory.Service)

// fallback:
func NewMessageHandler(..., emojiManager *emoji.Manager)

// Resolution: Accept both
func NewMessageHandler(..., memoryService memory.Service, emojiManager *emoji.Manager)
```

**Conflict 3: Message buffering location**
```go
// refactoring: Buffer at start
if h.memoryService != nil { ... }

// fallback: No buffering

// Resolution: Keep buffering at start
```

**Conflict 4: Response processing**
```go
// refactoring: Memory context in prompt
instructions += memoryContext

// fallback: Emoji conversion after response
response = emojiManager.ConvertShortcodesToDiscordEmojis(response)

// Resolution: Do both
instructions += memoryContext  // If available
// ... get AI response ...
response = emojiManager.ConvertShortcodesToDiscordEmojis(response)
```

### main.go Conflicts

**Conflict: Initialization order**
```go
// refactoring: Initialize memory service
// ... 80+ lines of memory init ...

// fallback: Initialize emoji manager
emojiManager := emoji.NewManager()

// Resolution: Do both
// 1. Memory init (keep all)
// 2. Emoji init (add)
emojiManager := emoji.NewManager()

// 3. Pass both to handlers
messageHandler := handlers.NewMessageHandler(dg, aiService, repo, memoryService, emojiManager)
```

### commands/commands.go Conflicts

**Conflict: Command registrations**
```go
// refactoring: Has /forcesummary
registry.AddCommand(..., forceSummaryHandler(memoryService))

// fallback: Has prompt commands
registry.AddCommand(..., promptHandler(repo))

// Resolution: Register both
// ... existing refactoring commands ...
// Add prompt commands from fallback
```

---

## Implementation Checklist

### Pre-Merge
- [ ] Create backup branches
- [ ] Create integration branch
- [ ] Review all conflicting files

### Phase 1: Port Features
- [ ] Copy emoji/ package
- [ ] Copy document_processor.go
- [ ] Copy gif_processor.go
- [ ] Copy related tests

### Phase 2: Resolve Conflicts
- [ ] Merge handlers/message.go
- [ ] Merge main.go
- [ ] Merge commands/commands.go
- [ ] Verify go.mod

### Phase 3: Integration
- [ ] Update constructor calls
- [ ] Wire emoji manager in handlers
- [ ] Ensure both memory and emoji work

### Phase 4: Testing
- [ ] Test memory buffering
- [ ] Test summarization
- [ ] Test emoji conversion
- [ ] Test document/GIF processing
- [ ] Test combined scenarios

### Phase 5: Cleanup
- [ ] Remove debug logs
- [ ] Update documentation
- [ ] Final commit

---

## Risk Assessment

| Risk | Impact | Mitigation |
|------|--------|------------|
| Memory system breaks | HIGH | Thorough testing, keep backup |
| Emoji conversion fails | MEDIUM | Test with various shortcodes |
| Performance issues | MEDIUM | Profile after merge |
| Database conflicts | HIGH | Keep both schemas, test migrations |
| Handler logic errors | HIGH | Code review, integration tests |

---

## Recommended Next Steps

1. **Choose Option B** (Feature Branch Integration)
2. **Create integration branch** from refactoring
3. **Port emoji package** (low risk)
4. **Port document/GIF processors** (low risk)
5. **Manually merge handler** (high attention needed)
6. **Test incrementally** after each step
7. **Final review and merge** to main

**Estimated Time**: 2-4 hours of focused work
**Risk Level**: Medium (with proper testing)

---

## Questions to Resolve

1. Do we want to keep the memory system as the primary feature?
2. Should emoji conversion be optional (configurable)?
3. Are there any fallback-specific database migrations needed?
4. Which branch should be the final target (main vs refactoring)?

---

*Document created: 2026-02-13*
*Status: Planning Phase*
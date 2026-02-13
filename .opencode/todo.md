# Mission: Complete merge/integration branch

## Status
- Branch: `merge/integration` (created from `refactoring`)
- Base: `refactoring` (memory system must be preserved)
- Target: Port features from `fallback` branch
- Build status: ✅ PASSING
- Vet status: ✅ CLEAN

## M1: Fix AI Service Architecture

### T1.1: Update ai/service.go to match fallback architecture | agent:Worker
- [x] S1.1.1: Add model config fields to ServiceConfig (GroqModel, GroqVisionModel, OllamaModel, OllamaVisionModel)
- [x] S1.1.2: Update LoadServiceConfig() to read GROQ_MODEL, GROQ_VISION_MODEL, OLLAMA_MODEL, OLLAMA_VISION_MODEL env vars
- [x] S1.1.3: Update NewService() to wrap each provider individually with NewRetryWrapper(provider, config, chatModel, visionModel)
- [x] S1.1.4: Add chain field to Service struct to store provider chain reference
- [x] S1.1.5: Update IsFallbackAvailable() to use stored chain reference
- [x] S1.1.6: Verify build passes

### T1.2: Fix test files | agent:Worker
- [x] S1.2.1: Fix ai/retry_test.go - Update NewRetryWrapper calls to include model parameters
- [x] S1.2.2: Fix memory/service_test.go - Update mockSummarizer to return *Summarizer

## M2: Integration Verification

### T2.1: Memory system intact | status:verified
- [x] S2.1.1: memory/service.go exists
- [x] S2.1.2: memory/repository.go exists
- [x] S2.1.3: memory/embeddings.go exists
- [x] S2.1.4: memory/summarizer.go exists
- [x] S2.1.5: memory/types.go exists

### T2.2: Emoji system in place | status:verified
- [x] S2.2.1: emoji/manager.go exists

### T2.3: Document/GIF processors in place | status:verified
- [x] S2.3.1: ai/document_processor.go exists
- [x] S2.3.2: ai/gif_processor.go exists

## M3: Final Verification (depends:M1,M2)

### T3.1: Full system test | agent:Reviewer
- [x] S3.1.1: Run go build ./... successfully
- [x] S3.1.2: Fix go vet warnings in test files
- [x] S3.1.3: Verify no import errors or missing dependencies

### T3.2: Commit changes | agent:Worker
- [ ] S3.2.1: Stage all modified files
- [ ] S3.2.2: Commit with descriptive message
- [ ] S3.2.3: Push to merge/integration branch

### T3.3: Documentation update | agent:Worker (optional)
- [ ] S3.3.1: Update AGENTS.md with new environment variables
- [ ] S3.3.2: Document architecture changes

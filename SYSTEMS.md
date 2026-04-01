# Systems

This document is intentionally high level. It describes logic flow and communication for the memory and voice systems without going into implementation detail.

## Memory

### Purpose

Memory lets the bot carry forward useful user-specific context inside a guild without replaying the entire raw conversation every time.

### Flow

1. A guild message arrives.
2. The message is buffered for memory work in the background.
3. Once enough buffered messages exist, the service may summarize them.
4. The summary is stored and older buffered entries can be cleared.
5. When the bot prepares a reply, it asks memory for relevant context.
6. Memory returns the best summaries and a confidence signal.
7. That context is appended to the AI request.

### Communication

- `handlers/message.go` sends message content into the memory service.
- The memory service talks to the embedding provider to build semantic search inputs.
- The memory service talks to the summarizer to compress buffered conversation.
- The memory service talks to its repository layer to store and retrieve buffered messages and summaries.
- The AI request consumes memory output as extra prompt context.

### Boundaries

- Memory is scoped by guild and user.
- Memory is advisory context, not a hard source of truth.
- If embeddings or summarization are unavailable, the bot should still continue operating with degraded context.

## Voice

### Purpose

Voice lets the bot join a Discord voice channel, listen for speech, transcribe it, generate a reply, and play that reply back as speech.

### Flow

1. The bot joins a voice channel.
2. Incoming voice packets are received from Discord.
3. Audio is buffered while voice activity is detected.
4. When speech ends or enough buffered audio exists, the audio is transcribed.
5. The transcription is sent through the same conversational AI flow.
6. The response text is converted to speech.
7. Audio is streamed back into the Discord voice connection.
8. The session returns to listening.
9. If the channel becomes empty, the bot leaves.

### Communication

- Discord voice traffic enters through the voice manager.
- The manager forwards decoded audio into the orchestrator.
- The orchestrator sends speech audio to the STT service.
- The orchestrator sends text prompts to the AI service.
- The orchestrator sends reply text to the TTS service.
- The manager sends synthesized audio back to Discord.
- Voice can also use the memory service to add conversational context before generating the reply.

### Session Rules

- A guild can only have one active voice session at a time.
- Voice responses depend on both global config and per-guild enablement.
- Text fallback may be used when speech generation or playback cannot complete.
- Voice state changes are event-driven but silence detection also relies on elapsed wall-clock time because Discord does not continuously emit silence packets.

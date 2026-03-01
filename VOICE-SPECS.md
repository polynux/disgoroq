## 1. Hardware Constraints & Resource Budget

**Target Hardware**: NVIDIA GTX 1060 6GB GDDR5
- **Compute Capability**: 6.1 (Pascal)
- **VRAM**: 6 GB (effective ~5.8 GB available after system reservation)
- **CUDA Cores**: 1280
- **TDP**: 120W

**VRAM Allocation Strategy**:
| Component | Memory Footprint | Persistence | Notes |
|-----------|-----------------|-------------|-------|
| **Ollama (Phi-4 4B Q4_K_M)** | ~3.3 GB | Resident | Primary LLM for reasoning |
| **Ollama (nomic-embed-text)** | ~0.3 GB | Resident | Embedding model for RAG/memory |
| **Qwen3-TTS 0.6B (BF16)** | ~1.2 GB | On-demand | Voice synthesis |
| **CUDA Context + Overhead** | ~0.5 GB | Fixed | PyTorch/Transformers overhead |
| **Safety Buffer** | ~0.5 GB | - | Prevent OOM during generation |
| **Total Active** | ~5.8 GB | - | Fits within 6GB constraint |

**Critical Constraint**: Only one GPU-intensive service (TTS or LLM inference) can actively allocate large buffers simultaneously. The 0.6B model choice leaves sufficient headroom to prevent `CUDA_OUT_OF_MEMORY` during peak activation allocation.

---

## 2. Model Selection & Specifications

### 2.1 Text-to-Speech (TTS)
**Model**: `Qwen/Qwen3-TTS-12Hz-0.6B-Base`
- **Architecture**: Qwen3-based decoder-only transformer with audio codec
- **Parameters**: 0.6 Billion (600M)
- **Sampling Rate**: 12kHz (optimized for voice bandwidth)
- **Quantization**: Brain Float 16 (BF16)
- **VRAM Usage**: ~1.2 GB (weights) + ~200MB (activations) = **~1.4 GB peak**
- **Inference Speed**: ~3-5 seconds for 10-second audio on GTX 1060
- **Voice Cloning**: Zero-shot via reference audio + transcript
- **Languages**: Multilingual (French, English, German, etc.)

**Why 0.6B over 1.7B**:
- 60% less VRAM (1.2GB vs 3.9GB)
- 3x faster inference on constrained hardware
- Quality difference negligible for Discord voice chat (8kHz Opus compression masks differences)
- Enables simultaneous LLM + TTS residency without swapping

### 2.2 Speech-to-Text (STT)
**Model**: `whisper.cpp` with `tiny` or `base` model
- **Implementation**: C++ inference (not Python Transformers)
- **Device**: CPU (Intel/AMD x86_64)
- **Model Size**: 39MB (tiny) / 74MB (base)
- **Memory**: System RAM only (~100MB)
- **Speed**: Real-time factor (RTF) 0.3-0.5 on modern CPU
- **Language**: French (auto-detect or forced)
- **Format**: Opus → PCM → Whisper

**Rationale for CPU**: Avoids GPU context switching overhead and saves 2GB VRAM that would be needed for Whisper on CUDA.

### 2.3 Language Model (LLM)
**Model**: Phi-4 Mini Instruct (4B parameters, Q4_K_M quantization)
- **Context Window**: 128K tokens (practically 8K for agent memory)
- **VRAM**: ~3.3 GB via Ollama
- **Quantization**: Q4_K_M (4-bit with importance matrix)
- **Embedding**: nomic-embed-text (274MB) for vector memory

---

## 3. System Architecture

### 3.1 Component Diagram

```
┌─────────────────────────────────────────────────────────────────┐
│                         Discord Bot (Go)                         │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────────────┐  │
│  │ Gateway      │  │ Orchestrator │  │ Memory System        │  │
│  │ Handler      │◄─┤ (VRAM Mgmt)  │◄─┤ (ChromaDB + Ollama)  │  │
│  └──────┬───────┘  └──────┬───────┘  └──────────────────────┘  │
└─────────┼─────────────────┼─────────────────────────────────────┘
          │                 │
          │ Voice Data      │ HTTP/JSON
          │                 │
┌─────────▼─────────────────▼─────────────────────────────────────┐
│                    Python Audio Service                          │
│  ┌─────────────────────────────────────────────────────────┐   │
│  │  FastAPI Server (Port 8880)                              │   │
│  │  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐   │   │
│  │  │ TTS Endpoint │  │ VRAM Monitor │  │ Health Check │   │   │
│  │  │ /tts/stream  │  │ /vram        │  │ /health      │   │   │
│  │  └──────┬───────┘  └──────────────┘  └──────────────┘   │   │
│  │         │                                               │   │
│  │  ┌──────▼───────┐  ┌──────────────────────────────┐    │   │
│  │  │ Qwen3 0.6B   │  │ Voice Cloning Pipeline       │    │   │
│  │  │ (BF16)       │  │ • Reference Audio Storage    │    │   │
│  │  │ Lazy Loaded  │  │ • Spectrogram Extraction     │    │   │
│  │  └──────────────┘  │ • Speaker Embedding Cache    │    │   │
│  │                    └──────────────────────────────┘    │   │
│  └─────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────┘
          │
          │ Unix Socket / Shared Memory
          │
┌─────────▼───────────────────────────────────────────────────────┐
│  whisper.cpp (CPU)                                               │
│  • Opus Decoder                                                  │
│  • VAD (Voice Activity Detection)                                │
│  • Real-time Transcription                                       │
└─────────────────────────────────────────────────────────────────┘
```

### 3.2 Communication Protocols

**Go Bot ↔ Python TTS**:
- **Protocol**: HTTP/1.1 (Keep-Alive for connection pooling)
- **Content-Type**: `application/json` (requests), `audio/wav` (responses)
- **Streaming**: Chunked Transfer-Encoding for TTS generation
- **Endpoints**:
  - `POST /tts/stream` - Generate speech with chunked response
  - `POST /load` - Preload model into VRAM
  - `POST /unload` - Release VRAM
  - `GET /vram` - Query available GPU memory

**Go Bot ↔ whisper.cpp**:
- **Protocol**: Unix Domain Socket (UDS) or gRPC
- **Data Format**: Raw PCM 16-bit 16kHz mono
- **Chunk Size**: 100ms audio frames for low latency

**Internal Go Communication**:
- **Orchestrator**: Manages state machine (Idle → Listening → Thinking → Speaking)
- **Memory**: BadgerDB or SQLite for conversation history, ChromaDB for embeddings

---

## 4. VRAM Management Strategy

### 4.1 State Machine

```go
type AgentState int

const (
    StateIdle AgentState = iota
    StateListening      // STT active (CPU), TTS unloaded
    StateThinking       // LLM inference (GPU), TTS unloaded
    StateSpeaking       // TTS inference (GPU), LLM cached
)

type VRAMManager struct {
    ttsLoaded      bool
    ollamaClient   *OllamaClient
    ttsClient      *TTSClient
    minFreeVRAM    float64 // 1500 MB
}
```

### 4.2 Lifecycle Management

**Voice Channel Join**:
1. Check VRAM: `nvidia-smi query` → require >1.5GB free
2. Send `POST /load` to TTS service (warmup)
3. Start whisper.cpp listener (CPU)
4. Transition to `StateListening`

**Voice Activity Detected**:
1. Accumulate audio buffer (500ms-1s)
2. Send to whisper.cpp → text
3. Transition to `StateThinking`
4. (Optional) `POST /unload` TTS if LLM needs more VRAM for complex reasoning
5. Query Ollama with context + memory

**Response Generation**:
1. Receive LLM response (text)
2. If TTS unloaded: `POST /load` (takes ~2s on GTX 1060)
3. Stream text to `POST /tts/stream`
4. Receive WAV chunks, encode to Opus, send to Discord
5. Transition to `StateListening`

**Voice Channel Leave**:
1. `POST /unload` to TTS service
2. Terminate whisper.cpp
3. Force CUDA cache clear: `torch.cuda.empty_cache()`
4. Transition to `StateIdle`

### 4.3 Memory Pressure Handling

**OOM Prevention**:
- Pre-flight check: Ensure 1.5GB free before loading TTS
- Fallback: If generation fails with OOM, unload TTS, retry with text-only response
- LRU Cache: Keep only last 3 speaker embeddings in VRAM (others on disk)

---

## 5. Voice Cloning Implementation

### 5.1 Reference Audio Pipeline

**Storage**:
- **Location**: `./voices/{user_id}/` persistent volume
- **Format**: WAV 16-bit 22kHz+ (converted to 12kHz on load)
- **Reference Text**: Stored in `{voice_name}.txt` alongside audio

**Preprocessing** (One-time per voice):
```python
# Extract speaker embedding from reference
# Cache to disk as .pt tensor (~50KB per voice)
speaker_embedding = model.extract_speaker(ref_audio, ref_text)
torch.save(speaker_embedding, f"./voices/{user_id}/embedding.pt")
```

**Runtime**:
1. Load cached embedding into GPU memory (not full model re-inference)
2. Generate audio with `generate_voice_clone()` using cached embedding
3. This reduces per-generation latency by ~500ms

### 5.2 Audio Streaming Protocol

**Chunked Generation**:
- Split long responses by sentence boundaries (`[.!?]`)
- Generate each sentence independently
- Stream via HTTP chunked encoding
- Client (Go) receives chunks and immediately encodes to Opus

**Latency Budget**:
- TTS First Byte: ~500ms (model warmup) + ~100ms per sentence
- Opus Encode: ~5ms per 20ms frame
- Discord Network: ~50-100ms
- **Total Time-to-Speech**: ~800ms-1.2s for first audio chunk

---

## 6. Deployment Specification

### 6.1 Docker Configuration

**TTS Service** (`Dockerfile`):
```dockerfile
FROM nvidia/cuda:12.1-runtime-ubuntu22.04
RUN pip install torch==2.5.1+cu121 transformers==4.48.0 qwen-tts==0.1.0 fastapi uvicorn
ENV CUDA_VISIBLE_DEVICES=0
ENV PYTORCH_CUDA_ALLOC_CONF="expandable_segments:True,max_split_size_mb:128"
EXPOSE 8880
CMD ["python", "tts_server.py"]
```

**Resource Limits**:
```yaml
deploy:
  resources:
    limits:
      memory: 3G
    reservations:
      devices:
        - driver: nvidia
          count: 1
          capabilities: [gpu]
```

### 6.2 File Structure

```
project/
├── bot/
│   ├── main.go                 # Discord bot entry
│   ├── orchestrator.go         # VRAM state machine
│   ├── tts_client.go           # HTTP client for TTS
│   └── stt/
│       └── whisper_client.go   # Unix socket client
├── tts-service/
│   ├── tts_server.py           # FastAPI app
│   ├── model.py                # Qwen3 wrapper
│   └── voices/                 # Reference audio storage
│       └── mohammed/
│           ├── reference.wav
│           ├── ref.txt
│           └── embedding.pt    # Cached speaker embedding
├── whisper/
│   └── whisper.cpp/            # Compiled binary + models
└── docker-compose.yml
```

### 6.3 Environment Variables

```bash
# TTS Service
TTS_MODEL=Qwen/Qwen3-TTS-12Hz-0.6B-Base
TTS_DEVICE=cuda:0
TTS_DTYPE=bfloat16
TTS_PORT=8880
TTS_UNLOAD_TIMEOUT=60

# Go Bot
OLLAMA_HOST=http://localhost:11434
TTS_ENDPOINT=http://localhost:8880
WHISPER_SOCKET=/tmp/whisper.sock
DISCORD_TOKEN=${DISCORD_TOKEN}
```

---

## 7. Performance Characteristics

**GTX 1060 6GB Benchmarks** (Expected):

| Operation | Latency | VRAM Delta |
|-----------|---------|------------|
| TTS Load (0.6B BF16) | 2.1s | +1.2 GB |
| TTS Unload | 0.3s | -1.1 GB |
| 10s Audio Generation | 3.5s | +0.2 GB (temp) |
| Whisper (CPU) 5s audio | 1.2s | 0 GB |
| Ollama 4B inference | 2-5 tok/s | +0.1 GB (activations) |

**Concurrent Limitations**:
- Cannot run TTS generation while Ollama is processing >2K context (spikes VRAM)
- Solution: Sequential processing with state machine (Thinking → Speaking, never simultaneous GPU-heavy ops)

---

## 8. Fallback Strategies

**If TTS OOM**:
1. Catch `RuntimeError: CUDA out of memory`
2. Unload TTS model immediately
3. Send text response to Discord chat instead of voice
4. Log incident for VRAM tuning

**If STT Misses**:
- VAD sensitivity tuning (discordgo voice receive threshold)
- Fallback to push-to-talk mode for noisy environments

**Network Latency**:
- Localhost communication between Go and Python (<1ms)
- If TTS service remote: WebSocket binary streaming instead of HTTP


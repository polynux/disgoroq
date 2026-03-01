# Qwen3-TTS Service for DisgoroQ

This service provides OpenAI-compatible TTS endpoints for the DisgoroQ Discord bot, powered by Qwen3-TTS with voice cloning support.

## Features

- **OpenAI-Compatible API**: Drop-in replacement for OpenAI TTS API
- **Voice Cloning**: Clone voices from reference audio files
- **Multi-language Support**: French, English, Chinese, Japanese, Korean, and more
- **Multiple Audio Formats**: WAV, MP3, Opus, AAC, FLAC, PCM
- **GPU Acceleration**: CUDA 12.1 support for fast inference

## Quick Start

### Using Docker (Recommended)

```bash
# Start the TTS service
docker-compose up -d tts-service

# Check health
curl http://localhost:8880/health

# Generate speech
curl -X POST http://localhost:8880/v1/audio/speech \
  -H "Content-Type: application/json" \
  -d '{"model":"qwen3-tts","voice":"Vivian","input":"Bonjour, comment ça va?"}' \
  --output output.wav
```

### Endpoints

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/health` | GET | Health check with device info |
| `/vram` | GET | GPU VRAM status |
| `/load` | POST | Load model into VRAM |
| `/unload` | POST | Unload model from VRAM |
| `/v1/models` | GET | List available models |
| `/v1/audio/voices` | GET | List available voices |
| `/v1/audio/speech` | POST | Generate speech from text |
| `/v1/audio/voice-clone` | POST | Clone voice from reference audio |
| `/v1/audio/voice-clone/capabilities` | GET | Check voice cloning support |

### Available Voices

Built-in voices (CustomVoice model):
- Vivian (female, English)
- Ryan (male, English)
- Sophia (female, English)
- Isabella (female, English)
- Evan (male, English)
- Lily (female, English)

OpenAI-compatible aliases:
- alloy → Vivian
- echo → Ryan
- fable → Sophia
- nova → Isabella
- onyx → Evan
- shimmer → Lily

## Voice Cloning

For voice cloning, you need the **Base** model instead of CustomVoice:

```bash
# Set environment variable
export TTS_MODEL=Qwen/Qwen3-TTS-12Hz-1.7B-Base
```

### Creating Custom Voices

1. Create a directory for your voice:
```bash
mkdir -p voices/my-voice
```

2. Add reference audio (5-30 seconds recommended):
```bash
cp /path/to/your/voice.wav voices/my-voice/reference.wav
```

3. Add transcript (required for best quality):
```bash
echo "This is a transcript of the reference audio." > voices/my-voice/reference.txt
```

4. Use the custom voice:
```bash
curl -X POST http://localhost:8880/v1/audio/speech \
  -H "Content-Type: application/json" \
  -d '{"model":"qwen3-tts","voice":"my-voice","input":"Test message!"}' \
  --output output.wav
```

### Voice Cloning via API

```bash
# Base64 encode your audio
AUDIO_BASE64=$(base64 -w0 reference.wav)

# Clone voice and generate speech
curl -X POST http://localhost:8880/v1/audio/voice-clone \
  -H "Content-Type: application/json" \
  -d "{
    \"input\": \"Your text to speak\",
    \"ref_audio\": \"$AUDIO_BASE64\",
    \"ref_text\": \"Transcript of reference audio\",
    \"language\": \"French\",
    \"response_format\": \"wav\"
  }" \
  --output output.wav
```

## Configuration

Environment variables:

| Variable | Default | Description |
|----------|---------|-------------|
| `TTS_MODEL` | Qwen/Qwen3-TTS-12Hz-0.6B-Base | Model to use |
| `TTS_BACKEND` | official | Backend type |
| `TTS_PORT` | 8880 | Server port |
| `TTS_HOST` | 0.0.0.0 | Server host |
| `VOICE_LIBRARY_DIR` | /app/voices | Custom voices directory |
| `TTS_WARMUP_ON_START` | false | Pre-load model on startup |
| `CUDA_VISIBLE_DEVICES` | 0 | GPU device ID |

## Model Selection

- **Qwen/Qwen3-TTS-12Hz-0.6B-Base**: Smallest, fastest, supports voice cloning
- **Qwen/Qwen3-TTS-12Hz-1.7B-Base**: Better quality, supports voice cloning
- **Qwen/Qwen3-TTS-12Hz-0.6B-CustomVoice**: Pre-built voices, no cloning
- **Qwen/Qwen3-TTS-12Hz-1.7B-CustomVoice**: Best built-in voices, no cloning

## VRAM Requirements

| Model | VRAM Required |
|-------|----------------|
| 0.6B Base | ~1.5 GB |
| 0.6B CustomVoice | ~1.2 GB |
| 1.7B Base | ~3.5 GB |
| 1.7B CustomVoice | ~3.0 GB |

## Integration with DisgoroQ

The Go bot communicates with this service via HTTP:

```go
// Create TTS client
ttsClient := voice.NewTTSHTTPClient(voice.TTSHTTPConfig{
    Endpoint:     "http://localhost:8880",
    Model:        "qwen3-tts",
    DefaultVoice: "Vivian",
    SampleRate:   12000,
    TimeoutMs:    30000,
})

// Generate speech
stream, err := ttsClient.Stream(ctx, &voice.TTSRequest{
    Text:    "Bonjour!",
    VoiceID: "Vivian",
})
```

## License

This service uses the Qwen3-TTS model. See the model license for terms of use.
# Custom Voices Directory

This directory stores custom voice references for voice cloning with Qwen3-TTS.

## Directory Structure

The service loads a **single voice** from a subdirectory (default: `default/`):

```
voices/
└── default/              # Voice directory name (configurable via VOICE_NAME)
    ├── reference.wav     # Reference audio (REQUIRED)
    └── reference.txt     # Transcript of the reference audio (REQUIRED for ICL mode)
```

## Voice Cloning Requirements

- **Model**: Voice cloning requires the **Base** model
  - `Qwen/Qwen3-TTS-12Hz-0.6B-Base` (smaller, faster)
  - `Qwen/Qwen3-TTS-12Hz-1.7B-Base` (larger, better quality)
- **Reference Audio**: 10-30 seconds of clear speech in the target voice
- **Reference Text**: **REQUIRED** - transcript of the reference audio (ICL mode only)

## ICL Mode (In-Context Learning)

The service uses **ICL mode exclusively**, which requires a transcript of the reference audio. This provides better voice cloning quality compared to x-vector only mode.

## Supported Audio Formats

- WAV (recommended)
- MP3
- M4A
- FLAC
- OGG

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `VOICE_LIBRARY_DIR` | `/app/voices` | Directory containing voice subdirectories |
| `VOICE_NAME` | `default` | Name of the voice subdirectory to load |
| `TTS_DEFAULT_LANGUAGE` | `French` | Default language for synthesis |

## Tips for Best Results

1. **Audio Quality**: Use clean audio without background noise
2. **Duration**: 10-30 seconds is optimal
3. **Transcript**: Always provide an accurate transcript
4. **Language**: Match the reference audio language to your target language
5. **Consistent Voice**: Use a single speaker in the reference audio

## Example Configuration

```bash
# Docker compose environment
environment:
  - TTS_MODEL=Qwen/Qwen3-TTS-12Hz-1.7B-Base
  - VOICE_NAME=default
  - TTS_DEFAULT_LANGUAGE=French
```

## API Usage

Once the voice is loaded, use the `/v1/tts/generate` endpoint:

```bash
curl -X POST http://localhost:8880/v1/tts/generate \
  -H "Content-Type: application/json" \
  -d '{"input":"Bonjour, comment allez-vous?"}' \
  --output output.wav
```
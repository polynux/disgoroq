# Custom Voices Directory

This directory is for storing custom voice references for voice cloning with Qwen3-TTS.

## Directory Structure

For each custom voice, create a directory with:

```
voices/
├── my-voice-name/
│   ├── reference.wav    # Reference audio (WAV, MP3, M4A, FLAC, or OGG)
│   └── reference.txt     # Transcript of the reference audio (optional)
├── another-voice/
│   ├── reference.mp3
│   └── reference.txt
```

## Voice Cloning Requirements

- **Model**: Voice cloning requires the **Base** model (Qwen/Qwen3-TTS-12Hz-1.7B-Base)
  - The CustomVoice model does NOT support voice cloning
- **Reference Audio**: 10-30 seconds of clear speech in the target voice
- **Reference Text**: Transcript of the reference audio for best quality (ICL mode)
- **Without Transcript**: The system can use x-vector mode (lower quality but works without transcript)

## Supported Audio Formats

- WAV (recommended)
- MP3
- M4A
- FLAC
- OGG

## Tips for Best Results

1. **Audio Quality**: Use clean audio without background noise
2. **Duration**: 10-30 seconds is optimal
3. **Transcript**: Always provide a transcript for best quality
4. **Language**: Match the reference audio language to your target language
5. **Consistent Voice**: Use a single speaker in the reference audio

## Example

For a French voice named "jean":
```
voices/
└── jean/
    ├── reference.wav     # 20 seconds of Jean speaking French
    └── reference.txt     # "Bonjour, je m'appelle Jean et je suis content de vous rencontrer..."
```

## Using Custom Voices with the Bot

Set the `TTS_MODEL` environment variable to the Base model:
```yaml
environment:
  - TTS_MODEL=Qwen/Qwen3-TTS-12Hz-1.7B-Base
```

Then reference the voice by name in the bot configuration or API calls.
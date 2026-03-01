# Whisper.cpp Speech-to-Text Service

This folder contains everything needed to run whisper.cpp as a speech-to-text service for the DisgoroQ Discord bot.

## Quick Start

### Option 1: Run Locally (Recommended for Performance)

```bash
# 1. Setup whisper.cpp
cd whisper
./setup.sh -m tiny

# 2. Start the server
./whisper_server.sh -m tiny -l fr -s /tmp/whisper.sock
```

### Option 2: Docker

```bash
# Build and run
docker-compose --profile voice up -d whisper

# Check logs
docker-compose logs -f whisper
```

## Files

| File | Purpose |
|------|---------|
| `setup.sh` | Downloads and compiles whisper.cpp |
| `whisper_server.sh` | Starts whisper.cpp with Unix socket |
| `whisper_server.py` | Python wrapper with better Unix socket handling |
| `Dockerfile` | Container image for whisper service |

## Setup Script Options

```bash
./setup.sh [options]

Options:
  -m, --models      Comma-separated models (tiny, base, small, medium, large)
  -c, --cuda        Build with CUDA support
  -o, --openblas    Build with OpenBLAS support
  -h, --help        Show help
```

### Examples

```bash
# Default (tiny model, CPU only)
./setup.sh

# Download multiple models
./setup.sh -m tiny,base,small

# Build with CUDA support
./setup.sh -c -m base
```

## Server Script Options

```bash
./whisper_server.sh [options]

Options:
  -m, --model      Model to use (tiny, base, small, medium) [default: tiny]
  -l, --language   Language code (fr, en, de, etc.) [default: fr]
  -s, --socket     Unix socket path [default: /tmp/whisper.sock]
  -t, --threads    Number of threads [default: 4]
  -h, --help       Show help
```

## API Protocol

The server uses a simple binary protocol over Unix sockets:

### Request Format

```
[4 bytes: length (big-endian)] + [JSON payload]
```

### Request JSON

```json
{
  "command": "transcribe",
  "audio": [/* PCM 16-bit audio bytes */],
  "language": "fr",
  "sample_rate": 16000,
  "channels": 1
}
```

### Response JSON

```json
{
  "text": "Bonjour, comment ça va?",
  "language": "fr",
  "confidence": 0.92,
  "duration": 2.5
}
```

### Commands

| Command | Description |
|---------|-------------|
| `transcribe` | Transcribe audio data |
| `health` | Health check |
| `start` | Start streaming session |
| `stop` | Stop streaming and get transcription |
| `reset` | Reset session state |

## Model Selection

| Model | VRAM/RAM | Speed | Quality |
|-------|----------|-------|---------|
| tiny | ~1 GB | Fastest | Good |
| base | ~1.5 GB | Fast | Better |
| small | ~2.5 GB | Medium | Good |
| medium | ~5 GB | Slow | Best |

For the GTX 1060 6GB, **tiny** or **base** models are recommended.

## Go Client Usage

```go
import "polynux/disgoroq/voice"

// Create STT client
sttClient := voice.NewWhisperClient(voice.WhisperConfig{
    SocketPath: "/tmp/whisper.sock",
    Model:      "tiny",
    Language:   "fr",
})

// Transcribe audio
text, err := sttClient.Transcribe(ctx, audioData)
```

## Performance Tips

1. **Use tiny model** for fastest transcription (good enough for most use cases)
2. **Set threads** to match your CPU cores
3. **Use GPU** if available (CUDA build)
4. **Buffer audio** in 500ms-1s chunks for better results

## Troubleshooting

### Socket already exists

```bash
rm -f /tmp/whisper.sock
```

### Model not found

```bash
./setup.sh -m tiny
```

### Permission denied on socket

```bash
chmod 666 /tmp/whisper.sock
```

### Build fails

```bash
# Install build dependencies
sudo apt-get install build-essential git

# Try clean build
cd whisper.cpp
make clean
make
```

## License

Whisper.cpp is MIT licensed. See https://github.com/ggerganov/whisper.cpp
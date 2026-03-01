#!/bin/bash
# =============================================================================
# Whisper.cpp Unix Socket Server Launcher
# =============================================================================
# This script starts whisper.cpp with a Unix socket interface for real-time
# speech-to-text transcription.
#
# Usage:
#   ./whisper_server.sh [options]
#
# Options:
#   -m, --model      Model to use (tiny, base, small, medium) [default: tiny]
#   -l, --language   Language code (fr, en, de, etc.) [default: fr]
#   -s, --socket     Unix socket path [default: /tmp/whisper.sock]
#   -t, --threads    Number of threads [default: 4]
#   -p, --processors Number of processors [default: 1]
#   -h, --help       Show this help message
# =============================================================================

set -e

# Default values
MODEL="tiny"
LANGUAGE="fr"
SOCKET_PATH="/tmp/whisper.sock"
THREADS=4
PROCESSORS=1
WHISPER_BIN="./main"
MODELS_DIR="./models"

# Parse arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        -m|--model)
            MODEL="$2"
            shift 2
            ;;
        -l|--language)
            LANGUAGE="$2"
            shift 2
            ;;
        -s|--socket)
            SOCKET_PATH="$2"
            shift 2
            ;;
        -t|--threads)
            THREADS="$2"
            shift 2
            ;;
        -p|--processors)
            PROCESSORS="$2"
            shift 2
            ;;
        -h|--help)
            head -20 "$0" | tail -18
            exit 0
            ;;
        *)
            echo "Unknown option: $1"
            exit 1
            ;;
    esac
done

# Check if whisper binary exists
if [[ ! -x "$WHISPER_BIN" ]]; then
    echo "Error: Whisper binary not found at $WHISPER_BIN"
    echo "Please run ./setup.sh first to download and compile whisper.cpp"
    exit 1
fi

# Check if model exists
MODEL_PATH="$MODELS_DIR/ggml-$MODEL.bin"
if [[ ! -f "$MODEL_PATH" ]]; then
    echo "Error: Model not found at $MODEL_PATH"
    echo "Available models:"
    ls -la "$MODELS_DIR"/ggml-*.bin 2>/dev/null || echo "  No models found. Run ./setup.sh to download."
    exit 1
fi

# Remove existing socket
if [[ -S "$SOCKET_PATH" ]]; then
    echo "Removing existing socket: $SOCKET_PATH"
    rm -f "$SOCKET_PATH"
fi

# Create socket directory if needed
SOCKET_DIR=$(dirname "$SOCKET_PATH")
if [[ "$SOCKET_DIR" != "." && "$SOCKET_DIR" != "/" ]]; then
    mkdir -p "$SOCKET_DIR"
fi

echo "=============================================="
echo "  Whisper.cpp Unix Socket Server"
echo "=============================================="
echo "  Model:    $MODEL ($MODEL_PATH)"
echo "  Language: $LANGUAGE"
echo "  Socket:   $SOCKET_PATH"
echo "  Threads:  $THREADS"
echo "=============================================="
echo ""

# Start whisper.cpp with Unix socket
# Note: whisper.cpp native Unix socket support may vary by version
# This is a placeholder for the command - actual implementation may differ

exec "$WHISPER_BIN" \
    -m "$MODEL_PATH" \
    -l "$LANGUAGE" \
    -t "$THREADS" \
    -p "$PROCESSORS" \
    --socket "$SOCKET_PATH" \
    --no-prints \
    2>&1 | while IFS= read -r line; do
        echo "[$(date '+%Y-%m-%d %H:%M:%S')] $line"
    done
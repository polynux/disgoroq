#!/bin/bash
# =============================================================================
# Whisper.cpp Setup Script
# =============================================================================
# Downloads and compiles whisper.cpp for the DisgoroQ voice chat feature.
#
# Usage:
#   ./setup.sh [options]
#
# Options:
#   -m, --models      Comma-separated list of models to download [default: tiny]
#   -c, --cuda        Build with CUDA support
#   -o, --openblas    Build with OpenBLAS support
#   -h, --help        Show this help message
#
# Models: tiny, base, small, medium, large
# =============================================================================

set -e

# Default values
MODELS="tiny"
CUDA=false
OPENBLAS=false
WHISPER_DIR="whisper.cpp"
BUILD_DIR="build"

# Parse arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        -m|--models)
            MODELS="$2"
            shift 2
            ;;
        -c|--cuda)
            CUDA=true
            shift
            ;;
        -o|--openblas)
            OPENBLAS=true
            shift
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

echo "=============================================="
echo "  Whisper.cpp Setup"
echo "=============================================="
echo "  Models:   $MODELS"
echo "  CUDA:     $CUDA"
echo "  OpenBLAS: $OPENBLAS"
echo "=============================================="

# Get script directory
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

# Clone whisper.cpp if not exists
if [[ ! -d "$WHISPER_DIR" ]]; then
    echo ""
    echo "Cloning whisper.cpp..."
    git clone https://github.com/ggerganov/whisper.cpp.git "$WHISPER_DIR"
else
    echo ""
    echo "whisper.cpp already cloned, updating..."
    cd "$WHISPER_DIR"
    git pull
    cd ..
fi

cd "$WHISPER_DIR"

# Build whisper.cpp
echo ""
echo "Building whisper.cpp..."
mkdir -p "$BUILD_DIR"
cd "$BUILD_DIR"

BUILD_ARGS=""
if [[ "$CUDA" == "true" ]]; then
    echo "Enabling CUDA support..."
    BUILD_ARGS="-DWHISPER_CUBLAS=ON"
fi
if [[ "$OPENBLAS" == "true" ]]; then
    echo "Enabling OpenBLAS support..."
    BUILD_ARGS="$BUILD_ARGS -DWHISPER_OPENBLAS=ON"
fi

cmake .. $BUILD_ARGS -DCMAKE_BUILD_TYPE=Release
make -j$(nproc)

# Copy binary
cp bin/main ../main
echo "Binary built: $(pwd)/../main"

cd ..

# Download models
echo ""
echo "Downloading models..."
mkdir -p models

IFS=',' read -ra MODEL_ARRAY <<< "$MODELS"
for model in "${MODEL_ARRAY[@]}"; do
    model=$(echo "$model" | xargs)  # Trim whitespace
    MODEL_FILE="ggml-$model.bin"
    MODEL_PATH="models/$MODEL_FILE"

    if [[ -f "$MODEL_PATH" ]]; then
        echo "  ✓ $model (already exists)"
    else
        echo "  Downloading $model..."
        bash ./models/download-ggml-model.sh "$model"
    fi
done

# Create symlinks in parent directory
echo ""
echo "Creating symlinks..."
ln -sf "$WHISPER_DIR/main" ./main 2>/dev/null || true
ln -sf "$WHISPER_DIR/models" ./models 2>/dev/null || true

echo ""
echo "=============================================="
echo "  Setup Complete!"
echo "=============================================="
echo ""
echo "Binaries:"
echo "  - $(pwd)/main"
echo ""
echo "Models:"
ls -lh models/*.bin 2>/dev/null || echo "  No models downloaded"
echo ""
echo "To start the server:"
echo "  ./whisper_server.sh -m tiny -l fr -s /tmp/whisper.sock"
echo ""
echo "Or use the Python wrapper:"
echo "  python3 whisper_server.py"
echo ""
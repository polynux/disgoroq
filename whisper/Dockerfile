# =============================================================================
# Whisper.cpp Docker Image for DisgoroQ STT
# =============================================================================
# Lightweight container for speech-to-text using whisper.cpp
# =============================================================================

FROM ubuntu:22.04 AS builder

ENV DEBIAN_FRONTEND=noninteractive

# Install build dependencies
RUN apt-get update && apt-get install -y --no-install-recommends \
    build-essential \
    cmake \
    git \
    wget \
    curl \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /build

# Clone whisper.cpp
RUN git clone https://github.com/ggerganov/whisper.cpp.git .

# Build with cmake
ARG MODEL=tiny
RUN mkdir -p build && cd build && \
    cmake .. -DCMAKE_BUILD_TYPE=Release && \
    make -j$(nproc)

# Download model
RUN bash ./models/download-ggml-model.sh $MODEL

# =============================================================================
# Production image
# =============================================================================
FROM ubuntu:22.04

ENV DEBIAN_FRONTEND=noninteractive

# Install runtime dependencies
RUN apt-get update && apt-get install -y --no-install-recommends \
    python3 \
    python3-pip \
    curl \
    libgomp1 \
    && rm -rf /var/lib/apt/lists/*

# Install Python dependencies for the server
RUN pip3 install --no-cache-dir asyncio

WORKDIR /app

# Create lib directory
RUN mkdir -p /app/lib

# Copy whisper.cpp binary from builder
COPY --from=builder /build/build/bin/whisper-cli /app/whisper-cli

# Copy shared libraries from builder
COPY --from=builder /build/build/src/libwhisper.so.1 /app/lib/
COPY --from=builder /build/build/src/libwhisper.so /app/lib/
COPY --from=builder /build/build/ggml/src/libggml.so /app/lib/
COPY --from=builder /build/build/ggml/src/libggml.so.0 /app/lib/
COPY --from=builder /build/build/ggml/src/libggml-base.so /app/lib/
COPY --from=builder /build/build/ggml/src/libggml-base.so.0 /app/lib/
COPY --from=builder /build/build/ggml/src/libggml-cpu.so /app/lib/
COPY --from=builder /build/build/ggml/src/libggml-cpu.so.0 /app/lib/

# Copy models
COPY --from=builder /build/models /app/models

# Copy server scripts
COPY whisper_server.py /app/whisper_server.py
COPY whisper_server.sh /app/whisper_server.sh

# Make scripts executable
RUN chmod +x /app/whisper-cli /app/whisper_server.sh

# Create socket directory
RUN mkdir -p /tmp/sockets

# Environment variables
ENV WHISPER_SOCKET_PATH=/tmp/sockets/whisper.sock
ENV WHISPER_MODEL=tiny
ENV WHISPER_LANGUAGE=fr
ENV WHISPER_BINARY=/app/whisper-cli
ENV WHISPER_MODELS_DIR=/app/models
ENV LD_LIBRARY_PATH=/app/lib:$LD_LIBRARY_PATH

# Expose socket directory as volume
VOLUME ["/tmp/sockets"]

# Health check
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD test -S $WHISPER_SOCKET_PATH || exit 1

# Run the Python server (better Unix socket support)
CMD ["python3", "/app/whisper_server.py"]
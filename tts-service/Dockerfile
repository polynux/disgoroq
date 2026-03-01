# =============================================================================
# Qwen3-TTS Service - Multi-stage Dockerfile for DisgoroQ
# Optimized for GPU/CUDA with proper cache layering
# =============================================================================

# =============================================================================
# Stage 1: Base image with system dependencies (cached layer)
# =============================================================================
ARG CUDA_VERSION=12.1.1
ARG CUDNN_VERSION=8
ARG UBUNTU_VERSION=22.04

FROM nvidia/cuda:${CUDA_VERSION}-cudnn${CUDNN_VERSION}-runtime-ubuntu${UBUNTU_VERSION} AS base

# Prevent interactive prompts
ENV DEBIAN_FRONTEND=noninteractive
ENV PYTHONUNBUFFERED=1
ENV PYTHONDONTWRITEBYTECODE=1
ENV NUMBA_CACHE_DIR=/tmp/numba_cache

# NVIDIA Container Runtime environment variables
ENV NVIDIA_VISIBLE_DEVICES=all
ENV NVIDIA_DRIVER_CAPABILITIES=compute,utility
ENV LD_LIBRARY_PATH=/usr/local/cuda/lib64:/usr/local/nvidia/lib:/usr/local/nvidia/lib64:${LD_LIBRARY_PATH}

# Install system dependencies (rarely changes - good for cache)
RUN apt-get update && apt-get install -y --no-install-recommends \
    python3.11 \
    python3.11-venv \
    python3.11-dev \
    python3-pip \
    build-essential \
    git \
    curl \
    ffmpeg \
    libsndfile1 \
    libsox-dev \
    sox \
    && rm -rf /var/lib/apt/lists/* \
    && ln -sf /usr/bin/python3.11 /usr/bin/python3 \
    && ln -sf /usr/bin/python3 /usr/bin/python

# Set up Python virtual environment
RUN python3 -m venv /opt/venv
ENV PATH="/opt/venv/bin:$PATH"

# Upgrade pip (separate layer for caching)
RUN pip install --no-cache-dir --upgrade pip setuptools wheel

# =============================================================================
# Stage 2: Dependencies builder (cached layer - only rebuilds when requirements change)
# =============================================================================
FROM base AS dependencies

WORKDIR /build

# Copy requirements first for better caching
COPY requirements.txt .

# Install PyTorch with CUDA support (large download - keep in separate layer)
RUN --mount=type=cache,target=/root/.cache/pip \
    pip install --no-cache-dir \
    torch>=2.0.0 \
    torchaudio>=2.0.0 \
    --index-url https://download.pytorch.org/whl/cu121

# Install other Python dependencies (separate layer)
RUN --mount=type=cache,target=/root/.cache/pip \
    pip install --no-cache-dir -r requirements.txt

# Install optional performance packages
RUN --mount=type=cache,target=/root/.cache/pip \
    pip install --no-cache-dir ninja packaging || true

# =============================================================================
# Stage 3: Builder for compiled extensions (if needed)
# =============================================================================
FROM dependencies AS builder

WORKDIR /build

# Copy qwen_tts module first (less frequent changes)
COPY qwen_tts/ ./qwen_tts/

# Copy API structure (moderate changes)
COPY api/ ./api/

# Copy main application (frequent changes)
COPY main.py .

# =============================================================================
# Stage 4: Production image
# =============================================================================
FROM base AS production

# Copy installed packages from dependencies stage
COPY --from=dependencies /opt/venv /opt/venv
ENV PATH="/opt/venv/bin:$PATH"

WORKDIR /app

# Copy qwen_tts module (less frequent changes)
COPY --from=builder /build/qwen_tts ./qwen_tts

# Copy API structure
COPY --from=builder /build/api ./api

# Copy main application (frequent changes - last layer)
COPY main.py .

# Create non-root user for security
RUN useradd --create-home --shell /bin/bash appuser \
    && mkdir -p /app/voices /tmp/numba_cache \
    && chown -R appuser:appuser /app /tmp/numba_cache

USER appuser

# Environment variables
ENV TTS_MODEL=Qwen/Qwen3-TTS-12Hz-0.6B-Base
ENV TTS_BACKEND=official
ENV TTS_PORT=8880
ENV TTS_HOST=0.0.0.0
ENV VOICE_LIBRARY_DIR=/app/voices
ENV PYTHONPATH=/app

EXPOSE 8880

# Health check
HEALTHCHECK --interval=30s --timeout=10s --start-period=120s --retries=3 \
    CMD curl -f http://localhost:8880/health || exit 1

# Run the server
CMD ["python", "-m", "main"]

# =============================================================================
# Stage 5: Development image (with additional tools)
# =============================================================================
FROM production AS development

USER root

# Install development tools
RUN apt-get update && apt-get install -y --no-install-recommends \
    vim \
    htop \
    gdb \
    && rm -rf /var/lib/apt/lists/*

# Install development Python packages
RUN --mount=type=cache,target=/root/.cache/pip \
    pip install --no-cache-dir \
    ipython \
    pytest \
    pytest-asyncio \
    httpx

USER appuser

# Development environment
ENV TTS_WARMUP_ON_START=false
ENV PYTHONPATH=/app

CMD ["python", "-m", "main"]

# =============================================================================
# Stage 6: CPU-only variant (for testing without GPU)
# =============================================================================
FROM python:3.11-slim AS cpu-base

ENV PYTHONUNBUFFERED=1
ENV PYTHONDONTWRITEBYTECODE=1
ENV NUMBA_CACHE_DIR=/tmp/numba_cache

# Install system dependencies
RUN apt-get update && apt-get install -y --no-install-recommends \
    build-essential \
    git \
    curl \
    ffmpeg \
    libsndfile1 \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /build

# Copy requirements
COPY requirements.txt .

# Install PyTorch CPU version
RUN --mount=type=cache,target=/root/.cache/pip \
    pip install --no-cache-dir --upgrade pip setuptools wheel \
    && pip install --no-cache-dir \
    torch>=2.0.0 \
    torchaudio>=2.0.0 \
    --index-url https://download.pytorch.org/whl/cpu

# Install other dependencies
RUN --mount=type=cache,target=/root/.cache/pip \
    pip install --no-cache-dir -r requirements.txt

WORKDIR /app

# Copy application
COPY . .

# Create user
RUN useradd --create-home --shell /bin/bash appuser \
    && mkdir -p /app/voices /tmp/numba_cache \
    && chown -R appuser:appuser /app /tmp/numba_cache
USER appuser

ENV TTS_PORT=8880
ENV TTS_HOST=0.0.0.0
ENV VOICE_LIBRARY_DIR=/app/voices
ENV PYTHONPATH=/app

EXPOSE 8880

HEALTHCHECK --interval=30s --timeout=10s --start-period=60s --retries=3 \
    CMD curl -f http://localhost:8880/health || exit 1

CMD ["python", "-m", "main"]
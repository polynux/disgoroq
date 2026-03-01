# coding=utf-8
# SPDX-License-Identifier: Apache-2.0
"""
Qwen3-TTS OpenAI-Compatible FastAPI Server for DisgoroQ Discord Bot.

A TTS API server providing OpenAI-compatible endpoints for the Qwen3-TTS model
with voice cloning support.
"""

import logging
import os
from contextlib import asynccontextmanager
from pathlib import Path

from fastapi import FastAPI
from fastapi.middleware.cors import CORSMiddleware

# Configure logging
logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s - %(name)s - %(levelname)s - %(message)s"
)
logger = logging.getLogger(__name__)

# Server configuration
HOST = os.getenv("TTS_HOST", "0.0.0.0")
PORT = int(os.getenv("TTS_PORT", "8880"))

# TTS configuration
TTS_MODEL = os.getenv("TTS_MODEL", "Qwen/Qwen3-TTS-12Hz-0.6B-Base")
TTS_BACKEND = os.getenv("TTS_BACKEND", "official")
VOICE_LIBRARY_DIR = os.getenv("VOICE_LIBRARY_DIR", "/app/voices")
TTS_WARMUP_ON_START = os.getenv("TTS_WARMUP_ON_START", "false").lower() == "true"

# CORS configuration
CORS_ORIGINS = os.getenv("CORS_ORIGINS", "*").split(",")


@asynccontextmanager
async def lifespan(app: FastAPI):
    """Lifespan context manager for model initialization."""

    # Print startup banner
    logger.info("=" * 50)
    logger.info("  Qwen3-TTS API Server for DisgoroQ")
    logger.info(f"  Model: {TTS_MODEL}")
    logger.info(f"  Backend: {TTS_BACKEND}")
    logger.info("=" * 50)
    logger.info(f"Server starting on http://{HOST}:{PORT}")
    logger.info(f"API Documentation: http://{HOST if HOST != '0.0.0.0' else 'localhost'}:{PORT}/docs")

    # Pre-load the TTS backend
    try:
        from api.backends import initialize_backend
        logger.info(f"Initializing TTS backend: {TTS_BACKEND}")
        backend = await initialize_backend(warmup=TTS_WARMUP_ON_START)
        logger.info(f"TTS backend '{backend.get_backend_name()}' loaded successfully!")
        logger.info(f"Model: {backend.get_model_id()}")

        device_info = backend.get_device_info()
        if device_info.get("gpu_available"):
            logger.info(f"GPU: {device_info.get('gpu_name')}")
            logger.info(f"VRAM: {device_info.get('vram_total')}")

        # Load custom voices if directory exists
        voices_path = Path(VOICE_LIBRARY_DIR)
        if voices_path.exists():
            logger.info(f"Loading custom voices from: {VOICE_LIBRARY_DIR}")
            await backend.load_custom_voices(VOICE_LIBRARY_DIR)

    except Exception as e:
        logger.warning(f"Backend initialization delayed: {e}")
        logger.info("Backend will be loaded on first request.")

    yield

    # Cleanup
    logger.info("Server shutting down...")


# Initialize FastAPI app
app = FastAPI(
    title="Qwen3-TTS API for DisgoroQ",
    description="""
## Qwen3-TTS OpenAI-Compatible API

A text-to-speech API server powered by Qwen3-TTS, providing full compatibility
with OpenAI's TTS API specification.

### Features
- 🎯 OpenAI API compatible endpoints
- 🌍 Multi-language support (10+ languages)
- 🎨 Multiple voice options
- 🔊 Voice cloning from reference audio
- 📊 Multiple audio formats (MP3, Opus, AAC, FLAC, WAV, PCM)
- ⚡ GPU-accelerated inference

### Endpoints
- `POST /v1/audio/speech` - Generate speech from text
- `POST /v1/audio/voice-clone` - Clone voice from reference audio
- `GET /v1/audio/voices` - List available voices
- `GET /v1/models` - List models
- `GET /health` - Health check
- `GET /vram` - VRAM status
- `POST /load` - Load model into VRAM
- `POST /unload` - Unload model from VRAM
""",
    version="1.0.0",
    lifespan=lifespan,
)

# Add CORS middleware
app.add_middleware(
    CORSMiddleware,
    allow_origins=CORS_ORIGINS,
    allow_credentials=True,
    allow_methods=["*"],
    allow_headers=["*"],
)


@app.get("/health")
async def health_check():
    """Health check endpoint with backend information."""
    try:
        from api.backends import get_backend

        backend = get_backend()
        device_info = backend.get_device_info()

        return {
            "status": "healthy" if backend.is_ready() else "initializing",
            "backend": {
                "name": backend.get_backend_name(),
                "model_id": backend.get_model_id(),
                "ready": backend.is_ready(),
                "supports_voice_cloning": backend.supports_voice_cloning(),
            },
            "device": {
                "type": device_info.get("device"),
                "gpu_available": device_info.get("gpu_available"),
                "gpu_name": device_info.get("gpu_name"),
                "vram_total": device_info.get("vram_total"),
                "vram_used": device_info.get("vram_used"),
            },
        }
    except Exception as e:
        logger.error(f"Health check error: {e}")
        return {
            "status": "error",
            "error": str(e),
            "backend": {
                "name": TTS_BACKEND,
                "ready": False,
            },
        }


@app.get("/vram")
async def get_vram():
    """Get current GPU VRAM status."""
    try:
        from api.backends import get_backend

        backend = get_backend()
        device_info = backend.get_device_info()

        # Parse VRAM strings to MB
        vram_total = device_info.get("vram_total", "0 GB")
        vram_used = device_info.get("vram_used", "0 GB")

        def parse_vram(vram_str):
            if not vram_str:
                return 0
            try:
                parts = vram_str.split()
                if len(parts) >= 2:
                    value = float(parts[0])
                    unit = parts[1].upper()
                    if unit == "GB":
                        return int(value * 1024)
                    elif unit == "MB":
                        return int(value)
                return 0
            except:
                return 0

        total_mb = parse_vram(vram_total)
        used_mb = parse_vram(vram_used)
        free_mb = max(0, total_mb - used_mb)

        return {
            "total_mb": total_mb,
            "used_mb": used_mb,
            "free_mb": free_mb,
            "model_loaded": backend.is_ready(),
        }
    except Exception as e:
        logger.error(f"VRAM check error: {e}")
        return {
            "total_mb": 0,
            "used_mb": 0,
            "free_mb": 0,
            "model_loaded": False,
            "error": str(e),
        }


@app.post("/load")
async def load_model():
    """Load the TTS model into VRAM."""
    try:
        from api.backends import get_backend, initialize_backend

        backend = get_backend()
        if backend.is_ready():
            return {"status": "already_loaded", "message": "Model is already loaded"}

        await initialize_backend(warmup=True)

        return {"status": "loaded", "message": "Model loaded successfully"}
    except Exception as e:
        logger.error(f"Failed to load model: {e}")
        return {"status": "error", "error": str(e)}, 500


@app.post("/unload")
async def unload_model():
    """Unload the TTS model from VRAM."""
    try:
        import torch

        # Clear CUDA cache
        if torch.cuda.is_available():
            torch.cuda.empty_cache()

        return {"status": "unloaded", "message": "Model unloaded successfully"}
    except Exception as e:
        logger.error(f"Failed to unload model: {e}")
        return {"status": "error", "error": str(e)}, 500


# Include OpenAI-compatible router
from api.routers.openai_compatible import router as openai_router
app.include_router(openai_router, prefix="/v1")


# Legacy endpoint compatibility for DisgoroQ Go client
@app.post("/tts/stream")
async def tts_stream_legacy():
    """Legacy endpoint - redirects to OpenAI-compatible endpoint."""
    from fastapi.responses import RedirectResponse
    return RedirectResponse(url="/v1/audio/speech")


if __name__ == "__main__":
    import uvicorn

    uvicorn.run(
        "main:app",
        host=HOST,
        port=PORT,
        reload=False,
        log_level="info",
    )
# coding=utf-8
# SPDX-License-Identifier: Apache-2.0
"""
Factory for creating TTS backend instances.
"""

import os
import logging
from pathlib import Path
from typing import Optional

from .base import TTSBackend

logger = logging.getLogger(__name__)

# Global backend instance
_backend_instance: Optional[TTSBackend] = None


def get_backend() -> TTSBackend:
    """
    Get or create the global TTS backend instance.

    The backend is selected based on the TTS_BACKEND environment variable:
    - "official" (default): Use official Qwen3-TTS implementation (GPU/CPU auto-detect)

    Returns:
        TTSBackend instance
    """
    global _backend_instance

    if _backend_instance is not None:
        return _backend_instance

    # Read configuration from environment variables
    backend_type = os.getenv("TTS_BACKEND", "official").lower()
    model_name = os.getenv("TTS_MODEL", "Qwen/Qwen3-TTS-12Hz-0.6B-Base")

    logger.info(f"Initializing TTS backend: {backend_type}")

    if backend_type == "official":
        # Lazy import to avoid issues if dependencies are missing
        from .official_qwen3_tts import OfficialQwen3TTSBackend

        _backend_instance = OfficialQwen3TTSBackend(model_name=model_name)
        logger.info(f"Using official Qwen3-TTS backend with model: {_backend_instance.get_model_id()}")

    elif backend_type in ("vllm_omni", "vllm-omni", "vllm"):
        try:
            from .vllm_omni_qwen3_tts import VLLMOmniQwen3TTSBackend
            _backend_instance = VLLMOmniQwen3TTSBackend(model_name=model_name)
            logger.info(f"Using vLLM-Omni backend with model: {_backend_instance.get_model_id()}")
        except ImportError as e:
            logger.error(f"vLLM backend not available: {e}")
            logger.info("Falling back to official backend")
            from .official_qwen3_tts import OfficialQwen3TTSBackend
            _backend_instance = OfficialQwen3TTSBackend(model_name=model_name)

    elif backend_type == "pytorch":
        try:
            from .pytorch_backend import PyTorchCPUBackend
            device = os.getenv("TTS_DEVICE", "cpu")
            dtype = os.getenv("TTS_DTYPE", "float32")
            _backend_instance = PyTorchCPUBackend(
                model_id=model_name,
                device=device,
                dtype=dtype,
            )
            logger.info(f"Using CPU-optimized PyTorch backend")
        except ImportError as e:
            logger.error(f"PyTorch CPU backend not available: {e}")
            logger.info("Falling back to official backend")
            from .official_qwen3_tts import OfficialQwen3TTSBackend
            _backend_instance = OfficialQwen3TTSBackend(model_name=model_name)

    elif backend_type == "openvino":
        try:
            from .openvino_backend import OpenVINOBackend
            _backend_instance = OpenVINOBackend()
            logger.info("Using OpenVINO backend")
        except ImportError as e:
            logger.error(f"OpenVINO backend not available: {e}")
            logger.info("Falling back to official backend")
            from .official_qwen3_tts import OfficialQwen3TTSBackend
            _backend_instance = OfficialQwen3TTSBackend(model_name=model_name)

    else:
        logger.error(f"Unknown backend type: {backend_type}")
        raise ValueError(
            f"Unknown TTS_BACKEND: {backend_type}. "
            f"Supported values: 'official', 'vllm_omni', 'pytorch', 'openvino'"
        )

    return _backend_instance


async def initialize_backend(warmup: bool = False) -> TTSBackend:
    """
    Initialize the backend and optionally perform warmup.

    Args:
        warmup: Whether to run a warmup inference

    Returns:
        Initialized TTSBackend instance
    """
    backend = get_backend()

    # Initialize the backend
    await backend.initialize()

    # Load custom voices
    custom_voices_dir = os.getenv(
        "VOICE_LIBRARY_DIR",
        str(Path(__file__).resolve().parent.parent.parent / "voices"),
    )

    if Path(custom_voices_dir).exists():
        try:
            await backend.load_custom_voices(custom_voices_dir)
        except Exception as e:
            logger.warning(f"Custom voice loading failed (non-critical): {e}")

    # Perform warmup if requested
    if warmup:
        logger.info("Performing backend warmup...")
        try:
            # Check if we have custom voices for base models
            if backend.get_model_type() == "base":
                custom_names = backend.get_custom_voice_names()
                if custom_names:
                    await backend.generate_speech_with_custom_voice(
                        text="Hello, this is a warmup test.",
                        voice=custom_names[0],
                        language="English",
                    )
                else:
                    logger.info("Skipping warmup: Base model has no custom voices")
            else:
                await backend.generate_speech(
                    text="Hello, this is a warmup test.",
                    voice="Vivian",
                    language="English",
                )
            logger.info("Backend warmup completed successfully")
        except Exception as e:
            logger.warning(f"Backend warmup failed (non-critical): {e}")

    return backend


def reset_backend() -> None:
    """Reset the global backend instance (useful for testing)."""
    global _backend_instance
    _backend_instance = None
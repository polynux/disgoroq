# coding=utf-8
# SPDX-License-Identifier: Apache-2.0
"""
Simplified TTS router for voice cloning only.
Uses a single pre-loaded voice with ICL mode.
"""

import logging
from typing import Literal, Optional

import numpy as np
from fastapi import APIRouter, HTTPException, Response
from fastapi.responses import StreamingResponse
from pydantic import BaseModel, Field

from ..config import TTS_DEFAULT_LANGUAGE, VOICE_NAME
from ..structures.schemas import NormalizationOptions
from ..services.text_processing import normalize_text
from ..services.audio_encoding import encode_audio, get_content_type

logger = logging.getLogger(__name__)

router = APIRouter(
    tags=["TTS"],
    responses={404: {"description": "Not found"}},
)


class TTSGenerateRequest(BaseModel):
    """Request schema for TTS generation endpoint."""

    input: str = Field(
        ...,
        description="The text to generate audio for.",
        max_length=4096,
    )
    response_format: Literal["mp3", "opus", "aac", "flac", "wav", "pcm"] = Field(
        default="wav",
        description="The format to return audio in. Default: wav (highest quality).",
    )
    speed: float = Field(
        default=1.0,
        ge=0.25,
        le=4.0,
        description="The speed of the generated audio. Select a value from 0.25 to 4.0.",
    )
    language: Optional[str] = Field(
        default=None,
        description=f"Language code for TTS. Default: {TTS_DEFAULT_LANGUAGE}",
    )
    stream: bool = Field(
        default=False,
        description="If True, stream audio chunks as they are generated for lower latency.",
    )
    normalization_options: Optional[NormalizationOptions] = Field(
        default_factory=NormalizationOptions,
        description="Options for the text normalization system",
    )


async def get_tts_backend():
    """Get the TTS backend instance, initializing if needed."""
    from ..backends import get_backend, initialize_backend

    backend = get_backend()

    if not backend.is_ready():
        await initialize_backend()

    return backend


@router.post("/v1/tts/generate")
async def generate_tts(request: TTSGenerateRequest):
    """
    Generate speech using the pre-loaded custom voice.

    This endpoint uses ICL mode (In-Context Learning) with a single
    pre-loaded voice from the voices/default/ directory.

    The voice must have:
    - reference.wav: Audio file of the voice to clone
    - reference.txt: Transcript of the reference audio (REQUIRED for ICL)

    If stream=True, audio is streamed as it's generated for lower latency.
    """
    try:
        backend = await get_tts_backend()

        # Check that a custom voice is loaded
        custom_voices = list(backend._custom_voices.keys())
        if not custom_voices:
            raise HTTPException(
                status_code=500,
                detail={
                    "error": "no_voice_loaded",
                    "message": "No custom voice loaded. Ensure voices/default/ exists with reference.wav and reference.txt.",
                    "type": "configuration_error",
                },
            )

        # Use the configured voice name, or fall back to first available
        voice_to_use = VOICE_NAME if VOICE_NAME in custom_voices else custom_voices[0]

        # Normalize input text
        normalized_text = normalize_text(request.input, request.normalization_options)

        if not normalized_text.strip():
            raise HTTPException(
                status_code=400,
                detail={
                    "error": "invalid_input",
                    "message": "Input text is empty after normalization",
                    "type": "invalid_request_error",
                },
            )

        # Use configured default language if not specified
        language = request.language or TTS_DEFAULT_LANGUAGE

        # STREAMING MODE
        if request.stream:
            # For streaming, we must use PCM format because WAV requires
            # a header with total file size which is unknown until generation completes
            stream_format = "pcm"  # Force PCM for streaming
            logger.info(
                f"Starting streaming TTS generation for text: {normalized_text[:50]}..."
            )

            async def generate_audio_chunks():
                """Async generator that yields audio chunks."""
                try:
                    chunk_count = 0
                    async for (
                        pcm_chunk,
                        sample_rate,
                    ) in backend.generate_speech_streaming(
                        text=normalized_text,
                        voice=voice_to_use,
                        language=language,
                        speed=request.speed,
                    ):
                        # Encode chunk to PCM format (raw bytes, no header needed)
                        chunk_bytes = encode_audio(
                            pcm_chunk, stream_format, sample_rate
                        )
                        chunk_count += 1
                        logger.debug(
                            f"Streaming chunk {chunk_count}: {len(chunk_bytes)} bytes"
                        )
                        yield chunk_bytes
                    logger.info(f"Streaming completed: {chunk_count} chunks")
                except Exception as e:
                    logger.error(f"Streaming failed: {e}")
                    raise

            content_type = get_content_type(stream_format)
            # Use audio/raw for PCM streaming
            return StreamingResponse(
                generate_audio_chunks(),
                media_type="audio/raw",  # Raw PCM
                headers={
                    "Cache-Control": "no-cache, no-store, must-revalidate",
                    "X-Audio-Format": "pcm",
                    "X-Sample-Rate": "24000",
                    "X-Channels": "1",
                    "X-Bits-Per-Sample": "16",
                },
            )

        # NON-STREAMING MODE
        # Generate speech using custom voice
        audio, sample_rate = await backend.generate_speech_with_custom_voice(
            text=normalized_text,
            voice=voice_to_use,
            language=language,
            speed=request.speed,
        )

        # Encode audio to requested format
        audio_bytes = encode_audio(audio, request.response_format, sample_rate)

        # Get content type
        content_type = get_content_type(request.response_format)

        # Return audio response
        return Response(
            content=audio_bytes,
            media_type=content_type,
            headers={
                "Content-Disposition": f"attachment; filename=speech.{request.response_format}",
                "Cache-Control": "no-cache",
            },
        )

    except HTTPException:
        raise
    except Exception as e:
        logger.error(f"TTS generation failed: {e}")
        raise HTTPException(
            status_code=500,
            detail={
                "error": "processing_error",
                "message": str(e),
                "type": "server_error",
            },
        )


@router.get("/v1/tts/voice")
async def get_voice_info():
    """
    Get information about the currently loaded voice.

    Returns the voice name and reference file information.
    """
    try:
        backend = await get_tts_backend()

        custom_voices = list(backend._custom_voices.keys())
        if not custom_voices:
            raise HTTPException(
                status_code=500,
                detail={
                    "error": "no_voice_loaded",
                    "message": "No custom voice loaded. Ensure voices/default/ exists with reference.wav and reference.txt.",
                    "type": "configuration_error",
                },
            )

        voice_to_use = VOICE_NAME if VOICE_NAME in custom_voices else custom_voices[0]

        return {
            "voice_name": voice_to_use,
            "available_voices": custom_voices,
            "language": TTS_DEFAULT_LANGUAGE,
            "mode": "icl",
        }

    except HTTPException:
        raise
    except Exception as e:
        logger.error(f"Failed to get voice info: {e}")
        raise HTTPException(
            status_code=500,
            detail={
                "error": "internal_error",
                "message": str(e),
                "type": "server_error",
            },
        )

#!/usr/bin/env python3
"""
Whisper.cpp Unix Socket Server

This script provides a Unix socket interface for whisper.cpp,
allowing real-time speech-to-text transcription.

The server accepts JSON requests over a Unix socket and returns
transcriptions with confidence scores.
"""

import asyncio
import base64
import json
import logging
import os
import signal
import struct
import sys
import tempfile
import wave
from pathlib import Path
from typing import Optional, Tuple

logging.basicConfig(
    level=logging.INFO, format="%(asctime)s - %(name)s - %(levelname)s - %(message)s"
)
logger = logging.getLogger(__name__)

# Configuration
SOCKET_PATH = os.getenv("WHISPER_SOCKET_PATH", "/tmp/whisper.sock")
WHISPER_MODEL = os.getenv("WHISPER_MODEL", "tiny")
WHISPER_LANGUAGE = os.getenv("WHISPER_LANGUAGE", "fr")
WHISPER_BINARY = os.getenv("WHISPER_BINARY", "./main")
MODELS_DIR = os.getenv("WHISPER_MODELS_DIR", "./models")


class WhisperServer:
    """Unix socket server for whisper.cpp transcription."""

    def __init__(self, socket_path: str, model: str, language: str):
        self.socket_path = socket_path
        self.model = model
        self.language = language
        self.server: Optional[asyncio.Server] = None
        self.whisper_process: Optional[asyncio.subprocess.Process] = None
        self._running = False

    async def start(self):
        """Start the Unix socket server."""
        # Remove existing socket
        if os.path.exists(self.socket_path):
            os.unlink(self.socket_path)

        # Ensure directory exists
        socket_dir = os.path.dirname(self.socket_path)
        if socket_dir:
            os.makedirs(socket_dir, exist_ok=True)

        # Start the server
        self.server = await asyncio.start_unix_server(
            self._handle_client, path=self.socket_path
        )

        # Set permissions
        os.chmod(self.socket_path, 0o666)

        self._running = True
        logger.info(f"Whisper server listening on {self.socket_path}")
        logger.info(f"Model: {self.model}, Language: {self.language}")

    async def stop(self):
        """Stop the server."""
        self._running = False
        if self.server:
            self.server.close()
            await self.server.wait_closed()
        if os.path.exists(self.socket_path):
            os.unlink(self.socket_path)
        logger.info("Whisper server stopped")

    async def _handle_client(
        self, reader: asyncio.StreamReader, writer: asyncio.StreamWriter
    ):
        """Handle a client connection."""
        client_addr = writer.get_extra_info("peername") or "unknown"
        logger.debug(f"Client connected: {client_addr}")

        try:
            while self._running:
                # Read message length (4 bytes, big-endian)
                length_data = await reader.readexactly(4)
                length = struct.unpack(">I", length_data)[0]

                if length == 0:
                    continue

                # Read message data
                data = await reader.readexactly(length)

                # Parse request
                try:
                    request = json.loads(data.decode("utf-8"))
                    response = await self._process_request(request)
                except json.JSONDecodeError as e:
                    response = {"error": f"Invalid JSON: {e}"}
                except Exception as e:
                    logger.error(f"Error processing request: {e}")
                    response = {"error": str(e)}

                # Send response
                response_data = json.dumps(response).encode("utf-8")
                response_length = struct.pack(">I", len(response_data))
                writer.write(response_length + response_data)
                await writer.drain()

        except asyncio.IncompleteReadError:
            logger.debug(f"Client disconnected: {client_addr}")
        except Exception as e:
            logger.error(f"Error handling client: {e}")
        finally:
            writer.close()
            try:
                await writer.wait_closed()
            except:
                pass

    async def _process_request(self, request: dict) -> dict:
        """Process a transcription request."""
        command = request.get("command", "transcribe")

        if command == "transcribe":
            return await self._transcribe(request)
        elif command == "health":
            return {"status": "healthy", "model": self.model, "language": self.language}
        elif command == "start":
            # Start streaming session (placeholder)
            return {"status": "started"}
        elif command == "stop":
            # Stop streaming session (placeholder)
            return {"status": "stopped", "text": ""}
        elif command == "reset":
            return {"status": "reset"}
        else:
            return {"error": f"Unknown command: {command}"}

    async def _transcribe(self, request: dict) -> dict:
        """Transcribe audio data using whisper.cpp."""
        audio_data = request.get("audio")

        # Handle different audio data formats from JSON
        logger.info(
            f"Received audio data type: {type(audio_data).__name__ if audio_data is not None else 'None'}"
        )
        if isinstance(audio_data, str):
            logger.info(
                f"Audio data is string, length: {len(audio_data)}, first 50 chars: {audio_data[:50] if len(audio_data) > 50 else audio_data}"
            )

        if audio_data is None:
            return {"error": "No audio data provided"}

        if isinstance(audio_data, str):
            # Go's JSON marshaller encodes []byte as base64
            try:
                audio_data = base64.b64decode(audio_data)
                logger.info(f"Decoded base64 audio: {len(audio_data)} bytes")
            except Exception as e:
                logger.error(f"Failed to decode base64 audio: {e}")
                return {"error": f"Invalid base64 audio data: {e}"}
        elif isinstance(audio_data, list):
            # Some clients may send as array of bytes
            audio_data = bytes(audio_data)
            logger.info(f"Converted list to bytes: {len(audio_data)} bytes")

        if not isinstance(audio_data, bytes):
            logger.error(
                f"Unexpected audio data type after conversion: {type(audio_data)}"
            )
            return {
                "error": f"Invalid audio data type: expected bytes, got {type(audio_data).__name__}"
            }

        language = request.get("language", self.language)

        if len(audio_data) == 0:
            return {"error": "No audio data provided"}

        logger.info(f"Processing {len(audio_data)} bytes of audio data")

        # Write audio to temporary WAV file
        with tempfile.NamedTemporaryFile(suffix=".wav", delete=False) as tmp_file:
            tmp_path = tmp_file.name
            # Convert raw PCM to WAV
            sample_rate = request.get("sample_rate", 16000)
            channels = request.get("channels", 1)
            self._write_wav(tmp_file, audio_data, sample_rate, channels)

        try:
            # Run whisper.cpp
            model_path = f"{MODELS_DIR}/ggml-{self.model}.bin"

            cmd = [
                WHISPER_BINARY,
                "-m",
                model_path,
                "-f",
                tmp_path,
                "-l",
                language,
                "--output-json",
                "--no-prints",
            ]

            # Set environment with library path
            env = os.environ.copy()
            env["LD_LIBRARY_PATH"] = "/app/lib:" + env.get("LD_LIBRARY_PATH", "")

            # Run whisper.cpp process
            process = await asyncio.create_subprocess_exec(
                *cmd,
                stdout=asyncio.subprocess.PIPE,
                stderr=asyncio.subprocess.PIPE,
                env=env,
            )

            stdout, stderr = await process.communicate()

            if process.returncode != 0:
                logger.error(f"Whisper failed: {stderr.decode()}")
                return {"error": f"Transcription failed: {stderr.decode()}"}

            # Parse JSON output (whisper writes to file.json)
            json_path = tmp_path + ".json"
            if os.path.exists(json_path):
                with open(json_path, "r") as f:
                    result = json.load(f)
                os.unlink(json_path)

                # Extract transcription
                text = ""
                transcription = result.get("transcription", [])
                if transcription and len(transcription) > 0:
                    text = transcription[0].get("text", "").strip()

                if not text:
                    # Try alternative format
                    text = result.get("text", "").strip()

                # Calculate duration safely
                duration = 0.0
                try:
                    if transcription and len(transcription) > 0:
                        offsets = transcription[0].get("offsets", {})
                        if isinstance(offsets, dict):
                            duration = offsets.get("to", 0) / 100.0
                        elif isinstance(offsets, list) and len(offsets) > 0:
                            duration = offsets[-1].get("to", 0) / 100.0
                except (KeyError, TypeError, IndexError):
                    pass

                return {
                    "text": text,
                    "language": result.get("result", {}).get("language", language),
                    "confidence": 0.9,  # Placeholder
                    "duration": duration,
                }
            else:
                # Fallback to stdout parsing
                text = stdout.decode().strip()
                return {"text": text, "language": language, "confidence": 0.8}

        except Exception as e:
            logger.error(f"Transcription error: {e}", exc_info=True)
            return {"error": str(e)}
        finally:
            # Cleanup
            if os.path.exists(tmp_path):
                os.unlink(tmp_path)

    def _write_wav(self, file, audio_data: bytes, sample_rate: int, channels: int):
        """Write PCM audio data to a WAV file."""
        # Ensure audio_data is bytes
        if isinstance(audio_data, str):
            logger.warning("audio_data is str in _write_wav, attempting to encode")
            audio_data = audio_data.encode("latin-1")
        elif isinstance(audio_data, memoryview):
            audio_data = bytes(audio_data)
        elif not isinstance(audio_data, bytes):
            logger.error(f"audio_data must be bytes, got {type(audio_data)}")
            raise TypeError(f"audio_data must be bytes, got {type(audio_data)}")

        with wave.open(file, "wb") as wav_file:
            wav_file.setnchannels(channels)
            wav_file.setsampwidth(2)  # 16-bit
            wav_file.setframerate(sample_rate)
            wav_file.writeframes(audio_data)


async def main():
    """Main entry point."""
    server = WhisperServer(SOCKET_PATH, WHISPER_MODEL, WHISPER_LANGUAGE)

    # Setup signal handlers
    loop = asyncio.get_event_loop()
    stop_event = asyncio.Event()

    def signal_handler():
        logger.info("Shutdown signal received")
        stop_event.set()

    for sig in (signal.SIGINT, signal.SIGTERM):
        loop.add_signal_handler(sig, signal_handler)

    try:
        await server.start()
        await stop_event.wait()
    finally:
        await server.stop()


if __name__ == "__main__":
    asyncio.run(main())

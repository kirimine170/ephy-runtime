"""Loopback-only speech service with a disposable persistent inference worker．"""

from __future__ import annotations

import asyncio
import base64
import importlib.util
import io
import json
from pathlib import Path
import sys
import wave
from contextlib import asynccontextmanager
from typing import Any

from fastapi import FastAPI, Request
from fastapi.responses import JSONResponse, StreamingResponse

from .qwen import MODEL_REVISION, SOURCE_REVISION
from .schemas import (MAX_REQUEST_BYTES, MAX_WAV_BYTES, MAX_WORKER_FRAME_BYTES,
                      SpeechError, SpeechRequest, error_code, public_profile, parse_speech_request)


def encode_frame(value: dict[str, Any]) -> bytes:
    return (json.dumps(value, ensure_ascii=True, separators=(",", ":"), allow_nan=False) + "\n").encode()


def validate_wav(body: bytes) -> None:
    if not 44 <= len(body) <= MAX_WAV_BYTES or body[:4] != b"RIFF" or body[8:12] != b"WAVE":
        raise SpeechError("tts_invalid_audio")
    if int.from_bytes(body[4:8], "little") + 8 != len(body):
        raise SpeechError("tts_invalid_audio")
    try:
        with wave.open(io.BytesIO(body), "rb") as wav:
            if (wav.getnchannels(), wav.getsampwidth(), wav.getframerate(), wav.getcomptype()) != (1, 2, 24000, "NONE"):
                raise SpeechError("tts_invalid_audio")
            frames = wav.getnframes()
            if not 0 < frames <= 24000 * 60 or len(wav.readframes(frames)) != frames * 2:
                raise SpeechError("tts_invalid_audio")
    except SpeechError:
        raise
    except Exception:
        raise SpeechError("tts_invalid_audio") from None


class ProcessWorker:
    """Only JSON bytes cross the process boundary；no pickle or shared tensors．"""

    def __init__(self, config: dict[str, Any], *, command: list[str] | None = None):
        self.config = config
        self.command = command or [sys.executable, "-m", "apps.speech", "--worker"]
        self.process: asyncio.subprocess.Process | None = None
        self.lock = asyncio.Lock()

    async def _line(self) -> dict[str, Any]:
        assert self.process is not None and self.process.stdout is not None
        try:
            line = await self.process.stdout.readline()
            if not line.endswith(b"\n") or len(line) > MAX_WORKER_FRAME_BYTES:
                raise SpeechError("tts_stream_eof")
            value = json.loads(line)
            if not isinstance(value, dict):
                raise SpeechError("tts_stream_eof")
            return value
        except SpeechError:
            raise
        except Exception:
            raise SpeechError("tts_stream_eof") from None

    async def _write(self, value: dict[str, Any]) -> None:
        assert self.process is not None and self.process.stdin is not None
        self.process.stdin.write(encode_frame(value))
        await self.process.stdin.drain()

    async def _start(self) -> None:
        if self.process is not None and self.process.returncode is None:
            return
        await self.stop()
        self.process = await asyncio.create_subprocess_exec(
            *self.command, stdin=asyncio.subprocess.PIPE, stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.DEVNULL, cwd=str(Path(__file__).resolve().parents[2]),
            limit=MAX_WORKER_FRAME_BYTES,
        )
        await self._write({"type": "configure", "config": self.config})
        if await self._line() != {"type": "ready"}:
            raise SpeechError("tts_unavailable")

    async def stop(self) -> None:
        process, self.process = self.process, None
        if process is None:
            return
        if process.returncode is None:
            try:
                process.terminate()
            except ProcessLookupError:
                pass
        try:
            await asyncio.wait_for(process.wait(), timeout=1.0)
        except asyncio.TimeoutError:
            try:
                process.kill()
            except ProcessLookupError:
                pass
            await asyncio.wait_for(process.wait(), timeout=1.0)
        if process.stdin is not None:
            process.stdin.close()

    async def synthesize(self, request: SpeechRequest, profile: dict[str, Any]) -> bytes:
        if self.lock.locked():
            raise SpeechError("tts_busy")
        async with self.lock:
            try:
                await asyncio.wait_for(self._start(), timeout=5.0)
                await self._write({"type": "synthesize", "request": request.model_dump(), "profile": profile})
                result = await self._line()
                if result.get("request_id") != request.request_id:
                    raise SpeechError("tts_stream_eof")
                if result.get("type") == "error":
                    raise SpeechError(result.get("error_code", "tts_failed"))
                if set(result) != {"type", "request_id", "wav_base64"} or result.get("type") != "result":
                    raise SpeechError("tts_stream_eof")
                body = base64.b64decode(result["wav_base64"], validate=True)
                validate_wav(body)
                return body
            except BaseException:
                await self.stop()
                raise


class SpeechService:
    def __init__(self, config: dict[str, Any] | None = None, *, worker: Any = None,
                 available: bool | None = None, request_timeout: float = 60.0):
        self.config = config or {}
        self.request_timeout = min(60.0, max(0.01, request_timeout))
        self.available = self._configured() if available is None else available
        raw_profiles = self.config.get("profiles", [])
        if not isinstance(raw_profiles, list) or len(raw_profiles) > 16:
            raise SpeechError("invalid_speech_text")
        self.profiles = {p["voice_profile_id"]: public_profile(p, available=self.available) for p in raw_profiles}
        if len(self.profiles) != len(raw_profiles):
            raise SpeechError("invalid_speech_text")
        default = self.config.get("default_profile_id", "")
        if default and default not in self.profiles:
            raise SpeechError("invalid_speech_text")
        self.default_profile_id = default
        self.worker = worker or ProcessWorker(self.config)

    def _configured(self) -> bool:
        try:
            return (self.config.get("source_revision") == SOURCE_REVISION and
                    self.config.get("model_revision") == MODEL_REVISION and
                    Path(self.config["model_path"]).is_dir() and
                    Path(self.config["asset_store_path"]).is_dir() and
                    importlib.util.find_spec("qwen_tts") is not None and
                    importlib.util.find_spec("torch") is not None)
        except Exception:
            return False

    def catalog(self) -> dict[str, Any]:
        result: dict[str, Any] = {"default_profile_id": self.default_profile_id, "profiles": list(self.profiles.values())}
        if not self.available:
            result["error_code"] = "tts_unavailable"
        return result

    def profile(self, request: SpeechRequest) -> dict[str, Any]:
        if not self.available:
            raise SpeechError("tts_unavailable")
        profile = self.profiles.get(request.voice_profile_id)
        if profile is None or any(profile[key] != getattr(request, key) for key in ("model_revision", "clone_prompt_digest")):
            raise SpeechError("voice_profile_changed")
        return profile

    async def events(self, request: SpeechRequest, connection: Any):
        job: asyncio.Task | None = None

        async def abort() -> None:
            if job is not None and not job.done():
                job.cancel()
                try:
                    await job
                except BaseException:
                    pass
                # Only synthesize knows whether this request owns the worker．
                # A canceled contender must never stop another request's model．

        try:
            profile = self.profile(request)
            # Resolve omitted controls from the selected profile without replacing
            # explicitly supplied values，including an explicit volume of 1.0．
            request = parse_speech_request({**profile["default_style"], **request.model_dump(exclude_unset=True)})
            job = asyncio.create_task(self.worker.synthesize(request, profile))
            deadline = asyncio.get_running_loop().time() + self.request_timeout
            while not job.done():
                if await connection.is_disconnected():
                    raise SpeechError("tts_canceled")
                if asyncio.get_running_loop().time() >= deadline:
                    raise SpeechError("tts_timeout")
                await asyncio.wait({job}, timeout=0.02)
            body = job.result()
            validate_wav(body)
            if await connection.is_disconnected():
                raise SpeechError("tts_canceled")
            yield encode_frame({"type": "audio", "request_id": request.request_id, "sequence": 1,
                                "wav_base64": base64.b64encode(body).decode("ascii")})
            yield encode_frame({"type": "completed", "request_id": request.request_id, "sequence": 1, "finish_reason": "stop"})
        except asyncio.CancelledError:
            await abort()
            raise
        except BaseException as exc:
            await abort()
            yield encode_frame({"type": "error", "request_id": request.request_id, "error_code": error_code(exc)})
        finally:
            await abort()


def create_app(config: dict[str, Any] | None = None, *, service: SpeechService | None = None) -> FastAPI:
    speech = service or SpeechService(config)
    @asynccontextmanager
    async def lifespan(_: FastAPI):
        try:
            yield
        finally:
            await speech.worker.stop()
    app = FastAPI(docs_url=None, redoc_url=None, openapi_url=None, lifespan=lifespan)
    app.state.speech = speech

    @app.get("/health")
    async def health() -> dict[str, Any]:
        return {"state": "ready" if speech.available else "unavailable", "streaming_mode": "phrase"}

    @app.get("/v1/voice-profiles")
    async def catalog() -> dict[str, Any]:
        return speech.catalog()

    @app.post("/v1/speech")
    async def synthesize(connection: Request):
        try:
            if connection.headers.get("origin") is not None or connection.headers.get("content-type", "").split(";", 1)[0].strip().lower() != "application/json":
                raise SpeechError("invalid_speech_text")
            body = bytearray()
            async with asyncio.timeout(5.0):
                async for part in connection.stream():
                    if len(body) + len(part) > MAX_REQUEST_BYTES:
                        raise SpeechError("invalid_speech_text")
                    body.extend(part)
            request = parse_speech_request(body)
            speech.profile(request)
        except SpeechError as exc:
            return JSONResponse({"error_code": exc.code}, status_code=400)
        except Exception:
            # Pydantic's normal detail includes the rejected speech text．
            return JSONResponse({"error_code": "invalid_speech_text"}, status_code=400)
        return StreamingResponse(speech.events(request, connection), media_type="application/x-ndjson",
                                 headers={"Cache-Control": "no-store", "X-Content-Type-Options": "nosniff"})

    return app

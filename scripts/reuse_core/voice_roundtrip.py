"""Local whisper.cpp → real Pipecat frames → Work → ProcessWorker．No microphone．"""

from __future__ import annotations
import argparse
import asyncio
import base64
import hashlib
import io
import json
from pathlib import Path
import time
import subprocess
import uuid
import wave

from scripts.asr.evaluate import Process
from scripts.reuse_core.pipecat_probe import (
    FrameProcessor,
    Pipeline,
    PipelineWorker,
    PipelineParams,
    WorkerRunner,
    LLMContext,
    LLMContextAggregatorPair,
    LLMUserAggregatorParams,
    ExternalUserTurnStrategies,
    LLMContextFrame,
    TranscriptionFrame,
    UserStartedSpeakingFrame,
    UserStoppedSpeakingFrame,
    EndFrame,
    DeliveryBoundary,
    EpochAudio,
)
from apps.speech.schemas import SpeechRequest
from apps.speech.service import ProcessWorker, SpeechService
from packages.config_core.loader import ModelConfig
from packages.llm_runtime.adapter import LlamaCppChatAdapter
from packages.llm_runtime.schemas import ChatCompletionRequest, ChatMessage


def start_asr(config):
    for name in ("helper", "model", "vad"):
        with Path(config[name]).open("rb") as source:
            if (
                hashlib.file_digest(source, "sha256").hexdigest()
                != config[name + "_sha256"]
            ):
                raise ValueError("asr_asset_hash_mismatch")
    process = Process(
        [
            config["helper"],
            "--model",
            config["model"],
            "--vad",
            config["vad"],
            "--model-revision",
            config["model_id"] + ":sha256:" + config["model_sha256"][:16],
            "--threads",
            str(config.get("threads", 4)),
            "--step-ms",
            str(config.get("step_ms", 500)),
        ]
    )
    try:
        at, event = process.event(time.monotonic() + 60)
        if event.get("type") != "ready":
            raise ValueError("asr_not_ready")
        return process, (at - process.started) * 1000
    except BaseException:
        process.close()
        raise


def transcribe(process, path):
    with wave.open(str(path)) as audio:
        rate = audio.getframerate()
        if (
            audio.getnchannels() != 1
            or audio.getsampwidth() != 2
            or not 8000 <= rate <= 48000
        ):
            raise ValueError("invalid_audio")
        pcm = audio.readframes(audio.getnframes())
    if not 0 < len(pcm) <= rate * 2 * 60:
        raise ValueError("audio_limit")
    identity = dict.fromkeys(
        ("operation_id", "session_id", "turn_id", "segment_id"),
        "reuse-" + uuid.uuid4().hex,
    )
    start = time.monotonic()
    deadline = start + 90
    process.send(
        {
            "type": "start",
            "protocol": 1,
            "sample_rate": rate,
            "partial": False,
            **identity,
        }
    )
    _, event = process.event(deadline)
    if event.get("type") != "started":
        raise ValueError("asr_protocol")
    for sequence, offset in enumerate(range(0, len(pcm), 64000), 1):
        process.send(
            {
                "type": "audio",
                "protocol": 1,
                "sequence": sequence,
                **identity,
                "pcm_base64": base64.b64encode(pcm[offset : offset + 64000]).decode(),
            }
        )
    process.send({"type": "finish", "protocol": 1, **identity})
    finals = []
    while True:
        _, event = process.event(deadline)
        if event.get("type") == "done":
            break
        if event.get("phase") == "final":
            finals.append(event.get("transcript", ""))
        if event.get("phase") in {"failure", "timeout"}:
            raise ValueError("asr_failed")
    if len(finals) != 1 or not finals[0]:
        raise ValueError("asr_final_count")
    return finals[0], (time.monotonic() - start) * 1000


async def complete(text):
    import httpx

    adapter = LlamaCppChatAdapter(timeout=90)
    await adapter._client.aclose()
    adapter._client = httpx.AsyncClient(
        timeout=90, trust_env=False, follow_redirects=False
    )
    cfg = ModelConfig(
        provider="llama_cpp",
        base_url="http://127.0.0.1:8082/v1",
        model="qwen3.8-27b",
        max_context=8192,
        default_temperature=0.3,
        thinking_mode="always",
        preserve_thinking=True,
    )
    request = ChatCompletionRequest(
        model=cfg.model,
        messages=[
            ChatMessage(
                role="system",
                content="あなたはEphyです．音声接続の合成試験です．一人称はわたし，親しみのある敬語，句読点は，と．を使い，確認を40文字以内の一文で返してください．",
            ),
            ChatMessage(role="user", content=text),
        ],
        max_tokens=2048,
        temperature=0.3,
    )
    start = time.monotonic()
    try:
        result = await adapter.create_chat_completion(cfg, request)
        choice = result["choices"][0]
        answer = choice["message"].get("content") or ""
        if choice["finish_reason"] != "stop" or not 0 < len(answer) <= 180:
            raise ValueError("incomplete_or_long_generation")
        return answer, (time.monotonic() - start) * 1000
    finally:
        await adapter.aclose()


async def run(args):
    output = Path(args.output)
    output.mkdir(parents=True, exist_ok=True, mode=0o700)
    config = json.loads(Path(args.config).read_text())
    asr_config = json.loads(Path(args.asr_config).read_text())
    tts = ProcessWorker(config)
    service = SpeechService(config, worker=tts, available=True)
    profile = service.profiles["voice-irodori-audiocpp-exp"]
    metrics = {
        "synthetic": True,
        "physical_playback_stop_measured": False,
        "microphone_opened": False,
        "concurrent_load": None,
    }

    async def synth(text, index):
        request = SpeechRequest(
            request_id=f"round-{index}",
            operation_id=f"round-{index}",
            session_id="reuse-synthetic",
            turn_id=f"round-{index}",
            generation_revision=1,
            speech_unit_sequence=1,
            voice_profile_id=profile["voice_profile_id"],
            model_revision=profile["model_revision"],
            reference_group_digest=profile["reference_group_digest"],
            synthesis_config_digest=profile["synthesis_config_digest"],
            speech_text=text,
        )
        start = time.monotonic()
        wav = await asyncio.wait_for(tts.synthesize(request, profile), 90)
        (output / f"output-{index}.wav").write_bytes(wav)
        return wav, (time.monotonic() - start) * 1000

    boundary = DeliveryBoundary()
    done = asyncio.Event()

    class Inference(FrameProcessor):
        async def process_frame(self, frame, direction):
            await super().process_frame(frame, direction)
            if isinstance(frame, LLMContextFrame):
                try:
                    answer, metrics["llm_ms"] = await complete(transcript)
                    wav, metrics["tts_first_playable_ms"] = await synth(answer, 1)
                    with wave.open(io.BytesIO(wav)) as audio:
                        metrics["audio_seconds"] = (
                            audio.getnframes() / audio.getframerate()
                        )
                        await self.push_frame(
                            EpochAudio(
                                audio=audio.readframes(audio.getnframes()),
                                sample_rate=audio.getframerate(),
                                num_channels=1,
                                epoch=1,
                                sequence=1,
                            ),
                            direction,
                        )
                    metrics["status"] = "ok"
                except Exception as exc:
                    metrics.update(status="error", error=type(exc).__name__)
                finally:
                    done.set()
            await self.push_frame(frame, direction)

    asr = None
    job = None
    try:
        asr, metrics["asr_load_ready_ms"] = await asyncio.to_thread(
            start_asr, asr_config
        )
        transcript, metrics["asr_final_ms"] = await asyncio.to_thread(
            transcribe, asr, Path(args.input)
        )
        metrics["asr_final_count"] = 1
        aggregators = LLMContextAggregatorPair(
            LLMContext([]),
            user_params=LLMUserAggregatorParams(
                user_turn_strategies=ExternalUserTurnStrategies(
                    enable_interruptions=False
                )
            ),
        )
        worker = PipelineWorker(
            Pipeline([aggregators.user(), Inference(), boundary]),
            params=PipelineParams(),
            enable_tracing=False,
            enable_rtvi=False,
            enable_turn_tracking=False,
            idle_timeout_secs=None,
        )
        runner = WorkerRunner(handle_sigint=False, handle_sigterm=False)
        await runner.add_workers(worker)
        job = asyncio.create_task(runner.run())
        await asyncio.wait_for(boundary.ready.wait(), 5)
        await worker.queue_frame(UserStartedSpeakingFrame())
        await worker.queue_frame(
            TranscriptionFrame(
                text=transcript,
                user_id="synthetic-asr",
                timestamp="2026-09-13T00:00:00Z",
                finalized=True,
            )
        )
        await worker.queue_frame(UserStoppedSpeakingFrame())
        await asyncio.wait_for(done.wait(), 180)
        await worker.queue_frame(EndFrame())
        await asyncio.wait_for(job, 10)
        job = None
        metrics["pipecat_final_contexts"] = boundary.contexts
        metrics["pipecat_audio_frames"] = len(boundary.frames)
        if boundary.contexts != 1 or len(boundary.frames) != 1:
            raise RuntimeError("pipecat_frame_count")
        if args.concurrent_load and metrics["status"] == "ok":
            start = time.monotonic()
            samples = []

            async def sample_memory():
                while True:
                    pids = [asr.process.pid, tts.process.pid] + (
                        [args.work_pid] if args.work_pid else []
                    )
                    try:
                        values = await asyncio.to_thread(
                            subprocess.check_output,
                            ["ps", "-o", "pid=,rss=", "-p", ",".join(map(str, pids))],
                            stderr=subprocess.DEVNULL,
                        )
                        samples.append(
                            {
                                "elapsed_ms": (time.monotonic() - start) * 1000,
                                "rss_bytes": {
                                    line.split()[0]: int(line.split()[1]) * 1024
                                    for line in values.decode().splitlines()
                                },
                            }
                        )
                    except subprocess.SubprocessError:
                        pass
                    await asyncio.sleep(0.25)

            sampler = asyncio.create_task(sample_memory())
            results = await asyncio.gather(
                asyncio.to_thread(transcribe, asr, Path(args.input)),
                complete("音声の検証を続けます．"),
                synth("わたしも，音声の検証を続けます．", 2),
                return_exceptions=True,
            )
            sampler.cancel()
            try:
                await sampler
            except asyncio.CancelledError:
                pass
            (output / "memory-samples.json").write_text(
                json.dumps(samples, indent=2) + "\n"
            )
            metrics["concurrent_load"] = {
                "sampled_sum_peak_rss_bytes": max(
                    (sum(s["rss_bytes"].values()) for s in samples), default=None
                ),
                "sampled_components": ["asr", "tts"]
                + (["work"] if args.work_pid else []),
                "submitted_per_component": 1,
                "queue_depth_measured": False,
                "elapsed_ms": (time.monotonic() - start) * 1000,
                "tasks": {
                    name: (
                        {"status": "error", "error": type(value).__name__}
                        if isinstance(value, BaseException)
                        else {"status": "ok", "elapsed_ms": value[1]}
                    )
                    for name, value in zip(("asr", "work", "tts"), results)
                },
            }
        if metrics["concurrent_load"] and any(
            row["status"] != "ok"
            for row in metrics["concurrent_load"]["tasks"].values()
        ):
            metrics["status"] = "error"
    except Exception as exc:
        metrics.update(status="error", error=type(exc).__name__)
    finally:
        if job is not None:
            job.cancel()
            try:
                await job
            except asyncio.CancelledError:
                pass
        await tts.stop()
        if asr:
            await asyncio.to_thread(asr.close)
        (output / "metrics.json").write_text(json.dumps(metrics, indent=2) + "\n")
        print(json.dumps(metrics), flush=True)
    return 0 if metrics.get("status") == "ok" else 1


if __name__ == "__main__":
    p = argparse.ArgumentParser()
    p.add_argument("--config", required=True)
    p.add_argument("--asr-config", required=True)
    p.add_argument("--input", required=True)
    p.add_argument("--output", required=True)
    p.add_argument("--concurrent-load", action="store_true")
    p.add_argument("--work-pid", type=int)
    raise SystemExit(asyncio.run(run(p.parse_args())))

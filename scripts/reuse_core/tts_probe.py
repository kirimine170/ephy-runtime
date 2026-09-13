"""Real worker cold/warm/cancel measurements；outputs remain outside Git．"""

from __future__ import annotations
import argparse
import asyncio
import hashlib
import io
import json
import subprocess
from pathlib import Path
import time
import wave

from apps.speech.schemas import SpeechRequest
from apps.speech.service import ProcessWorker, SpeechService


async def run(args):
    config = json.loads(Path(args.config).read_text())
    worker = ProcessWorker(
        config, command=[args.python, "-m", "apps.speech", "--worker"]
    )
    service = SpeechService(config, worker=worker, available=True)
    profile = service.profiles[args.profile]
    folder = Path(args.output)
    folder.mkdir(parents=True, exist_ok=True, mode=0o700)
    results = []
    units = [
        ("smoke-1", 1, "今日は検証用の短い音声です．"),
        ("smoke-2", 1, "Ephyの音声を続けて確認します．"),
    ]
    if args.corpus:
        corpus = json.loads(Path(args.corpus).read_text())
        units = [
            (case["id"], sequence, text)
            for case in corpus["cases"][: args.limit]
            for sequence, text in enumerate(case["units"], 1)
        ]
        if any(not 0 < len(text) <= 180 for _, _, text in units):
            raise ValueError("corpus_unit_exceeds_contract")
    else:
        units = units[: args.limit]
    if not units:
        raise ValueError("empty_corpus")

    def request(i, text):
        return SpeechRequest(
            request_id=f"reuse-{i}",
            operation_id=f"reuse-op-{i}",
            session_id="reuse-synthetic",
            turn_id=f"reuse-turn-{i}",
            generation_revision=1,
            speech_unit_sequence=1,
            voice_profile_id=args.profile,
            model_revision=profile["model_revision"],
            reference_group_digest=profile["reference_group_digest"],
            synthesis_config_digest=profile["synthesis_config_digest"],
            speech_text=text,
        )

    async def synth(label, i, text):
        start = time.monotonic()
        peak_rss = 0

        async def sample_rss():
            nonlocal peak_rss
            while True:
                if worker.process is not None:
                    try:
                        value = await asyncio.to_thread(
                            subprocess.check_output,
                            ["ps", "-o", "rss=", "-p", str(worker.process.pid)],
                            stderr=subprocess.DEVNULL,
                        )
                        peak_rss = max(peak_rss, int(value) * 1024)
                    except (ValueError, subprocess.SubprocessError):
                        pass
                await asyncio.sleep(0.25)

        sampler = asyncio.create_task(sample_rss())
        try:
            wav = await asyncio.wait_for(
                worker.synthesize(request(i, text), profile), args.timeout
            )
            elapsed = time.monotonic() - start
            with wave.open(io.BytesIO(wav)) as f:
                duration = f.getnframes() / f.getframerate()
            (folder / f"{label}-{i}.wav").write_bytes(wav)
            return {
                "condition": label,
                "case": i,
                "first_playable_ms": elapsed * 1000,
                "audio_seconds": duration,
                "rtf": elapsed / duration,
                "worker_pid": worker.process.pid,
                "status": "ok",
                "sha256": hashlib.sha256(wav).hexdigest(),
                "sampled_worker_peak_rss_bytes": peak_rss or None,
            }
        except Exception as exc:
            return {
                "condition": label,
                "case": i,
                "status": "error",
                "error_code": str(exc)
                if str(exc).startswith("tts_")
                else type(exc).__name__,
                "elapsed_ms": (time.monotonic() - start) * 1000,
            }
        finally:
            sampler.cancel()
            try:
                await sampler
            except asyncio.CancelledError:
                pass

    try:
        for i, (case, sequence, text) in enumerate(units):
            value = await synth("cold" if i == 0 else "warm", i, text)
            value.update(
                case_id=case,
                unit_sequence=sequence,
                text_sha256=hashlib.sha256(text.encode()).hexdigest(),
            )
            results.append(value)
            print(json.dumps(value), flush=True)
            if value["status"] != "ok":
                break
        if args.cancel and results[-1]["status"] == "ok":
            job = asyncio.create_task(
                worker.synthesize(
                    request(
                        100,
                        "これは生成の取り消しと次の発話の復帰を確認するための長めの合成文章です．",
                    ),
                    profile,
                )
            )
            await asyncio.sleep(0.2)
            process, temporary = worker.process, Path(worker.temporary.name)
            start = time.monotonic()
            job.cancel()
            try:
                await job
            except asyncio.CancelledError:
                pass
            value = {
                "condition": "cancel",
                "reap_ms": (time.monotonic() - start) * 1000,
                "old_returncode": process.returncode,
                "temporary_removed": not temporary.exists(),
            }
            results.append(value)
            print(json.dumps(value), flush=True)
            value = await synth("after_cancel", 101, units[0][2])
            results.append(value)
            print(json.dumps(value), flush=True)
    finally:
        await worker.stop()
        (folder / "metrics.json").write_text(
            json.dumps(
                {
                    "synthetic": True,
                    "profile": args.profile,
                    "planned_units": len(units),
                    "corpus_sha256": hashlib.sha256(
                        Path(args.corpus).read_bytes()
                    ).hexdigest()
                    if args.corpus
                    else None,
                    "load_ready_ms": None,
                    "load_note": "Model load is lazy and included in cold first playable；no separate ready signal．",
                    "results": results,
                },
                indent=2,
            )
            + "\n"
        )
    return 0 if all(v.get("status", "ok") == "ok" for v in results) else 1


if __name__ == "__main__":
    import sys

    parser = argparse.ArgumentParser()
    parser.add_argument("--config", required=True)
    parser.add_argument("--python", default=sys.executable)
    parser.add_argument("--profile", default="voice-irodori-audiocpp-exp")
    parser.add_argument("--output", required=True)
    parser.add_argument("--timeout", type=float, default=60)
    parser.add_argument("--limit", type=int, default=2)
    parser.add_argument("--corpus")
    parser.add_argument("--cancel", action="store_true")
    raise SystemExit(asyncio.run(run(parser.parse_args())))

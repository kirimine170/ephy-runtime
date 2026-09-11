#!/usr/bin/env python3
"""Generate one private provider side of the listening set．

Run this file with the provider's pinned inference Python．Provider names and
unblinded filenames are confined to the private staging directory．
"""

from __future__ import annotations

import argparse
import json
import os
from pathlib import Path
import sys
import tempfile
import threading
import time
from typing import Any, Callable


RUNTIME = Path(__file__).resolve().parents[2]
if str(RUNTIME) not in sys.path:
    sys.path.insert(0, str(RUNTIME))

from apps.speech.irodori import MODEL_REVISION as IRODORI_MODEL_REVISION, IrodoriAdapter
from apps.speech.qwen import MODEL_REVISION as QWEN_MODEL_REVISION, QwenAdapter
from apps.speech.schemas import SpeechRequest
from apps.speech.service import provider_config
from packages.speech_assets import read_private_json_file
from scripts.voice_ab.common import join_wavs, load_corpus, sha256, wav_duration


def _write_private(path: Path, body: bytes) -> None:
    descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    with os.fdopen(descriptor, "wb") as target:
        target.write(body)


class MemoryMonitor:
    def __init__(self) -> None:
        import psutil
        self.process = psutil.Process()
        self.peak = 0
        self.stop = threading.Event()
        self.thread = threading.Thread(target=self._run, daemon=True)

    def _run(self) -> None:
        while not self.stop.wait(0.02):
            try:
                rss = self.process.memory_info().rss
                for child in self.process.children(recursive=True):
                    rss += child.memory_info().rss
                self.peak = max(self.peak, rss)
            except Exception:
                pass

    def __enter__(self) -> MemoryMonitor:
        self.thread.start()
        return self

    def __exit__(self, *_: object) -> None:
        self.stop.set()
        self.thread.join(timeout=1)
        try:
            self.peak = max(self.peak, self.process.memory_info().rss)
        except Exception:
            pass


def _profile(config: dict[str, Any], provider: str) -> dict[str, Any]:
    profiles = config.get("profiles", [])
    matches = [item for item in profiles if isinstance(item, dict) and item.get("provider") == provider]
    if len(matches) != 1:
        raise ValueError("evaluation_profile_unavailable")
    return matches[0]


def _irodori(config_path: Path, temporary: Path) -> tuple[Any, dict[str, Any], Callable[[str, int], SpeechRequest]]:
    config = read_private_json_file(config_path)
    raw = _profile(config, "irodori-tts")
    adapter_config = provider_config(config, "irodori-tts")
    adapter_config["temporary_path"] = str(temporary)
    adapter = IrodoriAdapter(adapter_config)

    def request(text: str, sequence: int) -> SpeechRequest:
        return SpeechRequest(
            request_id=f"request-eval-{sequence}", operation_id="operation-eval",
            session_id="session-eval", turn_id="turn-eval", generation_revision=1,
            speech_unit_sequence=sequence, voice_profile_id=raw["voice_profile_id"],
            model_revision=IRODORI_MODEL_REVISION,
            reference_group_digest=raw["reference_group_digest"], speech_text=text,
            affect="neutral", intensity=1.0, pace=1.0, pitch_hint=0.0,
            volume=1.0, pause_style="natural", interruptible=True,
        )

    return adapter, raw, request


def _qwen(config_path: Path, reference_path: Path) -> tuple[Any, dict[str, Any], Callable[[str, int], SpeechRequest]]:
    import soundfile
    config = read_private_json_file(config_path)
    adapter_config = provider_config(config, "qwen3-tts")
    profiles = [item for item in config.get("profiles", [])
                if isinstance(item, dict) and item.get("provider") == "qwen3-tts"]
    if len(profiles) == 1:
        raw = profiles[0]
        adapter = QwenAdapter(adapter_config)
    elif not profiles:
        samples, sample_rate = soundfile.read(reference_path, dtype="float32", always_2d=True)
        mono = samples.mean(axis=1)
        prompt: list[Any] = []
        adapter: QwenAdapter

        def load_prompt(_: str) -> Any:
            if not prompt:
                prompt.extend(adapter.model.create_voice_clone_prompt(
                    ref_audio=(mono, sample_rate), ref_text=None, x_vector_only_mode=True,
                ))
            return prompt

        adapter = QwenAdapter(adapter_config, prompt_loader=load_prompt)
        raw = {
            "voice_profile_id": "voice-qwen-evaluation-only",
            "provider": "qwen3-tts",
            "model_revision": QWEN_MODEL_REVISION,
            "language": "ja-JP",
            "clone_prompt_digest": "e" * 64,
            "provenance_id": "prov_" + "0" * 32,
        }
    else:
        raise ValueError("evaluation_profile_unavailable")

    def request(text: str, sequence: int) -> SpeechRequest:
        return SpeechRequest(
            request_id=f"request-eval-{sequence}", operation_id="operation-eval",
            session_id="session-eval", turn_id="turn-eval", generation_revision=1,
            speech_unit_sequence=sequence, voice_profile_id=raw["voice_profile_id"],
            model_revision=QWEN_MODEL_REVISION, clone_prompt_digest=raw["clone_prompt_digest"],
            speech_text=text, affect="neutral", intensity=1.0, pace=1.0,
            pitch_hint=0.0, volume=1.0, pause_style="natural", interruptible=True,
        )

    return adapter, raw, request


def _load(args: argparse.Namespace, temporary: Path) -> tuple[Any, dict[str, Any], Callable[[str, int], SpeechRequest]]:
    if args.provider == "irodori-tts":
        return _irodori(args.config, temporary)
    return _qwen(args.config, args.reference)


def _warm(adapter: Any, provider: str, profile: dict[str, Any]) -> None:
    adapter._load()
    if provider == "qwen3-tts":
        adapter._prompt(profile["clone_prompt_digest"], profile.get("provenance_id"))


def _cancel_probe(args: argparse.Namespace) -> int:
    with tempfile.TemporaryDirectory(prefix="ephy-voice-ab-cancel-") as name:
        os.chmod(name, 0o700)
        adapter, profile, request = _load(args, Path(name))
        _warm(adapter, args.provider, profile)
        print("READY", flush=True)
        probe = "停止確認のための長い発話です．" * 10
        print("SYNTHESIS_START", flush=True)
        adapter.synthesize(request(probe[:180], 1), profile)
        print("UNEXPECTED_COMPLETION", flush=True)
    return 2


def _generate(args: argparse.Namespace) -> int:
    corpus = load_corpus(args.corpus)
    args.output.mkdir(mode=0o700, parents=False, exist_ok=False)
    metrics: list[dict[str, Any]] = []
    sequence = 0
    with tempfile.TemporaryDirectory(prefix="ephy-voice-ab-") as name:
        os.chmod(name, 0o700)
        adapter, profile, make_request = _load(args, Path(name))
        for case in corpus["cases"]:
            units: list[bytes] = []
            unit_metrics: list[dict[str, Any]] = []
            case_start = time.perf_counter()
            peak = 0
            for text in case["units"]:
                sequence += 1
                started = time.perf_counter()
                with MemoryMonitor() as memory:
                    body = adapter.synthesize(make_request(text, sequence), profile)
                elapsed = time.perf_counter() - started
                peak = max(peak, memory.peak)
                units.append(body)
                unit_metrics.append({
                    "sequence": sequence,
                    "elapsed_ms": round(elapsed * 1000, 3),
                    "audio_duration_ms": round(wav_duration(body) * 1000, 3),
                    "sha256": sha256(body),
                })
            joined = join_wavs(units)
            target = args.output / f"{case['id']}.wav"
            _write_private(target, joined)
            total = time.perf_counter() - case_start
            duration = wav_duration(joined)
            metrics.append({
                "case_id": case["id"],
                "condition": case["generation_condition"],
                "first_audio_ms": unit_metrics[0]["elapsed_ms"],
                "completion_ms": round(total * 1000, 3),
                "audio_duration_ms": round(duration * 1000, 3),
                "real_time_factor": round(total / duration, 6),
                "peak_rss_bytes": peak,
                "expected_units": len(case["units"]),
                "completed_units": len(unit_metrics),
                "sequence_order": [item["sequence"] for item in unit_metrics],
                "wav_sha256": sha256(joined),
                "unit_metrics": unit_metrics,
            })
            print(json.dumps({"case_id": case["id"], "completed": len(metrics)}, ensure_ascii=True), flush=True)
    report = {"schema_version": 1, "provider": args.provider, "cases": metrics}
    _write_private(args.output / "metrics.json", json.dumps(report, ensure_ascii=False, indent=2).encode("utf-8") + b"\n")
    return 0


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--provider", required=True, choices=("qwen3-tts", "irodori-tts"))
    parser.add_argument("--config", required=True, type=Path)
    parser.add_argument("--reference", required=True, type=Path)
    parser.add_argument("--corpus", required=True, type=Path)
    parser.add_argument("--output", type=Path)
    parser.add_argument("--cancel-probe", action="store_true")
    args = parser.parse_args()
    if args.cancel_probe:
        return _cancel_probe(args)
    if args.output is None or not args.output.is_absolute():
        raise ValueError("invalid_output")
    return _generate(args)


if __name__ == "__main__":
    raise SystemExit(main())

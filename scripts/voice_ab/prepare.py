#!/usr/bin/env python3
"""Prepare a private，provider-blind C0.3.2 listening package．"""

from __future__ import annotations

import argparse
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import secrets
import signal
import subprocess
import sys
import time
from typing import Any


RUNTIME = Path(__file__).resolve().parents[2]
if str(RUNTIME) not in sys.path:
    sys.path.insert(0, str(RUNTIME))

from scripts.voice_ab.common import AXES, load_corpus, metric_summary, read_json


def write_private(path: Path, body: bytes) -> None:
    descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    with os.fdopen(descriptor, "wb") as target:
        target.write(body)


def file_hash(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as source:
        for piece in iter(lambda: source.read(1024 * 1024), b""):
            digest.update(piece)
    return digest.hexdigest()


def qwen_condition(config: dict[str, Any]) -> str:
    profiles = [item for item in config.get("profiles", [])
                if isinstance(item, dict) and item.get("provider") == "qwen3-tts"]
    if len(profiles) == 1 and len(str(profiles[0].get("clone_prompt_digest", ""))) == 64:
        return "pinned Qwen Base adapter with an ICL clone prompt made from the shared reference and user-verified transcript"
    return "pinned Qwen Base adapter with official x-vector-only prompt from the shared reference；no reference transcript was configured"


def command(args: argparse.Namespace, provider: str, output: Path | None = None,
            cancel: bool = False) -> list[str]:
    python = args.qwen_python if provider == "qwen3-tts" else args.irodori_python
    config = args.qwen_config if provider == "qwen3-tts" else args.irodori_config
    result = [str(python), str(Path(__file__).with_name("worker.py")),
              "--provider", provider, "--config", str(config),
              "--reference", str(args.reference), "--corpus", str(args.corpus)]
    if cancel:
        result.append("--cancel-probe")
    else:
        result.extend(("--output", str(output)))
    return result


def cancel_probe(args: argparse.Namespace, provider: str) -> dict[str, Any]:
    process = subprocess.Popen(command(args, provider, cancel=True), cwd=RUNTIME,
                               stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
                               text=True, bufsize=1, start_new_session=True)
    assert process.stdout is not None
    deadline = time.monotonic() + 300
    ready = False
    while time.monotonic() < deadline:
        line = process.stdout.readline().strip()
        if line == "READY":
            ready = True
        if line == "SYNTHESIS_START" and ready:
            break
        if process.poll() is not None:
            raise RuntimeError("cancel_probe_start_failed")
    else:
        os.killpg(process.pid, signal.SIGKILL)
        raise RuntimeError("cancel_probe_start_timeout")
    time.sleep(0.35)
    started = time.perf_counter()
    os.killpg(process.pid, signal.SIGTERM)
    forced = False
    try:
        process.wait(timeout=1.0)
    except subprocess.TimeoutExpired:
        forced = True
        os.killpg(process.pid, signal.SIGKILL)
        process.wait(timeout=2.0)
    elapsed = (time.perf_counter() - started) * 1000
    return {"cancel_to_process_stop_ms": round(elapsed, 3), "forced_kill": forced,
            "stale_chunk_count": 0, "measurement": "disposable inference process termination proxy"}


def run_generation(args: argparse.Namespace, provider: str, output: Path, ordinal: int) -> None:
    print(json.dumps({"phase": "generation", "configuration": ordinal, "of": 2}), flush=True)
    result = subprocess.run(command(args, provider, output), cwd=RUNTIME, check=False)
    if result.returncode != 0:
        raise RuntimeError("provider_generation_failed")


def provider_summary(report: dict[str, Any], cancel: dict[str, Any]) -> dict[str, Any]:
    cases = report["cases"]
    warm = [item for item in cases if item["condition"] == "warm"]
    structural = []
    for item in cases:
        expected = item["expected_units"]
        sequences = item["sequence_order"]
        if item["completed_units"] != expected or sequences != list(range(sequences[0], sequences[0] + expected)):
            structural.append(item["case_id"])
    return {
        "warm_first_audio_ms": metric_summary([item["first_audio_ms"] for item in warm]),
        "real_time_factor": metric_summary([item["real_time_factor"] for item in warm]),
        "completion_ms": metric_summary([item["completion_ms"] for item in warm]),
        "memory_peak_rss_bytes": max(item["peak_rss_bytes"] for item in cases),
        "cancel": cancel,
        "stale_chunk_count": cancel["stale_chunk_count"],
        "long_text_structural_violations": structural,
        "long_text_check_scope": "speech-unit completion，duplication and monotonic sequence；not lexical ASR",
    }


def copy_assets(source: Path, target: Path) -> None:
    for name in ("index.html", "review.js", "style.css"):
        body = (source / name).read_bytes()
        write_private(target / name, body)


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--output", required=True, type=Path)
    parser.add_argument("--reference", required=True, type=Path)
    parser.add_argument("--qwen-python", required=True, type=Path)
    parser.add_argument("--qwen-config", required=True, type=Path)
    parser.add_argument("--irodori-python", required=True, type=Path)
    parser.add_argument("--irodori-config", required=True, type=Path)
    parser.add_argument("--evaluator-python", required=True, type=Path)
    parser.add_argument("--evaluator-model", required=True, type=Path)
    parser.add_argument("--corpus", type=Path, default=Path(__file__).with_name("corpus.json"))
    args = parser.parse_args()
    if not args.output.is_absolute() or args.output.exists() or RUNTIME in args.output.parents:
        raise ValueError("output_must_be_new_private_directory_outside_git")
    corpus = load_corpus(args.corpus)
    input_hashes = {str(path): file_hash(path) for path in (args.qwen_config, args.irodori_config)}
    args.output.mkdir(mode=0o700, parents=False, exist_ok=False)
    raw = args.output / "private-raw"
    public = args.output / "review"
    audio = public / "audio"
    for directory in (raw, public, audio):
        directory.mkdir(mode=0o700)

    cancel = {}
    for ordinal, provider in enumerate(("qwen3-tts", "irodori-tts"), 1):
        print(json.dumps({"phase": "cancel_probe", "configuration": ordinal, "of": 2}), flush=True)
        cancel[provider] = cancel_probe(args, provider)

    qwen_raw = raw / "qwen"
    irodori_raw = raw / "irodori"
    run_generation(args, "qwen3-tts", qwen_raw, 1)
    run_generation(args, "irodori-tts", irodori_raw, 2)
    reports = {
        "qwen3-tts": read_json(qwen_raw / "metrics.json"),
        "irodori-tts": read_json(irodori_raw / "metrics.json"),
    }
    if input_hashes != {str(path): file_hash(path) for path in (args.qwen_config, args.irodori_config)}:
        raise RuntimeError("input_configuration_changed")

    speaker_output = raw / "speaker-metrics.json"
    subprocess.run([
        str(args.evaluator_python), str(Path(__file__).with_name("speaker_metrics.py")),
        "--model", str(args.evaluator_model), "--reference", str(args.reference),
        "--corpus", str(args.corpus), "--qwen", str(qwen_raw),
        "--irodori", str(irodori_raw), "--output", str(speaker_output),
    ], cwd=RUNTIME, check=True)
    speaker = read_json(speaker_output)

    random = secrets.SystemRandom()
    ordered = list(corpus["cases"])
    random.shuffle(ordered)
    assignments = ["qwen3-tts"] * 12 + ["irodori-tts"] * 12
    random.shuffle(assignments)
    public_cases = []
    key_cases = []
    raw_dirs = {"qwen3-tts": qwen_raw, "irodori-tts": irodori_raw}
    by_provider = {provider: {item["case_id"]: item for item in report["cases"]}
                   for provider, report in reports.items()}
    for index, (case, provider_a) in enumerate(zip(ordered, assignments), 1):
        provider_b = "irodori-tts" if provider_a == "qwen3-tts" else "qwen3-tts"
        filenames = {label: secrets.token_hex(16) + ".wav" for label in ("A", "B")}
        for label, provider in (("A", provider_a), ("B", provider_b)):
            source = raw_dirs[provider] / f"{case['id']}.wav"
            write_private(audio / filenames[label], source.read_bytes())
        public_cases.append({
            "index": index, "case_id": case["id"], "category": case["category"],
            "text": " ".join(case["units"]),
            "audio_a": "audio/" + filenames["A"], "audio_b": "audio/" + filenames["B"],
        })
        key_cases.append({
            "case_id": case["id"], "A": provider_a, "B": provider_b,
            "generation_condition": case["generation_condition"],
            "metrics": {"A": by_provider[provider_a][case["id"]],
                        "B": by_provider[provider_b][case["id"]]},
        })
    reference_name = secrets.token_hex(16) + ".wav"
    write_private(public / reference_name, args.reference.read_bytes())
    manifest = {
        "schema_version": 1, "suite_id": corpus["suite_id"], "case_count": len(public_cases),
        "reference_audio": reference_name, "axes": list(AXES),
        "cases": public_cases,
    }
    write_private(public / "manifest.json", json.dumps(manifest, ensure_ascii=False, separators=(",", ":")).encode("utf-8"))
    copy_assets(Path(__file__).parent, public)
    server_token = secrets.token_urlsafe(32)
    write_private(args.output / "server-token", server_token.encode("ascii"))
    machine = {provider: provider_summary(reports[provider], cancel[provider]) for provider in reports}
    for provider in machine:
        machine[provider]["continuous_turn_voice"] = speaker["providers"][provider]
    key = {
        "schema_version": 1, "created_at": datetime.now(timezone.utc).isoformat(),
        "suite_id": corpus["suite_id"], "reference_sha256": file_hash(args.reference),
        "default_configuration_changed": False,
        "qwen_condition": qwen_condition(read_json(args.qwen_config)),
        "irodori_condition": "pinned Irodori Anime adapter with the shared reference group",
        "style_condition": "neutral provider defaults，pace 1.0，volume 1.0，identical input text and unit boundaries",
        "cases": key_cases, "machine_metrics": machine,
        "result_classes": [
            "Irodoriをlive default候補にできる．",
            "Irodoriは高品質だが遅いため，C0.4で同じ声の事前生成フィラーと組み合わせて再評価する．",
            "Qwenを維持し，Irodoriの学習や追加最適化は行わない．"
        ],
    }
    write_private(args.output / "private-key.json", json.dumps(key, ensure_ascii=False, indent=2).encode("utf-8") + b"\n")
    print(json.dumps({"status": "ready", "cases": len(public_cases),
                      "review_root": str(public), "token": server_token}, ensure_ascii=True), flush=True)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

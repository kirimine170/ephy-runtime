from __future__ import annotations

import hashlib
import io
import json
import math
import os
from pathlib import Path
import re
import statistics
import wave


AXES = (
    "cuteness",
    "ephy_identity",
    "reference_match",
    "naturalness",
    "emotion_fit",
    "japanese_pronunciation",
    "low_fatigue",
)
PREFERENCES = frozenset({"strong_a", "slight_a", "tie", "slight_b", "strong_b"})
OVERALL = frozenset({"A", "B", "tie", "neither"})
ID = re.compile(r"^[a-z0-9][a-z0-9-]{0,63}$")


def read_json(path: Path) -> dict:
    value = json.loads(path.read_text(encoding="utf-8"))
    if not isinstance(value, dict):
        raise ValueError("invalid_json_object")
    return value


def load_corpus(path: Path) -> dict:
    corpus = read_json(path)
    cases = corpus.get("cases")
    if corpus.get("schema_version") != 1 or not isinstance(cases, list) or len(cases) != 24:
        raise ValueError("invalid_corpus")
    seen: set[str] = set()
    cold = 0
    for case in cases:
        if not isinstance(case, dict) or not ID.fullmatch(str(case.get("id", ""))) or case["id"] in seen:
            raise ValueError("invalid_corpus")
        seen.add(case["id"])
        units = case.get("units")
        if not isinstance(units, list) or not 1 <= len(units) <= 8:
            raise ValueError("invalid_corpus")
        if any(not isinstance(text, str) or not text.strip() or len(text) > 180 or len(text.encode("utf-8")) > 720 for text in units):
            raise ValueError("invalid_corpus")
        if case.get("generation_condition") not in {"cold", "warm"}:
            raise ValueError("invalid_corpus")
        cold += case["generation_condition"] == "cold"
    if cold != 1:
        raise ValueError("invalid_corpus")
    return corpus


def wav_info(body: bytes) -> tuple[int, int, bytes]:
    with wave.open(io.BytesIO(body), "rb") as source:
        if source.getnchannels() != 1 or source.getsampwidth() != 2:
            raise ValueError("invalid_wav")
        rate = source.getframerate()
        frames = source.getnframes()
        pcm = source.readframes(frames)
        if not 8000 <= rate <= 48000 or not 0 < frames <= rate * 60 or len(pcm) != frames * 2:
            raise ValueError("invalid_wav")
        return rate, frames, pcm


def join_wavs(bodies: list[bytes], pause_ms: int = 140) -> bytes:
    if not bodies:
        raise ValueError("invalid_wav")
    decoded = [wav_info(body) for body in bodies]
    rates = {item[0] for item in decoded}
    if len(rates) != 1:
        raise ValueError("inconsistent_wav_rate")
    rate = decoded[0][0]
    silence = b"\0\0" * round(rate * pause_ms / 1000)
    pcm = silence.join(item[2] for item in decoded)
    output = io.BytesIO()
    with wave.open(output, "wb") as target:
        target.setnchannels(1)
        target.setsampwidth(2)
        target.setframerate(rate)
        target.writeframes(pcm)
    return output.getvalue()


def wav_duration(body: bytes) -> float:
    rate, frames, _ = wav_info(body)
    return frames / rate


def sha256(body: bytes) -> str:
    return hashlib.sha256(body).hexdigest()


def percentile(values: list[float], percent: float) -> float:
    if not values or not math.isfinite(percent) or not 0 <= percent <= 100:
        raise ValueError("invalid_percentile")
    ordered = sorted(values)
    point = (len(ordered) - 1) * percent / 100
    left = math.floor(point)
    right = math.ceil(point)
    if left == right:
        return ordered[left]
    return ordered[left] * (right - point) + ordered[right] * (point - left)


def metric_summary(values: list[float]) -> dict[str, float]:
    return {
        "p50": round(statistics.median(values), 3),
        "p95": round(percentile(values, 95), 3),
        "mean": round(statistics.mean(values), 3),
    }


def validate_vote(value: object, case_ids: set[str]) -> dict:
    if not isinstance(value, dict) or set(value) - {"case_id", "axes", "overall", "note"}:
        raise ValueError("invalid_vote")
    case_id = value.get("case_id")
    axes = value.get("axes")
    note = value.get("note", "")
    if case_id not in case_ids or not isinstance(axes, dict) or set(axes) != set(AXES):
        raise ValueError("invalid_vote")
    if any(choice not in PREFERENCES for choice in axes.values()):
        raise ValueError("invalid_vote")
    if value.get("overall") not in OVERALL or not isinstance(note, str) or len(note.encode("utf-8")) > 2000:
        raise ValueError("invalid_vote")
    return {"case_id": case_id, "axes": {axis: axes[axis] for axis in AXES},
            "overall": value["overall"], "note": note}


def private_directory(path: Path, *, create: bool = False) -> None:
    if not path.is_absolute() or path.is_symlink():
        raise ValueError("invalid_private_directory")
    if create:
        path.mkdir(mode=0o700, parents=False, exist_ok=False)
    info = path.stat()
    if not path.is_dir() or info.st_uid != os.getuid() or info.st_mode & 0o077:
        raise ValueError("invalid_private_directory")

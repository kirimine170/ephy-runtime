#!/usr/bin/env python3
"""Local WavLM proxy for continuous-turn speaker consistency．"""

from __future__ import annotations

import argparse
import itertools
import json
from pathlib import Path
import statistics

import librosa
import numpy as np
import torch
import torch.nn.functional as functional
from transformers import Wav2Vec2FeatureExtractor, WavLMForXVector


RATE = 16_000


def audio(path: Path) -> np.ndarray:
    samples, _ = librosa.load(path, sr=RATE, mono=True)
    trimmed, _ = librosa.effects.trim(samples.astype(np.float32), top_db=40)
    if len(trimmed) < RATE // 2:
        raise ValueError("audio_too_short")
    peak = float(np.max(np.abs(trimmed)))
    return trimmed / peak if peak else trimmed


def embedding(samples: np.ndarray, extractor: Any, model: Any) -> torch.Tensor:
    inputs = extractor(samples, sampling_rate=RATE, return_tensors="pt")
    with torch.inference_mode():
        value = model(**inputs).embeddings[0]
    return functional.normalize(value.float(), dim=-1).cpu()


def cosine(left: torch.Tensor, right: torch.Tensor) -> float:
    return float(torch.dot(left, right).item())


def summary(values: list[float]) -> dict[str, float]:
    return {"min": min(values), "mean": statistics.mean(values),
            "median": statistics.median(values), "max": max(values)}


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--model", required=True, type=Path)
    parser.add_argument("--reference", required=True, type=Path)
    parser.add_argument("--corpus", required=True, type=Path)
    parser.add_argument("--qwen", required=True, type=Path)
    parser.add_argument("--irodori", required=True, type=Path)
    parser.add_argument("--output", required=True, type=Path)
    args = parser.parse_args()
    corpus = json.loads(args.corpus.read_text(encoding="utf-8"))
    ids = [item["id"] for item in corpus["cases"] if item.get("stability_group") == "repeat-01"]
    if len(ids) < 3:
        raise ValueError("stability_set_too_small")
    extractor = Wav2Vec2FeatureExtractor.from_pretrained(args.model, local_files_only=True)
    model = WavLMForXVector.from_pretrained(args.model, local_files_only=True)
    model.eval()
    reference = audio(args.reference)
    window = min(5 * RATE, len(reference) // 3)
    starts = np.linspace(0, len(reference) - window, num=3, dtype=int)
    references = [embedding(reference[start:start + window], extractor, model) for start in starts]
    centroid = functional.normalize(torch.stack(references).mean(dim=0), dim=-1)
    report = {"schema_version": 1, "evaluator": "WavLMForXVector local proxy",
              "note": "Relative consistency proxy only；not a human listening score or universal threshold．",
              "providers": {}}
    for provider, directory in (("qwen3-tts", args.qwen), ("irodori-tts", args.irodori)):
        values = [embedding(audio(directory / f"{case_id}.wav"), extractor, model) for case_id in ids]
        pairwise = [cosine(values[left], values[right])
                    for left, right in itertools.combinations(range(len(values)), 2)]
        to_reference = [cosine(value, centroid) for value in values]
        report["providers"][provider] = {
            "case_ids": ids,
            "continuous_turn_pairwise_cosine": summary(pairwise),
            "continuous_turn_to_reference_cosine": summary(to_reference),
        }
    body = json.dumps(report, ensure_ascii=False, indent=2).encode("utf-8") + b"\n"
    descriptor = __import__("os").open(args.output, __import__("os").O_WRONLY | __import__("os").O_CREAT | __import__("os").O_EXCL, 0o600)
    with __import__("os").fdopen(descriptor, "wb") as target:
        target.write(body)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

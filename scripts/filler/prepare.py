#!/usr/bin/env python3
"""Generate the finite C0.4 set with the pinned Irodori profile．Never approve it．"""
from __future__ import annotations

import argparse
import hashlib
import json
import os
from pathlib import Path
import sys
import tempfile

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT))

from apps.speech.schemas import public_profile
from apps.speech.service import synthesis_config_digest
from packages.speech_assets import read_private_json_file
from scripts.voice_ab.worker import _irodori, _write_private
from scripts.voice_ab.common import wav_duration

CANDIDATES = {"hesitation_etto": "えっと", "hesitation_eeto": "ええと"}


def prepare(config: Path, output: Path) -> None:
    # Existing consent and immutable reference groups are enforced by the adapter．
    output.mkdir(mode=0o700, parents=False, exist_ok=False)
    with tempfile.TemporaryDirectory(prefix="ephy-c04-") as temporary:
        os.chmod(temporary, 0o700)
        adapter, raw, request = _irodori(config, Path(temporary))
        profile = public_profile(raw, available=True)
        profile["synthesis_config_digest"] = synthesis_config_digest(read_private_json_file(config), "irodori-tts")
        assets = []
        for index, (candidate, phrase) in enumerate(CANDIDATES.items(), 1):
            audio = adapter.synthesize(request(phrase, index), raw)
            filename = candidate + ".wav"
            _write_private(output / filename, audio)
            assets.append({"candidate": candidate, "file": filename, "sha256": hashlib.sha256(audio).hexdigest(),
                           "duration_ms": round(wav_duration(audio) * 1000, 3), "approved": False})
            print(json.dumps({"kind": "candidate_generated", "duration_ms": assets[-1]["duration_ms"]}), flush=True)
        # A synthetic transition sentence supports human voice／handoff checks．
        answer = adapter.synthesize(request("三角形の内角の和は，百八十度です．", 3), raw)
        _write_private(output / "review_answer.wav", answer)
    manifest = {"schema_version": 1, "enabled": False, "headphones_confirmed": False, "profile": profile, "assets": assets}
    _write_private(output / "manifest.json", (json.dumps(manifest, ensure_ascii=False, indent=2) + "\n").encode())


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--config", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    prepare(args.config.resolve(), args.output.absolute())

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

CANDIDATES = {"hesitation_etto": "えっと", "hesitation_eeto": "ええと", "backchannel_un": "うん", "backchannel_hai": "はい",
              "backchannel_gomen_iiyo": "ごめんごめん，いいよ？"}


def candidate_kind(candidate: str) -> str:
    return "backchannel" if candidate.startswith("backchannel_") else "hesitation"


def candidate_max_duration_ms(candidate: str) -> int:
    return 3000 if candidate_kind(candidate) == "backchannel" else 1500


def prepare(config: Path, output: Path, base_bundle: Path | None = None) -> None:
    # Existing consent and immutable reference groups are enforced by the adapter．
    output.mkdir(mode=0o700, parents=False, exist_ok=False)
    with tempfile.TemporaryDirectory(prefix="ephy-c04-") as temporary:
        os.chmod(temporary, 0o700)
        adapter, raw, request = _irodori(config, Path(temporary))
        profile = public_profile(raw, available=True)
        profile["synthesis_config_digest"] = synthesis_config_digest(read_private_json_file(config), "irodori-tts")
        base = read_private_json_file(base_bundle / "manifest.json") if base_bundle else None
        if base and base["profile"] != profile:
            raise ValueError("filler_profile_mismatch")
        assets = []
        for index, (candidate, phrase) in enumerate(CANDIDATES.items(), 1):
            previous = next((a for a in base["assets"] if a["candidate"] == candidate), None) if base else None
            if previous:
                if previous["file"] != candidate + ".wav":
                    raise ValueError("filler_asset_invalid")
                audio = (base_bundle / previous["file"]).read_bytes()
                if hashlib.sha256(audio).hexdigest() != previous["sha256"]:
                    raise ValueError("filler_asset_changed")
                _write_private(output / previous["file"], audio)
                assets.append(previous.copy())
                continue
            audio = adapter.synthesize(request(phrase, index), raw)
            filename = candidate + ".wav"
            _write_private(output / filename, audio)
            assets.append({"candidate": candidate, "file": filename, "sha256": hashlib.sha256(audio).hexdigest(),
                           "duration_ms": round(wav_duration(audio) * 1000, 3), "approved": False})
            print(json.dumps({"kind": "candidate_generated", "duration_ms": assets[-1]["duration_ms"]}), flush=True)
        # A synthetic transition sentence supports human voice／handoff checks．
        answer = (base_bundle / "review_answer.wav").read_bytes() if base_bundle else adapter.synthesize(request("三角形の内角の和は，百八十度です．", len(CANDIDATES) + 1), raw)
        _write_private(output / "review_answer.wav", answer)
    manifest = {"schema_version": 1, "enabled": False, "headphones_confirmed": False, "profile": profile, "assets": assets}
    _write_private(output / "manifest.json", (json.dumps(manifest, ensure_ascii=False, indent=2) + "\n").encode())


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--config", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--base-bundle", type=Path, help="Preserve reviewed immutable clips with the same profile")
    args = parser.parse_args()
    prepare(args.config.resolve(), args.output.absolute(), args.base_bundle.resolve() if args.base_bundle else None)

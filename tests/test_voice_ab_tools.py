from __future__ import annotations

import json
from pathlib import Path

import pytest

from scripts.voice_ab.common import AXES, join_wavs, load_corpus, validate_vote, wav_duration
from scripts.voice_ab.prepare import qwen_condition


ROOT = Path(__file__).resolve().parents[1]
TOOLS = ROOT / "scripts" / "voice_ab"


def tiny_wav(rate: int = 24_000, frames: int = 2_400) -> bytes:
    import io
    import wave
    output = io.BytesIO()
    with wave.open(output, "wb") as target:
        target.setnchannels(1)
        target.setsampwidth(2)
        target.setframerate(rate)
        target.writeframes(b"\0\0" * frames)
    return output.getvalue()


def test_corpus_has_balanced_required_scope_and_one_cold_pair() -> None:
    corpus = load_corpus(TOOLS / "corpus.json")
    categories = [item["category"] for item in corpus["cases"]]
    for required in ("短い相槌", "通常会話", "質問", "感情表現", "固有名詞", "難読語",
                     "長い説明", "文末の間", "フィラー候補", "連続turn"):
        assert required in categories
    assert sum(item["generation_condition"] == "cold" for item in corpus["cases"]) == 1
    assert sum(item.get("stability_group") == "repeat-01" for item in corpus["cases"]) == 3


def test_review_assets_never_name_providers() -> None:
    combined = "\n".join((TOOLS / name).read_text(encoding="utf-8")
                           for name in ("index.html", "review.js", "style.css"))
    assert "Qwen" not in combined
    assert "Irodori" not in combined
    assert "provider名" in combined


def test_join_wavs_keeps_units_and_equal_pause() -> None:
    joined = join_wavs([tiny_wav(), tiny_wav()], pause_ms=100)
    assert wav_duration(joined) == pytest.approx(0.3)
    with pytest.raises(ValueError, match="inconsistent_wav_rate"):
        join_wavs([tiny_wav(), tiny_wav(rate=44_100)])


def test_vote_requires_every_axis_and_bounded_blind_choice() -> None:
    vote = {"case_id": "case-01", "axes": {axis: "tie" for axis in AXES},
            "overall": "A", "note": "文末の間を再確認．"}
    assert validate_vote(vote, {"case-01"}) == vote
    broken = json.loads(json.dumps(vote))
    broken["axes"].pop("cuteness")
    with pytest.raises(ValueError, match="invalid_vote"):
        validate_vote(broken, {"case-01"})
    broken = json.loads(json.dumps(vote))
    broken["overall"] = "irodori-tts"
    with pytest.raises(ValueError, match="invalid_vote"):
        validate_vote(broken, {"case-01"})


def test_qwen_condition_records_verified_icl_without_exposing_it_publicly() -> None:
    config = {"profiles": [{"provider": "qwen3-tts", "clone_prompt_digest": "a" * 64}]}
    assert "ICL clone prompt" in qwen_condition(config)
    assert "x-vector-only" in qwen_condition({"profiles": []})

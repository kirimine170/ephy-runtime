"""Synthetic Irodori adapter probes．No real voice or model asset is used．"""

from __future__ import annotations

from dataclasses import dataclass
import hashlib
import io
from pathlib import Path
from types import SimpleNamespace
import wave

import pytest

from apps.speech.irodori import (MODEL_REVISION, IrodoriAdapter, _verify_files,
                                 normalize_speech_text, style_caption)
from apps.speech.schemas import IRODORI_CAPABILITIES, SpeechError, SpeechRequest, validate_style_for_capabilities


def wav_bytes(frames: int = 160, rate: int = 16000) -> bytes:
    output = io.BytesIO()
    with wave.open(output, "wb") as wav:
        wav.setnchannels(1)
        wav.setsampwidth(2)
        wav.setframerate(rate)
        wav.writeframes(b"\x10\x00" * frames)
    return output.getvalue()


def request(**changes) -> SpeechRequest:
    return SpeechRequest.model_validate({
        "request_id": "request-1", "operation_id": "operation-1", "session_id": "session-1",
        "turn_id": "turn-1", "generation_revision": 1, "speech_unit_sequence": 1, "voice_profile_id": "anime-voice",
        "model_revision": MODEL_REVISION, "reference_group_digest": "a" * 64,
        "speech_text": "EphyとGitHubのAPIを確認します🙂．", **changes,
    })


@dataclass
class Sampling:
    values: dict


class Runtime:
    def __init__(self, *, fail: bool = False):
        self.calls = []
        self.fail = fail

    def synthesize(self, sampling):
        assert Path(sampling.values["ref_wav"]).is_file()
        self.calls.append(sampling.values)
        if self.fail:
            raise RuntimeError("PRIVATE reference path")
        return SimpleNamespace(audio=[0.1] * 441, sample_rate=44100)


def factory(**values):
    return Sampling(values)


def group(*rates: int):
    references = tuple(SimpleNamespace(audio_bytes=wav_bytes(rate=rate),
        metadata={"sample_rate": rate}) for rate in rates)
    return SimpleNamespace(provenance_id="prov_" + "b" * 32, references=references)


def adapter(tmp_path: Path, runtime: Runtime, grouped=None) -> IrodoriAdapter:
    return IrodoriAdapter({"temporary_path": str(tmp_path)}, runtime=runtime,
        group_loader=lambda digest: grouped or group(16000), sampling_request_factory=factory)


def test_style_mapping_is_finite_and_never_contains_identity_or_emoji():
    captions = set()
    for affect in IRODORI_CAPABILITIES["controls"]["affect"]["values"]:
        speech = request(affect=affect)
        validate_style_for_capabilities(speech, IRODORI_CAPABILITIES)
        caption = style_caption(speech)
        captions.add(caption)
        assert affect not in caption and "Ephy" not in caption and "🙂" not in caption
    assert len(captions) == 6
    for invalid in ({"affect": "arbitrary prompt"}, {"intensity": 0.9}, {"pace": 2.0},
                    {"pause_style": "dramatic"}, {"pitch_hint": 1.0}):
        with pytest.raises(SpeechError, match="unsupported_voice_control"):
            validate_style_for_capabilities(request(**invalid), IRODORI_CAPABILITIES)


def test_targeted_normalization_keeps_display_text_separate_and_drops_emoji():
    original = "EphyとQwen，GitHub，ASR，TTS，LLM，APIを確認します🙂．"
    speech, changes = normalize_speech_text(original)
    assert original.startswith("Ephy")
    assert speech == "エフィーとクウェン,ギットハブ,エーエスアール,ティーティーエス,エルエルエム,エーピーアイを確認します."
    assert len(changes) == 7 and "🙂" not in speech
    assert normalize_speech_text("東京都の四谷です．")[0] == "東京都の四谷です."
    assert normalize_speech_text("温度は20℃，重さは2kgです．")[0] == "温度は20°C,重さは2kgです."


def test_auto_duration_full_precision_controls_and_group_temp_cleanup(tmp_path):
    runtime = Runtime()
    synthesis = adapter(tmp_path, runtime, group(16000, 24000))
    body = synthesis.synthesize(request(affect="warm", intensity=1.25, pace=0.85,
        pause_style="deliberate", volume=0.5), {"provenance_id": "prov_" + "b" * 32})
    assert body[:4] == b"RIFF"
    values = runtime.calls[0]
    assert values["seconds"] is None and values["duration_scale"] == pytest.approx(1 / 0.85)
    assert values["num_steps"] == 40 and values["seed"] == 420 and values["lora_adapter"] is None
    assert values["text"].startswith("エフィー") and "温かみ" in values["caption"]
    assert not Path(values["ref_wav"]).exists() and list(tmp_path.iterdir()) == []
    with wave.open(io.BytesIO(body)) as wav:
        assert (wav.getnchannels(), wav.getsampwidth(), wav.getframerate()) == (1, 2, 44100)


def test_temp_reference_is_deleted_when_generation_fails(tmp_path):
    runtime = Runtime(fail=True)
    with pytest.raises(RuntimeError, match="PRIVATE"):
        adapter(tmp_path, runtime).synthesize(request(), {"provenance_id": "prov_" + "b" * 32})
    assert list(tmp_path.iterdir()) == []


def test_profile_group_digest_and_provenance_are_both_pinned(tmp_path):
    synthesis = adapter(tmp_path, Runtime())
    with pytest.raises(SpeechError, match="tts_asset_invalid"):
        synthesis.synthesize(request(), {"provenance_id": "prov_" + "c" * 32})
    with pytest.raises(SpeechError, match="voice_profile_changed"):
        synthesis.synthesize(request(clone_prompt_digest="c" * 64), {"provenance_id": "prov_" + "b" * 32})


def test_artifact_verification_checks_size_hash_and_rejects_symlink(tmp_path):
    body = b"pinned public model"
    artifact = tmp_path / "model.safetensors"
    artifact.write_bytes(body)
    expected = hashlib.sha256(body).hexdigest()
    _verify_files(tmp_path, {artifact.name: (len(body), expected)})
    artifact.write_bytes(b"x" * len(body))
    with pytest.raises(SpeechError, match="tts_model_revision_mismatch"):
        _verify_files(tmp_path, {artifact.name: (len(body), expected)})

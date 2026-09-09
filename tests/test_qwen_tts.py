import hashlib
import io
import json
import sys
from types import SimpleNamespace
import wave

import pytest
import numpy as np

from apps.speech.qwen import MODEL_REVISION, QwenAdapter, verify_model_files, waveform_to_wav
from apps.speech.schemas import (QWEN_CAPABILITIES, SpeechError, SpeechRequest,
                                 parse_speech_request, validate_style_for_capabilities)


def speech_request(**changes):
    return SpeechRequest.model_validate({"request_id": "request-1", "voice_profile_id": "voice-1",
        "operation_id": "operation-1", "session_id": "session-1", "turn_id": "turn-1", "generation_revision": 1, "speech_unit_sequence": 1,
        "model_revision": MODEL_REVISION, "clone_prompt_digest": "a" * 64,
        "speech_text": "春には桜が咲きます。", **changes})


class FakeQwen:
    def __init__(self, sequences=None, *, invoke_talker=True, error=None):
        self.sequences = [[1, 2, 9]] if sequences is None else sequences
        self.calls = []
        self.error = error
        self.invoke_talker = invoke_talker
        self.model = SimpleNamespace(talker=SimpleNamespace(generate=self.generate_codes),
            config=SimpleNamespace(talker_config=SimpleNamespace(codec_eos_token_id=9)))

    def generate_codes(self, **kwargs):
        return SimpleNamespace(sequences=self.sequences)

    def generate_voice_clone(self, **kwargs):
        self.calls.append(kwargs)
        if self.error:
            raise self.error
        if self.invoke_talker:
            self.model.talker.generate(max_new_tokens=kwargs["max_new_tokens"])
        return [[0.0, 0.5, -0.5] * 100], 24000


def test_official_clone_uses_reusable_prompt_and_provider_language_mapping():
    model = FakeQwen()
    prompts = []
    prompt = object()
    adapter = QwenAdapter({}, model=model, prompt_loader=lambda digest: prompts.append(digest) or [prompt])
    profile = {"language": "ja-JP"}
    for _ in range(2):
        result = adapter.synthesize(speech_request(), profile)
        with wave.open(io.BytesIO(result)) as wav:
            assert (wav.getnchannels(), wav.getsampwidth(), wav.getframerate()) == (1, 2, 24000)
    assert prompts == ["a" * 64]
    assert all(call["voice_clone_prompt"] == [prompt] and call["language"] == "Japanese" for call in model.calls)
    assert all(call["max_new_tokens"] == 512 and call["non_streaming_mode"] is False for call in model.calls)
    assert all(not {"ref_audio", "ref_text", "instruct"} & set(call) for call in model.calls)


@pytest.mark.parametrize("sequences", [[], [[]], [[1, 2]], [[9, 1]], [[9, 9]], [[1] * 16], [[1] * 16 + [9]], [[9], [9]]])
def test_no_eos_or_damaged_terminal_never_becomes_audio(sequences):
    model = FakeQwen(sequences)
    original = model.model.talker.generate
    adapter = QwenAdapter({"output_budget": 16}, model=model, prompt_loader=lambda _: [])
    with pytest.raises(SpeechError, match="tts_incomplete"):
        adapter.synthesize(speech_request(), {"language": "ja-JP"})
    assert model.model.talker.generate == original


def test_eos_exactly_at_budget_is_complete_but_unobserved_eos_is_not():
    adapter = QwenAdapter({"output_budget": 16}, model=FakeQwen([[1] * 15 + [9]]), prompt_loader=lambda _: [])
    assert adapter.synthesize(speech_request(), {"language": "ja-JP"})[:4] == b"RIFF"
    adapter = QwenAdapter({}, model=FakeQwen(invoke_talker=False), prompt_loader=lambda _: [])
    with pytest.raises(SpeechError, match="tts_incomplete"):
        adapter.synthesize(speech_request(), {"language": "ja-JP"})


def test_inference_failure_restores_talker_instrumentation():
    model = FakeQwen(error=ValueError("PRIVATE reference transcript"))
    original = model.model.talker.generate
    adapter = QwenAdapter({}, model=model, prompt_loader=lambda _: [])
    with pytest.raises(ValueError):
        adapter.synthesize(speech_request(), {"language": "ja-JP"})
    assert model.model.talker.generate == original


@pytest.mark.parametrize("control", [{"affect": "happy"}, {"pace": 1.2}, {"pitch_hint": 1.0}, {"intensity": 0.5}, {"pause_style": "short"}, {"interruptible": False}])
def test_qwen_unsupported_controls_are_explicitly_rejected(control):
    request = speech_request().model_dump()
    request.update(control)
    with pytest.raises(SpeechError, match="unsupported_voice_control"):
        validate_style_for_capabilities(parse_speech_request(request), QWEN_CAPABILITIES)


def test_pcm_volume_scaling_and_invalid_waveforms():
    loud = waveform_to_wav([0.5, -0.5], 24000, 1.0)
    quiet = waveform_to_wav([0.5, -0.5], 24000, 0.5)
    with wave.open(io.BytesIO(loud)) as left, wave.open(io.BytesIO(quiet)) as right:
        assert int.from_bytes(left.readframes(1), "little", signed=True) == 16384
        assert int.from_bytes(right.readframes(1), "little", signed=True) == 8192
    for samples, rate, volume in [([], 24000, 1), ([float("nan")], 24000, 1), ([0], 22050, 1), ([0], 24000, float("inf"))]:
        with pytest.raises(SpeechError, match="tts_invalid_audio"):
            waveform_to_wav(samples, rate, volume)


def test_model_revision_checks_every_declared_file(tmp_path):
    body = b"fixed public model metadata"
    (tmp_path / "config.json").write_bytes(body)
    expected = hashlib.sha1(f"blob {len(body)}\0".encode() + body).hexdigest()
    manifest = {"config.json": (len(body), expected)}
    verify_model_files(tmp_path, manifest)
    (tmp_path / "config.json").write_bytes(b"x" * len(body))
    with pytest.raises(SpeechError, match="tts_model_revision_mismatch"):
        verify_model_files(tmp_path, manifest)


def test_clone_tensors_must_match_loaded_model_codebooks_and_speaker_dimension():
    config = SimpleNamespace(talker_config=SimpleNamespace(num_code_groups=16,
        code_predictor_config=SimpleNamespace(vocab_size=2048)), speaker_encoder_config=SimpleNamespace(enc_dim=2048))
    adapter = QwenAdapter({}, model=SimpleNamespace(model=SimpleNamespace(config=config)))
    valid = {"ref_code": np.zeros((20, 16), dtype=np.int64), "ref_spk_embedding": np.ones(2048, dtype=np.float32)}
    adapter._validate_prompt_arrays(valid)
    for changed in [
        {**valid, "ref_code": np.full((20, 16), 2048, dtype=np.int64)},
        {**valid, "ref_code": np.zeros((20, 15), dtype=np.int64)},
        {**valid, "ref_code": np.zeros((20, 16), dtype=np.int32)},
        {**valid, "ref_spk_embedding": np.ones(1024, dtype=np.float32)},
        {**valid, "ref_spk_embedding": np.full(2048, np.nan, dtype=np.float32)},
    ]:
        with pytest.raises(SpeechError, match="tts_asset_invalid"):
            adapter._validate_prompt_arrays(changed)


def test_prepare_uses_verified_audio_bytes_and_casts_bfloat_embedding_before_numpy(monkeypatch):
    from packages import speech_assets
    reference = SimpleNamespace(audio_bytes=b"verified private wav", audio_path="must-not-reopen.wav", transcript="正確な参照文．\n")
    saved = {}
    class Store:
        def load_reference(self, reference_id):
            assert reference_id == "ref-test"
            return reference
        def store_clone_prompt(self, reference_id, **kwargs):
            saved.update(kwargs)
            return "saved-prompt"
    monkeypatch.setattr(speech_assets, "SpeechAssetStore", lambda _: Store())
    def read(source, **kwargs):
        assert isinstance(source, io.BytesIO) and source.getvalue() == reference.audio_bytes
        return np.zeros(24000, dtype=np.float32), 24000
    monkeypatch.setitem(sys.modules, "soundfile", SimpleNamespace(read=read))
    class Tensor:
        def __init__(self, value):
            self.value, self.converted = value, False
        def detach(self): return self
        def cpu(self): return self
        def float(self): self.converted = True; return self
        def long(self): self.converted = True; return self
        def numpy(self):
            assert self.converted, "torch BF16 cannot be converted directly to numpy"
            return self.value
    model = FakeQwen()
    model.model.config.speaker_encoder_config = SimpleNamespace(enc_dim=2048)
    model.model.config.talker_config.num_code_groups = 16
    model.model.config.talker_config.code_predictor_config = SimpleNamespace(vocab_size=2048)
    def create_prompt(**kwargs):
        assert kwargs["ref_text"] == reference.transcript and kwargs["x_vector_only_mode"] is False
        return [SimpleNamespace(ref_code=Tensor(np.zeros((20, 16), dtype=np.int64)),
                                ref_spk_embedding=Tensor(np.ones(2048, dtype=np.float32)))]
    model.create_voice_clone_prompt = create_prompt
    adapter = QwenAdapter({"asset_store_path": "/private/test-assets"}, model=model)
    assert adapter.prepare("ref-test") == "saved-prompt"
    assert saved["metadata"]["ref_text"] == reference.transcript
    assert saved["model_revision"] == saved["tokenizer_revision"] == MODEL_REVISION


@pytest.mark.parametrize("changes", [{"speech_text": "あ" * 181}, {"speech_text": " "}, {"volume": float("nan")}, {"speech_text": "\ud800"}, {"request_id": "private ID"}, {"extra": "PRIVATE raw audio"}])
def test_request_validation_never_exposes_rejected_body(changes):
    data = speech_request().model_dump()
    data.update(changes)
    with pytest.raises(SpeechError) as caught:
        parse_speech_request(json.dumps(data).encode())
    assert str(caught.value) in {"invalid_speech_text", "unsupported_voice_control"}

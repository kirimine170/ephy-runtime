"""C pointer lifetime，identity and PCM failure contracts without model downloads．"""

import ctypes as C
import hashlib
import io
import subprocess
import sys
import wave
import pytest
from apps.speech.audiocpp import AudioCppAdapter, MODEL_REVISION, verified_file
from apps.speech.schemas import SpeechError, SpeechRequest, public_profile
from apps.speech.service import SpeechService, synthesis_config_digest

PROFILE = {
    "voice_profile_id": "audio-exp",
    "provider": "irodori-audiocpp",
    "display_name": "Audio experiment",
    "model_revision": MODEL_REVISION,
    "language": "ja-JP",
    "reference_group_digest": "a" * 64,
    "provenance_id": "synthetic-provenance",
}


class ABI:
    def __init__(self, *, rate=16000, channels=1, frames=3, fail=False):
        self.samples = (C.c_float * 3)(0.5, -0.5, 0.0)
        self.rate, self.channels, self.frames, self.fail = rate, channels, frames, fail
        self.freed = []
        self.options = {}

    def check(self, status):
        if status:
            raise SpeechError("tts_failed")

    def request_create(self):
        return 1

    def request_free(self, h):
        self.freed.append("request")

    def request_set_text(self, *a):
        return 0

    def request_set_voice_audio(self, *a):
        return 0

    def request_set_option(self, h, k, v):
        self.options[k.decode()] = v.decode()
        return 0

    def session_run(self, h, r, out):
        out._obj.value = 2
        return int(self.fail)

    def result_audio(self, h, samples, frames, rate, channels):
        C.cast(samples, C.POINTER(C.POINTER(C.c_float)))[0] = C.cast(
            self.samples, C.POINTER(C.c_float)
        )
        frames._obj.value = self.frames
        rate._obj.value = self.rate
        channels._obj.value = self.channels
        return 0

    def result_free(self, h):
        self.freed.append("result")
        self.samples[0] = float("nan")


def req():
    return SpeechRequest(
        request_id="r",
        operation_id="o",
        session_id="s",
        turn_id="t",
        generation_revision=1,
        speech_unit_sequence=1,
        voice_profile_id="audio-exp",
        model_revision=MODEL_REVISION,
        reference_group_digest="a" * 64,
        speech_text="Ephyの確認です．",
        volume=0.5,
    )


def wav(tmp_path):
    p = tmp_path / "ref.wav"
    with wave.open(str(p), "wb") as w:
        w.setnchannels(1)
        w.setsampwidth(2)
        w.setframerate(16000)
        w.writeframes(b"\0\0" * 3)
    return str(p)


def test_borrowed_pcm_is_copied_before_free_and_controls_are_mapped(tmp_path):
    a = AudioCppAdapter({})
    a.abi = ABI()
    body = a._generate("full text", "finite caption", wav(tmp_path), req())
    assert a.abi.freed == ["result", "request"]
    with wave.open(io.BytesIO(body)) as w:
        assert int.from_bytes(w.readframes(1), "little", signed=True) == 8192
    assert a.abi.options["num_inference_steps"] == "40"
    assert a.abi.options["instruction"] == "finite caption"
    assert (
        a.abi.options["no_ref"] == "false" and a.abi.options["text_chunk_size"] == "9"
    )


@pytest.mark.parametrize(
    "kwargs",
    [
        {"rate": 0},
        {"channels": 2},
        {"frames": 999999999},
        {"frames": 0},
        {"fail": True},
    ],
)
def test_invalid_native_result_always_releases_handles(tmp_path, kwargs):
    a = AudioCppAdapter({})
    a.abi = ABI(**kwargs)
    with pytest.raises(SpeechError):
        a._generate("text", "caption", wav(tmp_path), req())
    assert a.abi.freed == ["result", "request"]


def test_nan_result_fails_without_reusing_borrowed_memory(tmp_path):
    a = AudioCppAdapter({})
    a.abi = ABI()
    a.abi.samples[1] = float("nan")
    with pytest.raises(SpeechError, match="tts_invalid_audio"):
        a._generate("text", "caption", wav(tmp_path), req())
    assert a.abi.freed == ["result", "request"]


def test_library_hash_and_symlink_are_fail_closed(tmp_path):
    p = tmp_path / "lib"
    p.write_bytes(b"synthetic-library")
    sha = hashlib.sha256(p.read_bytes()).hexdigest()
    assert verified_file(p, sha) == p
    with pytest.raises(SpeechError):
        verified_file(p, "0" * 64)
    link = tmp_path / "link"
    link.symlink_to(p)
    with pytest.raises(SpeechError):
        verified_file(link, sha)


def test_experimental_profile_cannot_be_default_and_digest_separates_backends():
    assert public_profile(PROFILE, available=True)["provider"] == "irodori-audiocpp"
    with pytest.raises(SpeechError, match="invalid_voice_config"):
        SpeechService(
            {"profiles": [PROFILE], "default_profile_id": "audio-exp"}, available=True
        )
    cfg = {
        "providers": {
            "irodori-audiocpp": {"library_sha256": "a" * 64, "codec_backend": "same"}
        }
    }
    first = synthesis_config_digest(cfg, "irodori-audiocpp")
    cfg["providers"]["irodori-audiocpp"]["codec_backend"] = "cpu"
    assert first != synthesis_config_digest(cfg, "irodori-audiocpp")


def test_new_adapter_import_does_not_load_old_inference_dependencies():
    p = subprocess.run(
        [
            sys.executable,
            "-c",
            'import sys; import apps.speech.audiocpp; assert not {"torch","irodori_tts","silentcipher","dacvae"}.intersection(sys.modules)',
        ],
        capture_output=True,
    )
    assert p.returncode == 0, p.stderr

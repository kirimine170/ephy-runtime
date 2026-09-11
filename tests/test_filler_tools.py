import hashlib
import io
import json
from pathlib import Path
import subprocess
import sys
import urllib.error
import urllib.request
import wave

import pytest


@pytest.mark.parametrize("candidate,duration_ms", [("hesitation_etto", 500), ("backchannel_hai", 500), ("backchannel_gomen_iiyo", 2400)])
def test_human_review_is_local_and_never_enables_live_voice(tmp_path, candidate, duration_ms):
    with io.BytesIO() as out:
        with wave.open(out, "wb") as audio:
            audio.setnchannels(1); audio.setsampwidth(2); audio.setframerate(16000)
            audio.writeframes(b"\x00\x00" * (16 * duration_ms))
        body = out.getvalue()
    asset = {"candidate": candidate, "file": candidate + ".wav", "duration_ms": duration_ms,
             "sha256": hashlib.sha256(body).hexdigest(), "approved": False}
    manifest = {"schema_version": 1, "enabled": False, "headphones_confirmed": False,
                "profile": {"synthetic": True}, "assets": [asset]}
    path = tmp_path / "manifest.json"
    path.write_text(json.dumps(manifest)); path.chmod(0o600)
    (tmp_path / asset["file"]).write_bytes(body)
    (tmp_path / "review_answer.wav").write_bytes(body)
    root = Path(__file__).resolve().parents[1]
    process = subprocess.Popen([sys.executable, "scripts/filler/review_server.py", "--bundle", str(tmp_path), "--port", "0"],
                               cwd=root, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
    try:
        url = process.stdout.readline().strip()
        assert url.startswith("http://127.0.0.1:")
        origin = url.split("/", 3)[:3]
        origin = "/".join(origin)
        def vote(payload, from_origin=origin):
            req = urllib.request.Request(url + "review", data=json.dumps(payload).encode(), method="POST",
                                         headers={"Origin": from_origin, "Content-Type": "application/json"})
            return urllib.request.urlopen(req, timeout=3)
        with urllib.request.urlopen(url + "manifest", timeout=3) as res:
            public = res.read()
            assert b"synthetic" not in public and b"sha256" not in public
        with pytest.raises(urllib.error.HTTPError) as err:
            vote({"candidate": candidate, "approved": True}, "https://external.example")
        assert err.value.code == 403
        assert json.loads(path.read_text()) == manifest
        with vote({"candidate": candidate, "approved": True}) as res:
            assert res.status == 200
        saved = json.loads(path.read_text())
        assert saved["assets"][0]["approved"] is True
        assert saved["enabled"] is False and saved["headphones_confirmed"] is False
        with pytest.raises(urllib.error.HTTPError) as err:
            vote({"candidate": "checking_now", "approved": True})
        assert err.value.code == 400
        with pytest.raises(urllib.error.HTTPError):
            urllib.request.urlopen(url + "../manifest.json", timeout=3)
    finally:
        process.terminate()
        process.communicate(timeout=5)


def test_prepare_preserves_only_existing_asset_approvals(tmp_path, monkeypatch):
    from scripts.filler import prepare as module
    with io.BytesIO() as out:
        with wave.open(out, "wb") as audio:
            audio.setnchannels(1); audio.setsampwidth(2); audio.setframerate(16000)
            audio.writeframes(b"\x00\x00" * 8000)
        body = out.getvalue()
    profile = {"provider": "irodori-tts", "synthesis_config_digest": "a" * 64}
    config = tmp_path / "config.json"
    config.write_text('{}'); config.chmod(0o600)
    base = tmp_path / "base"; base.mkdir(mode=0o700)
    existing = {"candidate": "hesitation_etto", "file": "hesitation_etto.wav", "approved": True,
                "duration_ms": 500, "sha256": hashlib.sha256(body).hexdigest()}
    manifest = {"schema_version": 1, "profile": profile, "enabled": True, "headphones_confirmed": True, "assets": [existing]}
    (base / "manifest.json").write_text(json.dumps(manifest)); (base / "manifest.json").chmod(0o600)
    (base / existing["file"]).write_bytes(body); (base / "review_answer.wav").write_bytes(body)
    calls = []
    class Adapter:
        def synthesize(self, request, raw):
            calls.append(request); return body
    monkeypatch.setattr(module, '_irodori', lambda *_: (Adapter(), {}, lambda phrase, index: phrase))
    monkeypatch.setattr(module, 'public_profile', lambda *_, **__: profile.copy())
    monkeypatch.setattr(module, 'synthesis_config_digest', lambda *_: 'a' * 64)
    output = tmp_path / 'output'
    module.prepare(config, output, base)
    saved = json.loads((output / 'manifest.json').read_text())
    assert calls == ['ええと', 'うん', 'はい', 'ごめんごめん，いいよ？']
    assert saved['assets'][0] == existing
    assert all(not a['approved'] for a in saved['assets'][1:])
    assert not saved['enabled'] and not saved['headphones_confirmed']
    assert (output / existing['file']).read_bytes() == body
    (base / existing['file']).write_bytes(b'changed')
    with pytest.raises(ValueError, match='filler_asset_changed'):
        module.prepare(config, tmp_path / 'changed', base)

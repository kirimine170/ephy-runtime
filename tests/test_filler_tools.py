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


def test_human_review_is_local_and_never_enables_live_voice(tmp_path):
    with io.BytesIO() as out:
        with wave.open(out, "wb") as audio:
            audio.setnchannels(1); audio.setsampwidth(2); audio.setframerate(16000)
            audio.writeframes(b"\x00\x00" * 8000)
        body = out.getvalue()
    asset = {"candidate": "hesitation_etto", "file": "hesitation_etto.wav", "duration_ms": 500,
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
            vote({"candidate": "hesitation_etto", "approved": True}, "https://external.example")
        assert err.value.code == 403
        assert json.loads(path.read_text()) == manifest
        with vote({"candidate": "hesitation_etto", "approved": True}) as res:
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

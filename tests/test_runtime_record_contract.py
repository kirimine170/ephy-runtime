from __future__ import annotations

import json
from pathlib import Path
import shutil

import pytest

from scripts.verify_runtime_record_fixtures import canonical, strict_loads, verify

ROOT = Path(__file__).resolve().parents[1]


def test_karte_v2_schemas_mac_and_semantic_fixtures() -> None:
    assert verify(ROOT) == []


@pytest.mark.parametrize("raw", [
    '{"x":1,"x":2}', '{} {}', '{"n":1.5}', '{"n":1e3}',
    '{"n":9007199254740992}', '{"n":-0}', '{"text":"\\ud800"}',
    '{"text":"\\udc00"}', b'{"text":"\xff"}',
])
def test_strict_json_rejects_ambiguous_signed_input(raw) -> None:
    with pytest.raises((ValueError, UnicodeError)):
        strict_loads(raw)


def test_text_change_invalidates_mac_without_echoing_text(tmp_path: Path) -> None:
    for relative in ("schemas/karte-ephy/v2", "schemas/karte-context/v2"):
        destination = tmp_path / relative
        destination.parent.mkdir(parents=True, exist_ok=True)
        shutil.copytree(ROOT / relative, destination)
    path = tmp_path / "schemas/karte-ephy/v2/fixtures/raw-text.proposal.json"
    payload = json.loads(path.read_bytes())
    payload["events"][0]["text"] = "synthetic changed text"
    path.write_bytes(canonical(payload))
    errors = verify(tmp_path)
    assert "raw-text.proposal.json: MAC" in errors
    assert not any("synthetic changed text" in item for item in errors)


def test_canonical_bytes_preserve_unicode_and_literal_escape() -> None:
    value = strict_loads('{"z":"雪<>&\\u2028\\u2029\\\\u2028","a":1}')
    assert canonical(value) == '{"a":1,"z":"雪<>&\u2028\u2029\\\\u2028"}'.encode()

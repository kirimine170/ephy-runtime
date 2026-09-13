"""Exclude policy-owned records and delivery credentials from legacy readers．

No authorization is granted here．Only Karte v2 can authorize their contents．
The checks also cover managed RAG copies whose original was moved or removed．
"""
from __future__ import annotations

import json
import os
import re
import uuid
from pathlib import Path
from typing import Any

import yaml


ITEM = re.compile(rb'(?m)^<!-- karte-v2:item (\{[^\n]+\}) -->$')
RECORD_NAME = re.compile(r'^ephy-v2-[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}(?:-\d+)?\.md$')


def _record_metadata(value: Any) -> bool:
    return isinstance(value, dict) and isinstance(value.get("runtime_record"), dict) and str(value["runtime_record"].get("schema_version", "")).startswith("2.")


def has_record_marker(raw: bytes | str) -> bool:
    data = raw.encode("utf-8") if isinstance(raw, str) else raw
    # A mention of a schema field in ordinary documentation is not ownership．
    # Recognize actual v2 frontmatter，JSON records or rendered event metadata．
    if data.startswith(b"---\n"):
        header = data[4:].split(b"\n---", 1)[0]
        if len(header) <= 64 << 10:
            try:
                if _record_metadata(yaml.safe_load(header)):
                    return True
            except (ValueError, yaml.YAMLError):
                pass
    if data.lstrip().startswith(b"{"):
        try:
            if _record_metadata(json.loads(data)):
                return True
        except (ValueError, UnicodeError):
            pass
    for item in ITEM.finditer(data):
        try:
            event = json.loads(item[1])
            uuid.UUID(event["event_id"])
            uuid.UUID(event["conversation_id"])
            return True
        except (ValueError, KeyError, TypeError):
            pass
    return False


def protected_path(path: Path | str, *, raw: bytes | None = None, doc_id: str | None = None) -> bool:
    candidate = Path(path).expanduser().resolve(strict=False)
    parts = candidate.parts
    if ".mdsys" in parts or RECORD_NAME.fullmatch(candidate.name):
        return True
    for name in ("EPHY_RECORDING_HOME", "EPHY_KARTE_CONFIG_ROOT"):
        configured = os.environ.get(name, "").strip()
        if configured and candidate.is_relative_to(Path(configured).expanduser().resolve(strict=False)):
            return True
    # Default private configuration roots are never generic document sources．
    for index, part in enumerate(parts[:-1]):
        if part == "Ephy" and parts[index + 1] == "recording":
            return True
        if part == "Karte" and parts[index + 1] == "ephy-v2":
            return True
    roots = list(candidate.parents)
    configured = os.environ.get("KARTE_DATA_DIR", "").strip()
    if configured:
        roots.append(Path(configured).expanduser().resolve(strict=False))
    for root in dict.fromkeys(roots):
        ledger = root / ".mdsys/ephy/records/v2/ledger.json"
        try:
            if not ledger.exists():
                continue
            if ledger.is_symlink() or ledger.stat().st_size > 64 << 20:
                raise ValueError("karte_ownership_unavailable")
            state = json.loads(ledger.read_bytes())
            docs = state["docs"]
            if not isinstance(docs, dict):
                raise ValueError("karte_ownership_unavailable")
            if doc_id and doc_id in docs:
                return True
            for entry in docs.values():
                relative = entry["path"]
                if not isinstance(relative, str):
                    raise ValueError("karte_ownership_unavailable")
                managed = (root / relative).resolve(strict=False)
                if not managed.is_relative_to(root):
                    raise ValueError("karte_ownership_unavailable")
                if candidate == managed:
                    return True
        except (OSError, ValueError, KeyError, TypeError):
            # Stop reads and setup without deleting unrelated copies when
            # ownership cannot be determined．
            raise ValueError("karte_ownership_unavailable") from None
    try:
        if raw is None and candidate.is_file():
            # Markers live in the bounded frontmatter，before arbitrary body text．
            with candidate.open("rb") as stream:
                raw = stream.read(1 << 20)
        return raw is not None and has_record_marker(raw)
    except OSError:
        return True


def protected_chunk(chunk: Any) -> bool:
    get = chunk.get if isinstance(chunk, dict) else lambda key, default=None: getattr(chunk, key, default)
    if has_record_marker(get("chunk_text", "")):
        return True
    return any(protected_path(path, doc_id=get("doc_id")) for path in
               (get("source_path"), get("original_source_path")) if path)


def remove_managed_copy(chunk: Any) -> None:
    """Remove only a blocked copy produced by generic RAG，never its source．"""
    from packages.config_core.loader import ROOT_DIR
    get = chunk.get if isinstance(chunk, dict) else lambda key, default=None: getattr(chunk, key, default)
    source, original = get("source_path"), get("original_source_path")
    if not source or not original or not protected_chunk(chunk):
        return
    copy = Path(source)
    managed = Path(os.environ.get("LW_DATA_ROOT", "") or ROOT_DIR.parent / "LW_data").resolve()
    if copy.is_symlink() or copy.resolve() == Path(original).resolve():
        return
    if copy.resolve().is_relative_to(managed) and copy.is_file():
        copy.unlink()


def model_private_roots() -> list[Path]:
    home = Path.home()
    roots = [home/'Library/Application Support/Ephy/recording',
             home/'Library/Application Support/Karte/ephy-v2',
             home/'.config/Ephy/recording',home/'.config/Karte/ephy-v2']
    for name in ('EPHY_RECORDING_HOME','EPHY_KARTE_CONFIG_ROOT','KARTE_DATA_DIR'):
        configured = os.environ.get(name, '').strip()
        if configured:
            roots.append(Path(configured).expanduser())
    return [path.resolve(strict=False) for path in roots]

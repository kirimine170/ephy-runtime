"""Create an unsigned development instance locally, without overwriting any existing configuration．"""

import argparse
import hashlib
import json
import os
from datetime import datetime, timezone
from pathlib import Path
from uuid import uuid4

import yaml


def initialize(root: Path, private_root: Path, name: str = "エフィ"):
    config_path = root / "configs/ephy.local.yaml"
    if config_path.exists():
        raise ValueError("Ephy local config already exists; no files were changed")
    if not private_root.is_absolute() or not name.strip():
        raise ValueError("An absolute private root and a nonempty name are required")
    identity = yaml.safe_load((root / "configs/examples/identity.example.yaml").read_text())
    profile = yaml.safe_load((root / "configs/examples/profile.example.yaml").read_text())
    profile["clarification"]["example"] = []
    identity["identity"].update(instance_id=str(uuid4()), individual_name=name,
                                created_at=datetime.now(timezone.utc).isoformat())
    identity.pop("ownership", None)
    identity["genesis"]["created_by"] = "local-development"
    content = json.dumps({"identity": identity["identity"], "profile": profile}, sort_keys=True).encode()
    identity["genesis"]["genesis_manifest_hash"] = "sha256:" + hashlib.sha256(content).hexdigest()
    private_root.mkdir(parents=True, exist_ok=True)
    os.chmod(private_root, 0o700)
    instance = private_root / "instances" / identity["identity"]["instance_id"]
    instance.mkdir(parents=True, exist_ok=False, mode=0o700)
    for path, payload in ((instance / "identity.yaml", identity), (instance / "profile.yaml", profile),
                           (config_path, {"ephy": {"enabled": True, "private_root": str(private_root),
                                                   "instance_id": identity["identity"]["instance_id"]}})):
        fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(fd, "w", encoding="utf-8") as handle:
            yaml.safe_dump(payload, handle, allow_unicode=True, sort_keys=False)
    return {"status": "initialized", "development_only": True, "signed": False}


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--private-root", type=Path, required=True)
    parser.add_argument("--runtime-root", type=Path, default=Path(__file__).resolve().parents[1])
    parser.add_argument("--name", default="エフィ")
    args = parser.parse_args()
    print(json.dumps(initialize(args.runtime_root, args.private_root, args.name)))


if __name__ == "__main__":
    main()

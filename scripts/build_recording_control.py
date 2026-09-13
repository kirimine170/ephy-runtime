#!/usr/bin/env python3
"""Package the pinned Karte human control plane with body-free provenance．"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import subprocess
import tempfile

KARTE_REVISION = "90c69c58945e4eb00ec5f13c73b444ff298443d2"


def build(karte_root: Path, app: Path) -> dict:
    revision = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=karte_root, text=True).strip()
    dirty = subprocess.check_output(["git", "status", "--porcelain=v1"], cwd=karte_root)
    if revision != KARTE_REVISION or dirty:
        raise ValueError("Build recording control from the clean pinned Karte checkout．")
    helper = app / "Contents/Helpers/karte-ephy-control"
    helper.parent.mkdir(parents=True, exist_ok=True)
    subprocess.run(["go", "build", "-buildvcs=false", "-ldflags=-w -s", "-o", str(helper), "./cmd/karte-ephy-control"], cwd=karte_root, check=True)
    if platform.system() == "Darwin":
        subprocess.run(["codesign", "--force", "--sign", "-", "--identifier", "com.ephy.karte-control", str(helper)], check=True)
        subprocess.run(["codesign", "--verify", "--strict", str(helper)], check=True)
    with tempfile.TemporaryDirectory(prefix="ephy-control-check-") as temporary:
        root = Path(temporary).resolve()
        data = root / "karte"
        data.mkdir()
        raw = subprocess.check_output([str(helper), "-data-root", str(data), "-config-root", str(root / "config"), "capabilities"])
        capabilities = json.loads(raw)
        if capabilities["protocol_version"] != "2.0" or capabilities["record_schema"] != "2.0":
            raise ValueError("Unsupported Karte receiver contract")
    if subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=karte_root, text=True).strip() != revision or subprocess.check_output(["git", "status", "--porcelain=v1"], cwd=karte_root):
        raise ValueError("Karte source changed while building")
    proof = {"schema_version": 1, "source_revision": revision, "source_dirty": False, "protocol_version": "2.0", "executable_sha256": hashlib.sha256(helper.read_bytes()).hexdigest()}
    output = app / "Contents/Resources/karte-control-provenance.json"
    output.parent.mkdir(parents=True, exist_ok=True)
    output.write_text(json.dumps(proof, indent=2) + "\n")
    return proof


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--app", type=Path, required=True)
    parser.add_argument("--karte-root", type=Path, default=Path(os.environ.get("EPHY_KARTE_SOURCE_ROOT", Path(__file__).resolve().parents[2] / "karte")))
    args = parser.parse_args()
    print(json.dumps(build(args.karte_root.resolve(), args.app.resolve()), indent=2))

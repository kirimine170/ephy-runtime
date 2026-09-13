#!/usr/bin/env python3
"""Package the pinned Karte human control plane with body-free provenance．"""
import argparse
from contextlib import contextmanager
import hashlib
import io
import json
import os
from pathlib import Path
import platform
import subprocess
import tarfile
import tempfile

KARTE_REVISION = "90c69c58945e4eb00ec5f13c73b444ff298443d2"


@contextmanager
def pinned_source(karte_root: Path):
    """Use the recorded commit without keeping a historical worktree installed．"""
    revision = subprocess.check_output(
        ["git", "rev-parse", KARTE_REVISION + "^{commit}"], cwd=karte_root, text=True).strip()
    if revision != KARTE_REVISION:
        raise ValueError("The pinned Karte revision is unavailable．")
    archive = subprocess.check_output(["git", "archive", "--format=tar", revision], cwd=karte_root)
    with tempfile.TemporaryDirectory(prefix="ephy-karte-control-source-") as temporary:
        source = Path(temporary)
        with tarfile.open(fileobj=io.BytesIO(archive)) as bundle:
            bundle.extractall(source, filter="data")
        yield source


def build(karte_root: Path, app: Path) -> dict:
    revision = KARTE_REVISION
    helper = app / "Contents/Helpers/karte-ephy-control"
    helper.parent.mkdir(parents=True, exist_ok=True)
    with pinned_source(karte_root) as source:
        subprocess.run(["go", "build", "-buildvcs=false", "-ldflags=-w -s", "-o", str(helper), "./cmd/karte-ephy-control"], cwd=source, check=True)
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

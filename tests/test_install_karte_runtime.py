import json
import os
from pathlib import Path
import shutil
import subprocess


REPOSITORY_ROOT = Path(__file__).resolve().parents[1]


def _install_harness(tmp_path: Path) -> tuple[Path, dict[str, str]]:
    runtime_root = tmp_path / "runtime"
    scripts = runtime_root / "scripts"
    scripts.mkdir(parents=True)
    installer = scripts / "install_karte_runtime.sh"
    shutil.copy2(REPOSITORY_ROOT / "scripts/install_karte_runtime.sh", installer)

    fake_bin = tmp_path / "fake-bin"
    fake_bin.mkdir()
    (fake_bin / "uname").write_text("#!/bin/sh\nprintf 'Linux\\n'\n")
    (fake_bin / "ditto").write_text(
        "#!/usr/bin/env bash\n"
        "set -euo pipefail\n"
        "if [[ \"${1:-}\" == '-x' && \"${2:-}\" == '-k' ]]; then\n"
        "  python3 -m zipfile -e \"$3\" \"$4\"\n"
        "  find \"$4\" -path '*/Contents/MacOS/karte' -exec chmod +x {} \\;\n"
        "else\n"
        "  cp -R -- \"$1\" \"$2\"\n"
        "fi\n"
    )
    for command in fake_bin.iterdir():
        command.chmod(0o755)

    temp_root = tmp_path / "tmp"
    temp_root.mkdir()
    environment = os.environ.copy()
    environment["PATH"] = f"{fake_bin}{os.pathsep}{environment['PATH']}"
    environment["TMPDIR"] = str(temp_root)
    return installer, environment


def _karte_bundle(tmp_path: Path, *, with_provenance: bool) -> Path:
    bundle = tmp_path / "artifact/Karte.app"
    executable = bundle / "Contents/MacOS/karte"
    executable.parent.mkdir(parents=True)
    executable.write_text("#!/bin/sh\nexit 0\n")
    executable.chmod(0o755)
    if with_provenance:
        provenance = bundle / "Contents/Resources/karte-build-provenance.json"
        provenance.parent.mkdir(parents=True)
        provenance.write_text(json.dumps({"source_revision": "a" * 40}) + "\n")
    return bundle


def test_installer_rejects_bundle_without_embedded_provenance(tmp_path):
    installer, environment = _install_harness(tmp_path)
    bundle = _karte_bundle(tmp_path, with_provenance=False)
    archive = shutil.make_archive(
        str(tmp_path / "karte-without-provenance"),
        "zip",
        root_dir=bundle.parent,
        base_dir=bundle.name,
    )

    result = subprocess.run(
        ["bash", str(installer), archive],
        text=True,
        capture_output=True,
        env=environment,
    )

    assert result.returncode == 1
    assert "missing embedded build provenance" in result.stderr
    assert not (installer.parents[1] / "data/runtime/karte/Karte.app").exists()


def test_installer_copies_valid_bundle_with_embedded_provenance(tmp_path):
    installer, environment = _install_harness(tmp_path)
    bundle = _karte_bundle(tmp_path, with_provenance=True)

    result = subprocess.run(
        ["bash", str(installer), str(bundle)],
        text=True,
        capture_output=True,
        env=environment,
    )

    destination = installer.parents[1] / "data/runtime/karte/Karte.app"
    assert result.returncode == 0, result.stderr
    assert f"Installed bundled Karte at {destination}" in result.stdout
    assert (destination / "Contents/MacOS/karte").stat().st_mode & 0o111
    assert json.loads(
        (destination / "Contents/Resources/karte-build-provenance.json").read_text()
    ) == {"source_revision": "a" * 40}

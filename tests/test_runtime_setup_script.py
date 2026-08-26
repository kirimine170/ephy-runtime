from pathlib import Path
import shutil
import subprocess

import pytest


@pytest.mark.parametrize(("server", "executable", "expected", "message"), [
    (None, False, 1, "executable is missing"),
    ("exit 0", False, 1, "file is not executable"),
    ("exit 1", True, 1, "cannot load its dynamic libraries"),
    ("exit 0", True, 0, "dynamic libraries are loadable"),
])
def test_setup_checks_server_and_continues_diagnostics(tmp_path, server, executable, expected, message):
    source = Path(__file__).resolve().parents[1] / "scripts"
    scripts = tmp_path / "scripts"
    scripts.mkdir()
    for filename in ("_runtime_common.sh", "check_runtime_setup.sh"):
        shutil.copy2(source / filename, scripts / filename)
    python = tmp_path / ".venv/bin/python"
    python.parent.mkdir(parents=True)
    python.write_text("#!/bin/sh\nexit 1\n")
    python.chmod(0o755)
    if server is not None:
        binary = tmp_path / "llama.cpp/build/bin/llama-server"
        binary.parent.mkdir(parents=True)
        binary.write_text("#!/bin/sh\n" + server + "\n")
        binary.chmod(0o755 if executable else 0o644)
    result = subprocess.run(["bash", str(scripts / "check_runtime_setup.sh")], text=True, capture_output=True)
    assert result.returncode == expected
    assert message in result.stdout + result.stderr
    assert "fast model" in result.stdout
    assert "qdrant binary" in result.stdout

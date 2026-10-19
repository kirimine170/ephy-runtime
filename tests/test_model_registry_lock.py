import os
from pathlib import Path
import subprocess
import sys

from packages.model_registry.service import ModelRegistry


def test_registry_lock_excludes_another_process_and_releases_after_exception(tmp_path):
    registry = ModelRegistry(tmp_path)
    child_code = """
from pathlib import Path
import sys
from packages.model_registry.service import ModelRegistry
print('ready', flush=True)
with ModelRegistry(Path(sys.argv[1]))._lock():
    print('acquired', flush=True)
"""
    child = None
    try:
        with registry._lock():
            env = {**os.environ, "PYTHONPATH": str(Path(__file__).resolve().parents[1])}
            child = subprocess.Popen([sys.executable, "-u", "-c", child_code, str(tmp_path)],
                                     stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, env=env)
            assert child.stdout.readline().strip() == "ready"
            # The child has reached its acquisition attempt but cannot complete it．
            try:
                child.communicate(timeout=0.3)
            except subprocess.TimeoutExpired:
                pass
            else:
                raise AssertionError("Registry lock did not exclude another process")
        stdout, stderr = child.communicate(timeout=10)
        assert child.returncode == 0, stderr
        assert stdout.strip() == "acquired"
        try:
            with registry._lock():
                raise RuntimeError("fixture")
        except RuntimeError:
            pass
        with registry._lock():
            pass
    finally:
        if child is not None and child.poll() is None:
            child.kill()
            child.communicate(timeout=10)

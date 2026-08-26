from pathlib import Path
import shutil
import subprocess


def test_packaged_launcher_preserves_root_environment_and_arguments(tmp_path):
    root = tmp_path / "runtime with spaces"
    scripts = root / "scripts"
    scripts.mkdir(parents=True)
    launcher = scripts / "start_conversation_app.sh"
    shutil.copyfile(Path(__file__).parents[1] / "scripts" / launcher.name, launcher)
    binary = root / "desktop/build/bin/desktop.app/Contents/MacOS/desktop"
    binary.parent.mkdir(parents=True)
    binary.write_text('#!/bin/bash\nprintf "%s\\n" "$PWD" "$EPHY_START_CONVERSATION" "$1"\n')
    binary.chmod(0o700)
    result = subprocess.run(["bash", str(launcher), "argument with spaces"], cwd=tmp_path,
                            text=True, capture_output=True, check=True)
    assert result.stdout.splitlines() == [str(root), "1", "argument with spaces"]


def test_packaged_launcher_explains_missing_build(tmp_path):
    scripts = tmp_path / "scripts"
    scripts.mkdir()
    launcher = scripts / "start_conversation_app.sh"
    shutil.copyfile(Path(__file__).parents[1] / "scripts" / launcher.name, launcher)
    result = subprocess.run(["bash", str(launcher)], text=True, capture_output=True)
    assert result.returncode == 1
    assert "wails build" in result.stderr

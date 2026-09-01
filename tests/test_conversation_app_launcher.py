from pathlib import Path
import shutil
import subprocess


def _copy_launch_scripts(scripts: Path) -> Path:
    source_scripts = Path(__file__).parents[1] / "scripts"
    launcher = scripts / "start_conversation_app.sh"
    shutil.copyfile(source_scripts / launcher.name, launcher)
    shutil.copyfile(source_scripts / "_karte_runtime.sh", scripts / "_karte_runtime.sh")
    return launcher


def test_packaged_launcher_preserves_root_environment_and_arguments(tmp_path):
    root = tmp_path / "runtime with spaces"
    scripts = root / "scripts"
    scripts.mkdir(parents=True)
    launcher = _copy_launch_scripts(scripts)
    binary = root / "desktop/build/bin/ephy-runtime.app/Contents/MacOS/ephy-runtime"
    binary.parent.mkdir(parents=True)
    binary.write_text('#!/bin/bash\nprintf "%s\\n" "$PWD" "$EPHY_START_CONVERSATION" "$1"\n')
    binary.chmod(0o700)
    result = subprocess.run(["bash", str(launcher), "argument with spaces"], cwd=tmp_path,
                            text=True, capture_output=True, check=True)
    assert result.stdout.splitlines() == [str(root), "1", "argument with spaces"]


def test_launcher_falls_back_to_runnable_unpacked_binary(tmp_path):
    root = tmp_path / "runtime"
    scripts = root / "scripts"
    scripts.mkdir(parents=True)
    launcher = _copy_launch_scripts(scripts)
    binary = root / "desktop/build/bin/ephy-runtime"
    binary.parent.mkdir(parents=True)
    binary.write_text('#!/bin/bash\nprintf "%s\\n" "$PWD" "$EPHY_START_CONVERSATION"\n')
    binary.chmod(0o700)

    result = subprocess.run(["bash", str(launcher)], cwd=tmp_path, text=True, capture_output=True, check=True)

    assert result.stdout.splitlines() == [str(root), "1"]


def test_packaged_launcher_explains_missing_build(tmp_path):
    scripts = tmp_path / "scripts"
    scripts.mkdir()
    launcher = _copy_launch_scripts(scripts)
    result = subprocess.run(["bash", str(launcher)], text=True, capture_output=True)
    assert result.returncode == 1
    assert "build_conversation_app.sh" in result.stderr


def test_bundled_karte_runtime_launcher():
    test_script = Path(__file__).parents[1] / "scripts" / "test_karte_runtime.sh"
    result = subprocess.run(["bash", str(test_script)], text=True, capture_output=True, check=True)
    assert "Bundled Karte runtime test passed．" in result.stdout

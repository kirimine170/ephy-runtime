from pathlib import Path
import os
import shutil
import subprocess

import pytest


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


def _run_shell_with_cd_environment(arguments, *, cwd, cdpath, noisy_cd=False, **environment):
    bootstrap = ""
    if noisy_cd:
        bootstrap = r'''
cd() {
  printf 'output from an exported cd function\n'
  builtin cd "$@"
}
export -f cd
'''
    bootstrap += '\nexec bash --noprofile --norc "$@"\n'
    # No user Karte override or shell startup file enters these scratch tests．
    return subprocess.run(
        ["bash", "--noprofile", "--norc", "-c", bootstrap, "launcher-test", *arguments],
        cwd=cwd,
        env={"PATH": os.environ.get("PATH", os.defpath), "CDPATH": cdpath, **environment},
        text=True,
        capture_output=True,
        timeout=5,
    )


@pytest.mark.parametrize("noisy_cd", [False, True])
def test_relative_karte_directory_ignores_cdpath_and_exported_cd(tmp_path, noisy_cd):
    root = tmp_path / "runtime with spaces"
    scripts = root / "scripts"
    scripts.mkdir(parents=True)
    _copy_launch_scripts(scripts)
    executable = root / "data/runtime/karte/Karte.app/Contents/MacOS/karte"
    executable.parent.mkdir(parents=True)
    executable.write_text("#!/bin/bash\nexit 97\n")
    executable.chmod(0o700)
    cwd = tmp_path / "working directory"
    data_dir = cwd / "relative data"
    data_dir.mkdir(parents=True)
    cdpath_root = tmp_path / "cdpath search"
    shadow_dir = cdpath_root / data_dir.name
    shadow_dir.mkdir(parents=True)

    command = r'''
set -euo pipefail
source "$1"
# Return this shell's PID after pointer persistence，without querying or starting Karte．
karte_runtime_pid_from_data_dir() { printf '%s\n' "$$"; }
start_bundled_karte_runtime "$2"
printf '%s\n' "$KARTE_DATA_DIR" "$PWD" "$$"
'''
    result = _run_shell_with_cd_environment(
        ["-c", command, "karte-test", str(scripts / "_karte_runtime.sh"), str(root)],
        cwd=cwd,
        cdpath=f"{cdpath_root}:.",
        noisy_cd=noisy_cd,
        KARTE_DATA_DIR=data_dir.name,
        EPHY_KARTE_EXECUTABLE=str(executable),
        EPHY_START_KARTE="1",
        EPHY_KARTE_LAUNCH_MODE="direct",
    )

    assert result.returncode == 0, result.stderr
    lines = result.stdout.splitlines()
    assert lines[:2] == [str(data_dir.resolve()), str(cwd)]
    assert len(lines) == 3
    assert lines[2].isdigit()
    assert (root / "data/runtime/pids/karte.pid").read_text() == lines[2] + "\n"
    pointer = root / "data/runtime/karte/.karte-data-dir"
    assert pointer.read_bytes() == str(data_dir.resolve()).encode() + b"\n"
    assert (data_dir / "content").is_dir()
    assert not (shadow_dir / "content").exists()


@pytest.mark.parametrize("noisy_cd", [False, True])
def test_relative_launcher_ignores_cdpath_and_preserves_cwd_and_arguments(tmp_path, noisy_cd):
    root = tmp_path / "runtime with spaces"
    shadow_root = tmp_path / "cdpath search" / root.name
    for candidate in (root, shadow_root):
        scripts = candidate / "scripts"
        scripts.mkdir(parents=True)
        _copy_launch_scripts(scripts)
        binary = candidate / "desktop/build/bin/ephy-runtime.app/Contents/MacOS/ephy-runtime"
        binary.parent.mkdir(parents=True)
        binary.write_text('#!/bin/bash\nprintf "%s\\n" "$PWD" "$EPHY_START_CONVERSATION" "$#" "$@"\n')
        binary.chmod(0o700)
    launcher = root / "scripts/start_conversation_app.sh"
    arguments = ["argument with spaces", "--option=value", ""]

    result = _run_shell_with_cd_environment(
        [str(launcher.relative_to(tmp_path)), *arguments],
        cwd=tmp_path,
        cdpath=f"{shadow_root.parent}:.",
        noisy_cd=noisy_cd,
        EPHY_START_KARTE="0",
    )

    assert result.returncode == 0, result.stderr
    assert result.stdout.splitlines() == [str(root), "1", "3", *arguments]


@pytest.mark.parametrize("separator", ["\n", "\r"])
def test_invalid_line_break_in_karte_pointer_preserves_existing_pointer(tmp_path, separator):
    scripts = tmp_path / "scripts"
    scripts.mkdir()
    _copy_launch_scripts(scripts)
    placed_dir = tmp_path / "app directory"
    placed_dir.mkdir()
    valid_dir = tmp_path / "valid data"
    valid_dir.mkdir()
    pointer = placed_dir / ".karte-data-dir"
    original = str(valid_dir).encode() + b"\n"
    pointer.write_bytes(original)
    original_mtime = pointer.stat().st_mtime_ns
    invalid_dir = tmp_path / f"invalid{separator}data"
    invalid_dir.mkdir()

    result = _run_shell_with_cd_environment(
        ["-c", 'source "$1"; persist_karte_runtime_data_dir "$2" "$3"', "pointer-test",
         str(scripts / "_karte_runtime.sh"), str(placed_dir), str(invalid_dir)],
        cwd=tmp_path,
        cdpath=".",
        noisy_cd=True,
    )

    assert result.returncode == 1
    assert "Refusing invalid Karte data directory pointer．" in result.stderr
    assert pointer.read_bytes() == original
    assert pointer.stat().st_mtime_ns == original_mtime
    assert not list(placed_dir.glob(".karte-data-dir.tmp.*"))


def test_bundled_karte_runtime_launcher():
    test_script = Path(__file__).parents[1] / "scripts" / "test_karte_runtime.sh"
    result = subprocess.run(["bash", str(test_script)], text=True, capture_output=True, check=True)
    assert "Bundled Karte runtime test passed．" in result.stdout

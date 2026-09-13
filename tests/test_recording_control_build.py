import subprocess

from scripts import build_recording_control as control


def test_pinned_source_does_not_require_an_old_worktree_or_copy_local_edits(tmp_path, monkeypatch):
    source = tmp_path / 'karte'
    subprocess.run(['git', 'init', '-q', str(source)], check=True)
    (source / 'source.txt').write_text('pinned source')
    subprocess.run(['git', '-C', str(source), 'add', '.'], check=True)
    subprocess.run(['git', '-C', str(source), '-c', 'user.name=Fixture', '-c',
                    'user.email=fixture@example.invalid', 'commit', '-qm', 'pinned'], check=True)
    revision = subprocess.check_output(['git', '-C', str(source), 'rev-parse', 'HEAD'], text=True).strip()
    monkeypatch.setattr(control, 'KARTE_REVISION', revision)
    (source / 'source.txt').write_text('current local edit')
    (source / 'untracked.txt').write_text('local only')
    with control.pinned_source(source) as exported:
        assert (exported / 'source.txt').read_text() == 'pinned source'
        assert not (exported / 'untracked.txt').exists()
    assert not exported.exists()
    assert (source / 'source.txt').read_text() == 'current local edit'

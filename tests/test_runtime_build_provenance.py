import importlib.util
import hashlib
import json
from pathlib import Path
import subprocess

import pytest

spec = importlib.util.spec_from_file_location('runtime_build_provenance', Path(__file__).parents[1] / 'scripts/runtime_build_provenance.py')
provenance = importlib.util.module_from_spec(spec)
spec.loader.exec_module(provenance)


def repository(root):
    subprocess.run(['git', 'init', '-q', str(root)], check=True)
    (root / 'source.txt').write_text('source')
    subprocess.run(['git', '-C', str(root), 'add', '.'], check=True)
    subprocess.run(['git', '-C', str(root), '-c', 'user.name=Fixture', '-c', 'user.email=fixture@example.invalid', 'commit', '-qm', 'fixture'], check=True)
    return root


def test_provenance_hashes_final_binary_and_rejects_a_source_change(tmp_path):
    root = repository(tmp_path / 'repo')
    before = provenance.source_snapshot(root)
    assert before['source_dirty'] is False
    binary = tmp_path / 'binary'; binary.write_bytes(b'final signed executable')
    result = provenance.finish(root, before, binary, tmp_path / 'provenance.json')
    assert result['executable_sha256'] == hashlib.sha256(binary.read_bytes()).hexdigest()
    (root / 'source.txt').write_text('changed during build')
    with pytest.raises(ValueError, match='Source changed'):
        provenance.finish(root, before, binary, tmp_path / 'bad.json')
    assert not (tmp_path / 'bad.json').exists()


def test_provenance_tracks_uncommitted_edits_without_exposing_their_text(tmp_path):
    root = repository(tmp_path / 'repo')
    (root / 'new.txt').write_text('uncommitted fixture')
    before = provenance.source_snapshot(root)
    assert before['source_dirty'] is True
    assert 'fixture' not in str(before)
    (root / 'new.txt').write_text('second fixture')
    assert provenance.source_snapshot(root) != before


def test_bundle_provenance_checks_the_packaged_helper(tmp_path):
    root = repository(tmp_path / 'repo')
    before = provenance.source_snapshot(root)
    contents = tmp_path / 'app/Contents'
    for directory in ['MacOS', 'Helpers', 'Resources']:
        (contents / directory).mkdir(parents=True)
    binary = contents / 'MacOS/ephy-runtime'; binary.write_bytes(b'app')
    helper = contents / 'Helpers/ephy-whisper'; helper.write_bytes(b'helper')
    digest = hashlib.sha256(helper.read_bytes()).hexdigest()
    (contents / 'Resources/whisper-build-provenance.json').write_text(json.dumps({'helper_sha256':digest, 'whisper_cpp':{'commit':'pinned'}}))
    result = provenance.finish(root, before, binary, tmp_path / 'proof.json')
    assert result['whisper_helper_sha256'] == digest
    helper.write_bytes(b'stale helper')
    with pytest.raises(ValueError, match='Bundled ASR helper'):
        provenance.finish(root, before, binary, tmp_path / 'bad.json')


def test_bundle_provenance_rejects_replaced_karte_control(tmp_path):
    root = repository(tmp_path / 'repo')
    before = provenance.source_snapshot(root)
    contents = tmp_path / 'app/Contents'
    for directory in ['MacOS', 'Helpers', 'Resources']:
        (contents / directory).mkdir(parents=True)
    binary = contents / 'MacOS/ephy-runtime'; binary.write_bytes(b'app')
    helper = contents / 'Helpers/karte-ephy-control'; helper.write_bytes(b'control')
    digest = hashlib.sha256(helper.read_bytes()).hexdigest()
    (contents / 'Resources/karte-control-provenance.json').write_text(json.dumps({'executable_sha256':digest, 'protocol_version':'2.0', 'source_revision':'pinned', 'source_dirty':False}))
    result = provenance.finish(root, before, binary, tmp_path / 'proof.json')
    assert result['karte_control_sha256'] == digest and result['karte_source_revision'] == 'pinned'
    helper.write_bytes(b'replaced control')
    with pytest.raises(ValueError, match='Bundled Karte control'):
        provenance.finish(root, before, binary, tmp_path / 'bad.json')

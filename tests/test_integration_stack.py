import json
from pathlib import Path
from unittest.mock import Mock
import urllib.error

import pytest

from scripts import integration_stack as stack


def test_running_service_is_reused_without_spawn(tmp_path, monkeypatch):
    monkeypatch.setattr(stack, 'listener', lambda port: 123)
    monkeypatch.setattr(stack, 'fetch', lambda *args: {'status': 'ok'})
    monkeypatch.setattr(stack, 'process_identity', lambda pid: 'same process')
    spawn = Mock()
    monkeypatch.setattr(stack.subprocess, 'Popen', spawn)
    result = stack.ensure_service({'logs': tmp_path}, 'fixture', 1234, '/health', ['unused'])
    assert result == {'pid': 123, 'identity': 'same process', 'started': False}
    spawn.assert_not_called()


def test_busy_port_never_starts_a_second_service(tmp_path, monkeypatch):
    monkeypatch.setattr(stack, 'listener', lambda port: 123)
    monkeypatch.setattr(stack, 'fetch', Mock(side_effect=urllib.error.HTTPError('local', 503, '', {}, None)))
    spawn = Mock()
    monkeypatch.setattr(stack.subprocess, 'Popen', spawn)
    monkeypatch.setattr(stack.time, 'monotonic', Mock(side_effect=[0, 0, 2]))
    monkeypatch.setattr(stack.time, 'sleep', lambda seconds: None)
    with pytest.raises(RuntimeError, match='timed out'):
        stack.ensure_service({'logs': tmp_path}, 'fixture', 1234, '/health', ['unused'], timeout=1)
    spawn.assert_not_called()


def test_missing_service_starts_and_verifies_its_owner(tmp_path, monkeypatch):
    monkeypatch.setattr(stack, 'listener', Mock(side_effect=[None, 987]))
    monkeypatch.setattr(stack, 'fetch', lambda *args: {'status': 'ok'})
    monkeypatch.setattr(stack, 'process_identity', lambda pid: 'own process')
    child = Mock(pid=987)
    child.poll.return_value = None
    spawn = Mock(return_value=child)
    monkeypatch.setattr(stack.subprocess, 'Popen', spawn)
    assert stack.ensure_service({'logs': tmp_path}, 'fixture', 1234, '/health', ['fixture'])['started']
    spawn.assert_called_once()


def test_failed_child_does_not_report_ready(tmp_path, monkeypatch):
    monkeypatch.setattr(stack, 'listener', lambda port: None)
    child = Mock()
    child.poll.return_value = 1
    monkeypatch.setattr(stack.subprocess, 'Popen', Mock(return_value=child))
    with pytest.raises(RuntimeError, match='exited'):
        stack.ensure_service({'logs': tmp_path}, 'fixture', 1234, '/health', ['fixture'])


def test_browser_artifact_is_rejected_before_launch(tmp_path):
    stack.write_json(tmp_path / 'current.json', {'release': 'fixture'})
    stack.write_json(tmp_path / 'releases/fixture/manifest.json', {'karte_native_api': False})
    with pytest.raises(ValueError, match='not native'):
        stack.release_files({'home': tmp_path})


def test_changed_release_file_is_rejected(tmp_path):
    stack.write_json(tmp_path / 'current.json', {'release': 'fixture'})
    release = tmp_path / 'releases/fixture'
    release.mkdir(parents=True)
    (release / 'binary').write_bytes(b'original')
    expected = stack.digest(release / 'binary')
    stack.write_json(release / 'manifest.json', {'karte_native_api': True, 'files': {'binary': expected}})
    (release / 'binary').write_bytes(b'changed')
    with pytest.raises(ValueError, match='differs'):
        stack.release_files({'home': tmp_path})


def test_stop_never_signals_reused_pid(tmp_path, monkeypatch):
    stack.write_json(tmp_path / 'processes.json', {'services': {'gateway': {'pid': 42, 'identity': 'original'}}})
    monkeypatch.setattr(stack, 'process_identity', lambda pid: 'unrelated replacement')
    kill = Mock()
    monkeypatch.setattr(stack.os, 'kill', kill)
    with pytest.raises(RuntimeError, match='identity'):
        stack.stop({'home': tmp_path})
    kill.assert_not_called()


def test_unsaved_native_window_keeps_dependencies_alive(tmp_path, monkeypatch):
    stack.write_json(tmp_path / 'processes.json', {'services': {
        'runtime': {'pid': 42, 'identity': 'runtime', 'app': '/fixture.app'},
        'gateway': {'pid': 43, 'identity': 'gateway'}}})
    monkeypatch.setattr(stack, 'process_identity', lambda pid: {42: 'runtime', 43: 'gateway'}[pid])
    monkeypatch.setattr(stack, 'native_tool', lambda config: Path('/fixture-tool'))
    monkeypatch.setattr(stack.subprocess, 'run', Mock())
    monkeypatch.setattr(stack.time, 'monotonic', Mock(side_effect=[0, 21]))
    kill = Mock()
    monkeypatch.setattr(stack.os, 'kill', kill)
    with pytest.raises(RuntimeError, match='not closed'):
        stack.stop({'home': tmp_path})
    kill.assert_not_called()


@pytest.mark.skipif(stack.fcntl is None, reason='native macOS lifecycle lock')
def test_lifecycle_lock_prevents_overlapping_start_or_stop(tmp_path):
    with stack.exclusive({'home': tmp_path}):
        with pytest.raises(RuntimeError, match='still running'):
            with stack.exclusive({'home': tmp_path}):
                pytest.fail('second operation entered')


def test_asr_worker_must_belong_to_this_runtime(monkeypatch):
    monkeypatch.setattr(stack, 'command', lambda *args: '41 99 /fixture/ephy-whisper\n42 123 /fixture/ephy-whisper')
    monkeypatch.setattr(stack, 'process_identity', lambda pid: 'worker')
    assert stack.whisper_child(123, Path('/fixture/ephy-whisper'))['pid'] == 42


def test_changed_whisper_source_requires_rebuild(tmp_path, monkeypatch):
    monkeypatch.setattr(stack, 'ROOT', tmp_path)
    source = tmp_path / 'desktop/voice/whisper/worker.cpp'
    source.parent.mkdir(parents=True)
    source.write_text('original worker')
    (tmp_path / 'bin').mkdir()
    (tmp_path / 'bin/ephy-whisper').write_bytes(b'fixture binary')
    stack.write_json(tmp_path / 'scripts/asr/assets.json', {'whisper_cpp': {'commit': 'fixture'}})
    stack.write_json(tmp_path / 'bin/whisper-build-provenance.json', {
        'helper_sha256': stack.digest(tmp_path / 'bin/ephy-whisper'),
        'local_source_files': {'desktop/voice/whisper/worker.cpp': stack.digest(source)},
        'whisper_cpp': {'commit': 'fixture'}})
    assert stack.helper_is_current()
    source.write_text('updated worker')
    assert not stack.helper_is_current()


def test_tts_catalog_without_completed_audio_does_not_pass_preflight(tmp_path, monkeypatch):
    from contextlib import nullcontext
    profile = {'voice_profile_id': 'fixture', 'model_revision': 'fixture',
               'reference_group_digest': 'fixture', 'synthesis_config_digest': 'fixture', 'default_style': {}}
    monkeypatch.setattr(stack.OPENER, 'open', lambda *args, **kwargs: nullcontext([b'{"type":"completed"}\n']))
    with pytest.raises(RuntimeError, match='preflight'):
        stack.warm_tts({'home': tmp_path}, {'pid': 42, 'identity': 'fixture'}, 'fixture', profile)
    assert not (tmp_path / 'tts-ready.json').exists()


def test_spawned_service_is_cleaned_up_after_readiness_timeout(tmp_path, monkeypatch):
    monkeypatch.setattr(stack, 'listener', lambda port: None)
    monkeypatch.setattr(stack, 'fetch', Mock(side_effect=ConnectionError()))
    monkeypatch.setattr(stack.time, 'monotonic', Mock(side_effect=[0, 0, 2]))
    monkeypatch.setattr(stack.time, 'sleep', lambda seconds: None)
    child = Mock()
    child.poll.return_value = None
    monkeypatch.setattr(stack.subprocess, 'Popen', Mock(return_value=child))
    with pytest.raises(RuntimeError, match='timed out'):
        stack.ensure_service({'logs': tmp_path}, 'fixture', 1234, '/health', ['fixture'], timeout=1)
    child.terminate.assert_called_once()
    child.wait.assert_called_once_with(timeout=5)
    child.kill.assert_not_called()


def test_config_resolves_relative_absolute_and_user_paths(tmp_path):
    path = tmp_path / 'config/integration.json'
    value = {key: '../assets' for key in stack.PATH_KEYS}
    value.update(schema_version=1, home=str(tmp_path / 'runtime'), logs='../logs', node_bin='~')
    stack.write_json(path, value)
    config = stack.load_config(path)
    assert config['home'] == tmp_path / 'runtime'
    assert config['logs'] == tmp_path / 'logs'
    assert config['karte_root'] == tmp_path / 'assets'
    assert config['node_bin'] == Path.home()


@pytest.mark.skipif(stack.sys.platform == 'win32', reason='macOS virtual environment interpreter')
def test_config_keeps_virtual_environment_python_symlink(tmp_path):
    base = tmp_path / 'base-python'
    base.touch()
    python = tmp_path / 'venv/bin/python'
    python.parent.mkdir(parents=True)
    python.symlink_to(base)
    value = {key: 'assets' for key in stack.PATH_KEYS}
    value.update(schema_version=1, home='home', logs='logs', tts_python='venv/bin/python')
    path = tmp_path / 'integration.json'
    stack.write_json(path, value)
    assert stack.load_config(path)['tts_python'] == python
    assert python.resolve() != python


def test_karte_ready_wait_covers_slow_first_initialization(tmp_path, monkeypatch):
    marker = tmp_path / '.mdsys/runtime/karte.pid'
    stack.write_json(tmp_path / '.mdsys/context/v2/capabilities.json', {'protocol_version': '2.0'})
    marker.parent.mkdir(parents=True)
    marker.write_text('1')
    monkeypatch.setattr(stack, 'process_identity', lambda pid: 'current')
    monkeypatch.setattr(stack.time, 'monotonic', Mock(side_effect=[0, 0, 45]))
    monkeypatch.setattr(stack.time, 'sleep', lambda _: marker.write_text('42'))
    stack.wait_for_karte({'data_root': tmp_path}, {'pid': 42, 'identity': 'current'})


def test_karte_exit_fails_readiness_without_waiting_for_deadline(tmp_path, monkeypatch):
    monkeypatch.setattr(stack, 'process_identity', lambda pid: None)
    with pytest.raises(RuntimeError, match='exited'):
        stack.wait_for_karte({'data_root': tmp_path}, {'pid': 42, 'identity': 'original'})

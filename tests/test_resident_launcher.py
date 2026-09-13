from pathlib import Path
from unittest.mock import Mock

import pytest

from scripts import resident_app as resident


def installation(state):
    resident.config_for(state)
    resident.lifecycle.write_json(state / 'installation.json', {'schema_version': 1, 'runtime_root': str(resident.ROOT), 'state_root': str(state.resolve())})


def test_prepare_copy_does_not_overwrite_existing_selection(tmp_path):
    original = tmp_path / 'normal.json'
    chosen = tmp_path / 'resident.json'
    original.write_text('normal')
    chosen.write_text('resident choice')
    resident.copy_new(original, chosen)
    assert chosen.read_text() == 'resident choice'
    assert original.read_text() == 'normal'


def test_state_rejects_git_and_symlink(tmp_path):
    repo = tmp_path / 'repo'
    repo.mkdir()
    (repo / '.git').write_text('gitdir: fixture')
    with pytest.raises(ValueError, match='outside Git'):
        resident.private_directory(repo / 'state')
    link = tmp_path / 'link'
    link.symlink_to(repo)
    with pytest.raises(ValueError, match='non-symlink'):
        resident.private_directory(link)


def test_running_foreign_gateway_is_never_adopted(monkeypatch):
    monkeypatch.setattr(resident.lifecycle, 'listener', lambda port: 42)
    monkeypatch.setattr(resident.lifecycle, 'process_identity', lambda pid: 'foreign')
    with pytest.raises(RuntimeError, match='another process'):
        resident.owned_or_free({}, 'gateway', resident.GATEWAY_PORT, {})
    with pytest.raises(RuntimeError, match='another process'):
        resident.owned_or_free({}, 'gateway', resident.GATEWAY_PORT, {'gateway': {'pid': 42, 'identity': 'old'}})
    resident.owned_or_free({}, 'gateway', resident.GATEWAY_PORT, {'gateway': {'pid': 42, 'identity': 'foreign'}})


def test_environment_keeps_identity_but_separates_storage_and_credentials(tmp_path, monkeypatch):
    (tmp_path / 'speech-token').write_text('new-private-token')
    monkeypatch.setenv('EPHY_TTS_BEARER_TOKEN', 'normal-private-token')
    monkeypatch.setenv('KARTE_DATA_DIR', '/normal-private-memory')
    monkeypatch.setenv('OPENAI_API_KEY', 'cloud-key')
    info = {'node_bin': '/local/node', 'instance_id': 'same-instance', 'voice_profile_id': 'chosen-voice'}
    env = resident.environment(tmp_path, info)
    assert env['EPHY_TTS_BEARER_TOKEN'] == 'new-private-token'
    assert env['KARTE_DATA_DIR'] == str(tmp_path / 'karte')
    assert env['EPHY_RECORDING_HOME'] == str(tmp_path / 'recording')
    assert env['EPHY_RESIDENT_INSTANCE_ID'] == 'same-instance'
    assert env['EPHY_RESIDENT_VOICE_PROFILE_ID'] == 'chosen-voice'
    assert 'OPENAI_API_KEY' not in env
    assert 'EPHY_START_CONVERSATION' not in env
    assert env['EPHY_GATEWAY_URL'].endswith(':18900')


def test_close_monitor_does_not_stop_replacement_launch(tmp_path, monkeypatch):
    installation(tmp_path)
    config = resident.config_for(tmp_path)
    old = {'runtime': {'pid': 42, 'identity': 'old'}}
    resident.save_records(tmp_path, old)
    def identity(pid):
        resident.save_records(tmp_path, {'runtime': {'pid': 43, 'identity': 'new'}})
        return None
    monkeypatch.setattr(resident.lifecycle, 'process_identity', identity)
    stop = Mock()
    monkeypatch.setattr(resident, 'stop', stop)
    resident.watch(tmp_path)
    stop.assert_not_called()


def test_close_monitor_reaps_only_recorded_launch(tmp_path, monkeypatch):
    installation(tmp_path)
    resident.save_records(tmp_path, {'runtime': {'pid': 42, 'identity': 'app'}})
    monkeypatch.setattr(resident.lifecycle, 'process_identity', lambda pid: None)
    stop = Mock()
    monkeypatch.setattr(resident, 'stop', stop)
    resident.watch(tmp_path)
    stop.assert_called_once_with(tmp_path.resolve())


def test_stop_rejects_foreign_or_shared_process_manifest(tmp_path, monkeypatch):
    installation(tmp_path)
    stop = Mock()
    monkeypatch.setattr(resident.lifecycle, 'stop', stop)
    resident.lifecycle.write_json(tmp_path / 'processes.json', {'services': {'fast': {'pid': 42}}})
    with pytest.raises(ValueError, match='does not belong'):
        resident.stop(tmp_path)
    resident.save_records(tmp_path, {'fast': {'pid': 42}})
    with pytest.raises(ValueError, match='shared service'):
        resident.stop(tmp_path)
    stop.assert_not_called()


def test_reopened_window_requires_new_monitor_even_if_old_monitor_is_alive(monkeypatch):
    monkeypatch.setattr(resident.lifecycle, 'process_identity', lambda pid: 'monitor')
    monitor = {'pid': 10, 'identity': 'monitor', 'runtime': {'pid': 42, 'identity': 'old-window'}}
    assert resident.monitor_matches(monitor, monitor['runtime'])
    assert not resident.monitor_matches(monitor, {'pid': 43, 'identity': 'new-window'})


def test_stale_monitor_target_does_not_watch_new_window(tmp_path, monkeypatch):
    installation(tmp_path)
    resident.save_records(tmp_path, {'runtime': {'pid': 43, 'identity': 'new'}})
    stop = Mock()
    monkeypatch.setattr(resident, 'stop', stop)
    resident.watch(tmp_path, {'pid': 42, 'identity': 'old'})
    stop.assert_not_called()


def test_failed_start_rolls_back_only_new_services(tmp_path, monkeypatch):
    installation(tmp_path)
    old = {'gateway': {'pid': 42, 'identity': 'existing'}}
    new = {'pid': 43, 'identity': 'new'}
    resident.save_records(tmp_path, old)
    def fail(*args, **kwargs):
        resident.save_records(tmp_path, {**old, 'irodori': new})
        raise RuntimeError('fixture startup failure')
    monkeypatch.setattr(resident, '_start', fail)
    stop = Mock()
    monkeypatch.setattr(resident.lifecycle, 'stop_services', stop)
    with pytest.raises(RuntimeError, match='fixture startup failure'):
        resident.start(tmp_path, {})
    assert stop.call_args.args[1] == {'irodori': new}

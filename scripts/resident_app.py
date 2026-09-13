#!/usr/bin/env python3
"""Prepare，build and run the isolated Wails resident app using existing local assets．"""
from __future__ import annotations

import argparse
import hashlib
import json
import os
from pathlib import Path
import secrets
import shutil
import signal
import subprocess
import sys
import tempfile
import time

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT))
from scripts import integration_stack as lifecycle

DEFAULT_STATE = ROOT.parents[1] / 'local-data/resident-feedback'
DEFAULT_BUILD = Path(tempfile.gettempdir()) / f'ephy-resident-feedback-{os.getuid()}'
GATEWAY_PORT = 18900
SPEECH_PORT = 18967


def private_directory(path: Path) -> Path:
    if not path.is_absolute() or path.is_symlink():
        raise ValueError('Resident state must be an absolute non-symlink directory．')
    path.mkdir(parents=True, exist_ok=True, mode=0o700)
    path = path.resolve()
    if any((p / '.git').exists() for p in (path, *path.parents)):
        raise ValueError('Resident state must be outside Git repositories．')
    path.chmod(0o700)
    return path


def config_for(state: Path) -> dict:
    state = private_directory(state)
    return {'home': state, 'logs': private_directory(state / 'logs')}


def manifest(state: Path) -> dict:
    value = json.loads((state / 'installation.json').read_text())
    if (value.get('schema_version') != 1 or value.get('runtime_root') != str(ROOT)
            or value.get('state_root') != str(state.resolve())):
        raise ValueError('This resident installation belongs to another worktree．')
    return value


def load_records(state: Path) -> dict:
    manifest(state)
    path = state / 'processes.json'
    if not path.exists():
        return {}
    report = json.loads(path.read_text())
    if report.get('runtime_root') != str(ROOT) or report.get('state_root') != str(state.resolve()):
        raise ValueError('The process list does not belong to this resident installation．')
    records = report['services']
    if not set(records) <= {'runtime', 'asr', 'karte', 'gateway', 'irodori'}:
        raise ValueError('The resident process list contains a shared service．')
    return records


def save_records(state: Path, records: dict) -> None:
    lifecycle.write_json(state / 'processes.json', {'runtime_root': str(ROOT), 'state_root': str(state.resolve()), 'services': records})


def copy_new(source: Path, target: Path) -> None:
    """Never overwrite an existing local choice or source file．"""
    if not source.is_file() or target.exists():
        return
    target.parent.mkdir(parents=True, exist_ok=True)
    shutil.copyfile(source, target)
    target.chmod(0o600)


def prepare(state: Path, baseline: Path, build_dir: Path) -> dict:
    import yaml
    baseline = baseline.resolve(strict=True)
    if baseline == ROOT or not (baseline / 'scripts/integration_stack.py').is_file():
        raise ValueError('Specify the existing normal runtime checkout as --baseline．')
    if (state / 'installation.json').exists():
        existing = manifest(state)
        if existing['baseline'] != str(baseline):
            raise ValueError('An existing resident installation has another baseline．')
        return existing
    normal_config = lifecycle.load_config(baseline / 'configs/integration.local.json')
    for normal_root in (normal_config['home'], normal_config['data_root']):
        if state.is_relative_to(normal_root) or normal_root.is_relative_to(state):
            raise ValueError('Resident state must be separate from the normal app and its memory．')
    # Only configuration and executable assets are copied．No memory/conversation DBs．
    for name in ('models.local.yaml', 'ephy.local.yaml', 'model-registry.local.json', 'runtime-selection.local.json'):
        copy_new(baseline / 'configs' / name, ROOT / 'configs' / name)
    for name in ('rag.local.yaml', 'web.local.yaml'):
        target = ROOT / 'configs' / name
        if not target.exists():
            content = ({'vector_db': {'provider': 'local_json', 'store_path': str(state / 'index/documents.json')},
                        'rag': {'embedding_provider': 'local_hash'}} if name.startswith('rag')
                       else {'web_search': {'enabled': False}})
            target.write_text(yaml.safe_dump(content))
            target.chmod(0o600)
    # Keep the existing Identity and base Profile read-only．The overlay lives in this state．
    ephy_path = ROOT / 'configs/ephy.local.yaml'
    ephy = yaml.safe_load(ephy_path.read_text()).get('ephy', {}) if ephy_path.exists() else {}
    if not ephy.get('enabled') or not ephy.get('instance_id'):
        raise ValueError('The normal app must have an explicit Identity/Profile configuration．')
    for folder in ('preferences', 'recording', 'karte', 'karte-config', 'tmp', 'index'):
        private_directory(state / folder)
    token_path = state / 'speech-token'
    if not token_path.exists():
        token_path.write_text(secrets.token_urlsafe(32))
        token_path.chmod(0o600)
    copy_new(normal_config['tts_config'], state / 'speech-config.json')
    # The helper and its provenance remain paired，and model weights are never copied．
    for name in ('ephy-whisper', 'whisper-build-provenance.json'):
        copy_new(baseline / 'bin' / name, ROOT / 'bin' / name)
    (ROOT / 'bin/ephy-whisper').chmod(0o755)
    notices = ROOT / 'bin/whisper-notices'
    if not notices.exists():
        shutil.copytree(baseline / 'bin/whisper-notices', notices, copy_function=shutil.copyfile)
    from scripts.asr.configure import configuration
    asr = configuration(ROOT / 'bin/ephy-whisper', normal_config['asr_models'], normal_config.get('asr_model_id', 'large-v3-turbo-f16'))
    lifecycle.write_json(state / 'asr.json', asr)
    # Expose the normal venv read-only to desktop's existing model catalog reader．
    venv = ROOT / '.venv'
    if not venv.exists():
        venv.symlink_to(baseline / '.venv', target_is_directory=True)
    result = {'schema_version': 1, 'runtime_root': str(ROOT), 'state_root': str(state), 'baseline': str(baseline),
              'base_commit': lifecycle.command('git', '-C', baseline, 'rev-parse', 'HEAD'),
              'build_dir': str(build_dir.absolute()), 'instance_id': str(ephy['instance_id']),
              'gateway_python': str(baseline / '.venv/bin/python'),
              'tts_python': str(normal_config['tts_python']), 'node_bin': str(normal_config['node_bin']),
              'karte_root': str(normal_config['karte_root']),
              'voice_profile_id': normal_config['voice_profile_id'],
              'shared_resources': 'Read-only Identity/Profile，model/reference assets and model servers at 8081/8082/8083．',
              'gateway_port': GATEWAY_PORT, 'speech_port': SPEECH_PORT}
    lifecycle.write_json(state / 'installation.json', result)
    return result


def environment(state: Path, info: dict) -> dict:
    # Explicit allowlist removes inherited tracing/cloud/normal-stack configuration．
    keep = ('PATH', 'HOME', 'USER', 'LOGNAME', 'LANG', 'SHELL', 'SYSTEMROOT')
    env = {key: os.environ[key] for key in keep if key in os.environ}
    env.update(PATH=info['node_bin'] + os.pathsep + env.get('PATH', '/usr/bin:/bin'),
               PYTHONPATH=str(ROOT), PYTHONDONTWRITEBYTECODE='1',
               TMPDIR=str(state / 'tmp'), EPHY_RUNTIME_ROOT=str(ROOT),
               EPHY_RESIDENT='1', EPHY_RESIDENT_ENABLED='1',
               EPHY_RESIDENT_NAMESPACE='resident-feedback', EPHY_RESIDENT_STATE_ROOT=str(state),
               EPHY_RESIDENT_INSTANCE_ID=info['instance_id'],
               EPHY_RESIDENT_VOICE_PROFILE_ID=info['voice_profile_id'],
               EPHY_GATEWAY_URL=f'http://127.0.0.1:{GATEWAY_PORT}',
               EPHY_PREFERENCE_DATA_ROOT=str(state / 'preferences'), EPHY_RECORDING_HOME=str(state / 'recording'),
               KARTE_DATA_DIR=str(state / 'karte'), EPHY_KARTE_CONFIG_ROOT=str(state / 'karte-config'),
               KARTE_LOG_DIR=str(state / 'logs'), EPHY_LOG_DIR=str(state / 'logs'),
               EPHY_ASR_PROVIDER='whisper-cpp', EPHY_ASR_CONFIG=str(state / 'asr.json'),
               EPHY_TTS_ENDPOINT=f'http://127.0.0.1:{SPEECH_PORT}',
               EPHY_TTS_BEARER_TOKEN=(state / 'speech-token').read_text().strip(),
               HF_HUB_OFFLINE='1', TRANSFORMERS_OFFLINE='1', HF_HUB_DISABLE_TELEMETRY='1',
               OTEL_SDK_DISABLED='true', OPENAI_AGENTS_DISABLE_TRACING='1')
    grants = state / 'participation-grants.json'
    if grants.is_file():
        env['EPHY_RESIDENT_PARTICIPATION_GRANTS'] = str(grants)
    return env


def build(state: Path, info: dict) -> None:
    env = environment(state, info)
    output = Path(info['build_dir'])
    env.update(EPHY_APP_OUTPUT_DIR=str(output), EPHY_REQUIRE_WHISPER='1',
               EPHY_KARTE_SOURCE_ROOT=info['karte_root'], EPHY_APP_BUNDLE_ID='com.wails.ephy-runtime.resident-feedback',
               EPHY_APP_DISPLAY_NAME='Ephy 常駐検証')
    subprocess.run(['npm', 'ci', '--ignore-scripts'], cwd=ROOT / 'desktop/frontend', env=env, check=True)
    subprocess.run(['bash', str(ROOT / 'scripts/build_conversation_app.sh')], cwd=ROOT, env=env, check=True)
    lifecycle.native_tool(config_for(state))


def owned_or_free(config: dict, name: str, port: int, records: dict) -> None:
    owner = lifecycle.listener(port)
    if owner is None:
        return
    record = records.get(name, {})
    if record.get('pid') != owner or lifecycle.process_identity(owner) != record.get('identity'):
        raise RuntimeError(f'Port {port} is owned by another process．It was not modified．')


def start_context_fixture(state: Path, records: dict, remember, env: dict) -> None:
    helper = state / 'karte-context-fixture'
    if not (state / 'participation-grants.json').is_file():
        return
    if not helper.is_file():
        raise ValueError('Prepare the fixture helper before enabling its grants．')
    from scripts.build_recording_control import KARTE_REVISION
    proof = json.loads(helper.with_suffix('.provenance.json').read_text())
    if proof.get('source_revision') != KARTE_REVISION or proof.get('executable_sha256') != lifecycle.digest(helper):
        raise ValueError('The fixture receiver differs from its recorded build．')
    old = records.get('karte', {})
    if old.get('pid') and lifecycle.process_identity(old['pid']) == old.get('identity'):
        return
    # This executes the pinned Karte contextcore，not a replacement search implementation．
    with (state / 'logs/karte-context.log').open('wb') as log:
        child = subprocess.Popen([str(helper), '--data-root', str(state / 'karte')], cwd=ROOT, env=env,
                                 stdin=subprocess.DEVNULL, stdout=log, stderr=log, start_new_session=True)
    try:
        remember('karte', {'pid': child.pid, 'identity': lifecycle.process_identity(child.pid), 'started': True})
        deadline = time.monotonic() + 5
        while time.monotonic() < deadline:
            if child.poll() is not None:
                raise RuntimeError('The isolated Karte fixture receiver exited．')
            if b'karte_context_ready' in (state / 'logs/karte-context.log').read_bytes()[:2048]:
                return
            time.sleep(.1)
        raise RuntimeError('The isolated Karte fixture receiver did not become ready．')
    except BaseException:
        child.terminate()
        try:
            child.wait(timeout=5)
        except subprocess.TimeoutExpired:
            child.kill()
            child.wait(timeout=5)
        raise


def start(state: Path, info: dict, *, gui: bool = True) -> dict:
    before = load_records(state)
    try:
        return _start(state, info, gui=gui)
    except BaseException:
        current = load_records(state)
        created = {name: record for name, record in current.items() if record != before.get(name)}
        if created:
            lifecycle.stop_services(config_for(state), created)
        raise


def monitor_matches(monitor: dict, watched: dict) -> bool:
    return (bool(monitor.get('pid')) and monitor.get('runtime') == watched
            and lifecycle.process_identity(monitor['pid']) == monitor.get('identity'))


def _start(state: Path, info: dict, *, gui: bool = True) -> dict:
    config = config_for(state)
    records = load_records(state)
    bundle = Path(info['build_dir']) / 'ephy-runtime.app'
    if gui and not bundle.is_dir():
        raise ValueError('Build the resident app first．')
    for name, port in (('gateway', GATEWAY_PORT), ('irodori', SPEECH_PORT)):
        owned_or_free(config, name, port, records)
    env = environment(state, info)
    # Bound diagnostics across launches．Private feedback retention is enforced by its store．
    for name in ('gateway', 'irodori'):
        log = config['logs'] / f'{name}.log'
        if log.exists() and log.stat().st_size > 2 * 1024 * 1024:
            log.replace(log.with_suffix('.previous.log'))
    def remember(name, record):
        records[name] = record
        save_records(state, records)
    start_context_fixture(state, records, remember, env)
    remember('gateway', lifecycle.ensure_service(config, 'gateway', GATEWAY_PORT, '/health',
        [info['gateway_python'], '-m', 'uvicorn', 'apps.gateway.main:app', '--host', '127.0.0.1',
         '--port', str(GATEWAY_PORT), '--no-access-log', '--log-level', 'warning'], cwd=ROOT, env=env, timeout=45))
    remember('irodori', lifecycle.ensure_service(config, 'irodori', SPEECH_PORT, '/health',
        [info['tts_python'], '-m', 'apps.speech', 'serve', '--config', str(state / 'speech-config.json'),
         '--port', str(SPEECH_PORT)], cwd=ROOT, env=env, token=env['EPHY_TTS_BEARER_TOKEN'], timeout=45))
    asr_state = 'not_started'
    if gui:
        current = records.get('runtime', {})
        if current.get('pid') and lifecycle.process_identity(current['pid']) == current.get('identity'):
            subprocess.run([str(lifecycle.native_tool(config)), 'activate', str(current['pid'])], check=True)
        else:
            subprocess.run(['codesign', '--verify', '--strict', '--deep', str(bundle)], check=True)
            opened = json.loads(lifecycle.command(lifecycle.native_tool(config), 'open', bundle, env=env))
            pid = opened['pid']
            remember('runtime', {'pid': pid, 'identity': lifecycle.process_identity(pid), 'app': str(bundle), 'started': True})
        expected = ROOT / 'bin/ephy-whisper'
        deadline = time.monotonic() + 40
        while time.monotonic() < deadline:
            if lifecycle.process_identity(records['runtime']['pid']) != records['runtime']['identity']:
                raise RuntimeError('Resident app exited during startup．')
            child = lifecycle.whisper_child(records['runtime']['pid'], expected)
            if child:
                remember('asr', child)
                asr_state = 'worker_started'
                break
            time.sleep(.25)
        else:
            asr_state = 'worker_not_confirmed'
    if gui:
        monitor_path = state / 'monitor.json'
        monitor = json.loads(monitor_path.read_text()) if monitor_path.exists() else {}
        watched = {'pid': records['runtime']['pid'], 'identity': records['runtime']['identity']}
        if not monitor_matches(monitor, watched):
            with (config['logs'] / 'monitor.log').open('ab') as log:
                child = subprocess.Popen([info['gateway_python'], str(Path(__file__).resolve()), 'watch', '--state', str(state),
                                          '--watch-runtime', json.dumps(watched)],
                                         cwd=ROOT, env=env, stdin=subprocess.DEVNULL, stdout=log, stderr=log, start_new_session=True)
            lifecycle.write_json(monitor_path, {'pid': child.pid, 'identity': lifecycle.process_identity(child.pid), 'runtime': watched})
    lifecycle.write_json(config['logs'] / 'startup.json', {'services': records, 'microphone_started': False,
                                                        'shared_model_servers': [8081, 8082, 8083]})
    return {'state': 'running', 'gateway_url': env['EPHY_GATEWAY_URL'], 'microphone_started': False,
            'app': str(bundle) if gui else None, 'asr_state': asr_state}


def stop(state: Path) -> None:
    config = config_for(state)
    load_records(state)
    if (state / 'processes.json').exists():
        lifecycle.stop(config)


def watch(state: Path, expected: dict | None = None) -> None:
    """Own no input/session state．Reap this launch's services when its GUI closes．"""
    config = config_for(state)
    records = load_records(state)
    app = records.get('runtime')
    if not app:
        return
    if expected is not None and any(app.get(key) != expected.get(key) for key in ('pid', 'identity')):
        return
    while lifecycle.process_identity(app['pid']) == app['identity']:
        time.sleep(1)
    for attempt in range(6):
        try:
            with lifecycle.exclusive(config):
                current = load_records(state)
                # A new app may have been opened before this watcher acquired its lock．
                if current.get('runtime') != app:
                    return
                stop(state)
            return
        except RuntimeError:
            if attempt == 5:
                raise
            time.sleep(1)


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=('prepare', 'fixture', 'build', 'start', 'stop', 'status', 'watch'))
    parser.add_argument('--state', type=Path, default=DEFAULT_STATE)
    parser.add_argument('--baseline', type=Path, default=ROOT.parent / 'ephy-runtime')
    parser.add_argument('--build-dir', type=Path, default=DEFAULT_BUILD)
    parser.add_argument('--services-only', action='store_true')
    parser.add_argument('--watch-runtime', type=json.loads, help=argparse.SUPPRESS)
    args = parser.parse_args()
    config = config_for(args.state)
    state = config['home']
    if args.action == 'watch':
        watch(state, args.watch_runtime)
        return
    with lifecycle.exclusive(config):
        if args.action == 'prepare':
            info = prepare(state, args.baseline, args.build_dir)
            print(json.dumps({'prepared': True, 'state': str(state), 'build_dir': info['build_dir']}))
        elif args.action == 'status':
            load_records(state)
            print(json.dumps(lifecycle.status(config), indent=2))
        elif args.action == 'stop':
            stop(state)
        else:
            info = manifest(state)
            if args.action == 'fixture':
                from scripts.reuse_core.karte_fixture import prepare as prepare_fixture, build as build_fixture
                prepare_fixture(state)
                build_fixture(Path(info['karte_root']), state / 'karte-context-fixture')
                print('実Karteの読み取り処理と合成資料を準備しました．専用アプリを起動し，観測モードで試せます．')
            elif args.action == 'build':
                build(state, info)
            else:
                print(json.dumps(start(state, info, gui=not args.services_only), ensure_ascii=False))


if __name__ == '__main__':
    try:
        main()
    except Exception as error:
        # All error messages are operational metadata，never configuration payloads．
        print(str(error), file=sys.stderr)
        sys.exit(1)

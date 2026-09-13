#!/usr/bin/env python3
"""Build，start，inspect and stop one local Ephy integration environment．"""
from __future__ import annotations

import argparse
from contextlib import contextmanager
from datetime import datetime, timezone
try:
    import fcntl
except ImportError:
    fcntl = None
import hashlib
import json
import os
from pathlib import Path
import plistlib
import shutil
import signal
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid

ROOT = Path(__file__).resolve().parents[1]
OPENER = urllib.request.build_opener(urllib.request.ProxyHandler({}))
PATH_KEYS = ('home', 'logs', 'karte_root', 'data_root', 'asr_source', 'asr_models',
             'tts_python', 'tts_config', 'tts_token_file', 'node_bin')


def digest(path):
    with Path(path).open('rb') as file:
        return hashlib.file_digest(file, 'sha256').hexdigest()


def write_json(path, value):
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_suffix(path.suffix + '.tmp')
    temporary.write_text(json.dumps(value, ensure_ascii=False, indent=2) + '\n')
    temporary.chmod(0o600)
    temporary.replace(path)


def load_config(path):
    path = path.resolve(strict=True)
    value = json.loads(path.read_text())
    if value.get('schema_version') != 1:
        raise ValueError('Unsupported integration configuration．')
    for key in PATH_KEYS:
        value[key] = (path.parent / Path(value[key]).expanduser()).resolve()
    for key in ('home', 'logs'):
        value[key].mkdir(parents=True, exist_ok=True)
    value['config_path'] = path
    return value


def command(*args, **kwargs):
    return subprocess.check_output([str(a) for a in args], text=True, **kwargs).strip()


def process_identity(pid):
    result = subprocess.run(['ps', '-p', str(pid), '-o', 'lstart=', '-o', 'comm='], capture_output=True, text=True)
    return result.stdout.strip() if result.returncode == 0 else None


def listener(port):
    result = subprocess.run(['lsof', '-nP', '-t', f'-iTCP:{port}', '-sTCP:LISTEN'], capture_output=True, text=True)
    if result.returncode not in (0, 1):
        raise RuntimeError(f'Cannot inspect port {port}．')
    owners = set(result.stdout.split())
    if len(owners) > 1:
        raise RuntimeError(f'Port {port} has multiple owners．')
    return int(next(iter(owners))) if owners else None


def fetch(url, token=None):
    headers = {'X-Forwarded-For': '127.0.0.1'}
    if token:
        headers['Authorization'] = 'Bearer ' + token
    with OPENER.open(urllib.request.Request(url, headers=headers), timeout=3) as response:
        raw = response.read()
        try:
            return json.loads(raw)
        except json.JSONDecodeError:
            return {'status': response.status}


def require_local(url, port):
    parsed = urllib.parse.urlparse(url or '')
    if parsed.scheme != 'http' or parsed.hostname not in ('127.0.0.1', 'localhost') or parsed.port != port:
        raise ValueError('The configured service is not the expected local endpoint．')


def ensure_service(config, name, port, route, args, *, cwd=ROOT, env=None, token=None, timeout=240):
    owner = listener(port)
    child = None
    if owner is None:
        with (config['logs'] / (name + '.log')).open('ab') as log:
            child = subprocess.Popen([str(a) for a in args], cwd=cwd, env=env,
                                     stdin=subprocess.DEVNULL, stdout=log, stderr=log, start_new_session=True)
        print(name + '：起動しています．', flush=True)
    try:
        until = time.monotonic() + timeout
        while time.monotonic() < until:
            if child is not None and child.poll() is not None:
                raise RuntimeError(name + ' exited during startup．See its local log．')
            try:
                fetch(f'http://127.0.0.1:{port}{route}', token)
                actual = listener(port)
                if actual != (child.pid if child is not None else owner):
                    raise RuntimeError(name + ' changed port owner during startup．')
                print(name + '：準備完了', flush=True)
                return {'pid': actual, 'identity': process_identity(actual), 'started': child is not None}
            except urllib.error.HTTPError as error:
                if error.code not in (429, 503):
                    raise RuntimeError(name + ' rejected its readiness check．') from None
            except (urllib.error.URLError, TimeoutError, ConnectionError):
                pass
            time.sleep(.5)
        raise RuntimeError(name + ' readiness timed out．')
    except BaseException:
        if child is not None and child.poll() is None:
            child.terminate()
            try:
                child.wait(timeout=5)
            except subprocess.TimeoutExpired:
                child.kill()
                child.wait(timeout=5)
        raise


def native_tool(config):
    source = Path(__file__).with_name('integration_app.swift')
    binary = config['home'] / 'bin/integration-app'
    stamp = binary.with_suffix('.sha256')
    expected = digest(source)
    if not binary.is_file() or not stamp.is_file() or stamp.read_text() != expected:
        binary.parent.mkdir(parents=True, exist_ok=True)
        subprocess.run(['swiftc', str(source), '-o', str(binary)], check=True)
        stamp.write_text(expected)
    return binary


def source_snapshot(root):
    from scripts.runtime_build_provenance import source_snapshot as snapshot
    return snapshot(root)


def copy_bundle(source, target):
    # Do not carry Finder/File Provider metadata into a signed bundle．
    shutil.copytree(source, target, symlinks=True, copy_function=shutil.copyfile)
    for original in source.rglob('*'):
        if original.is_file() and not original.is_symlink():
            (target / original.relative_to(source)).chmod(original.stat().st_mode & 0o777)
    subprocess.run(['codesign', '--verify', '--deep', '--strict', str(target)], check=True)


def helper_is_current():
    try:
        proof = json.loads((ROOT / 'bin/whisper-build-provenance.json').read_text())
        assets = json.loads((ROOT / 'scripts/asr/assets.json').read_text())
        local = {str(p.relative_to(ROOT)): digest(p) for p in sorted((ROOT / 'desktop/voice/whisper').glob('*')) if p.is_file()}
        return (proof['helper_sha256'] == digest(ROOT / 'bin/ephy-whisper')
                and proof['local_source_files'] == local and proof['whisper_cpp'] == assets['whisper_cpp'])
    except (OSError, ValueError, KeyError):
        return False


def build(config):
    env = {**os.environ, 'PATH': str(config['node_bin']) + os.pathsep + os.environ.get('PATH', ''),
           'EPHY_KARTE_SOURCE_ROOT': str(config['karte_root']), 'EPHY_REQUIRE_WHISPER': '1'}
    env.pop('VITE_USE_WAILS_STUBS', None)
    if not helper_is_current():
        subprocess.run([sys.executable, str(ROOT / 'scripts/asr/build_helper.py'), '--source', str(config['asr_source'])], env=env, check=True)
    before = {'runtime_source': source_snapshot(ROOT), 'karte_source': source_snapshot(config['karte_root'])}
    with tempfile.TemporaryDirectory(prefix='ephy-integration-build-') as temporary:
        staging = Path(temporary)
        env['EPHY_APP_OUTPUT_DIR'] = str(staging / 'runtime')
        subprocess.run([str(ROOT / 'scripts/build_conversation_app.sh')], cwd=ROOT, env=env, check=True)
        subprocess.run([str(config['karte_root'] / 'scripts/build_local_app.sh')], cwd=config['karte_root'], env=env, check=True)
        runtime = staging / 'runtime/ephy-runtime.app'
        karte = staging / 'Karte.app'
        (karte / 'Contents/MacOS').mkdir(parents=True)
        (karte / 'Contents/Resources').mkdir()
        binary = karte / 'Contents/MacOS/karte'
        shutil.copyfile(config['karte_root'] / 'build/bin/karte', binary)
        binary.chmod(0o755)
        info = {'CFBundlePackageType': 'APPL', 'CFBundleName': 'Karte', 'CFBundleExecutable': 'karte',
                'CFBundleIdentifier': 'com.wails.Karte', 'CFBundleVersion': '1.0.0',
                'CFBundleShortVersionString': '1.0.0', 'LSMinimumSystemVersion': '11.0',
                'NSHighResolutionCapable': True, 'NSMicrophoneUsageDescription': 'Karteで明示的に録音するときに使用します．'}
        (karte / 'Contents/Info.plist').write_bytes(plistlib.dumps(info))
        native = json.loads((config['karte_root'] / 'frontend/dist/karte-frontend-build.json').read_text())
        if native.get('native_api') is not True:
            raise RuntimeError('Karte was not built with native APIs．')
        write_json(karte / 'Contents/Resources/karte-frontend-build.json', native)
        subprocess.run([str(config['karte_root'] / 'scripts/package-macos-runtime.sh'), str(karte), 'darwin-arm64'],
                       cwd=config['karte_root'], env={**env, 'KARTE_REQUIRE_ASR_MODELS': '0'}, check=True)
        proof = {'schema_version': 1, 'runtime_source': source_snapshot(ROOT),
                 'karte_source': source_snapshot(config['karte_root']), 'karte_native_api': True}
        if any(proof[key] != value for key, value in before.items()):
            raise RuntimeError('Source changed while building the integration apps．Rebuild before starting．')
        proof['asr_model_id'] = config.get('asr_model_id', 'large-v3-turbo-f16')
        identifier = hashlib.sha256(json.dumps(proof, sort_keys=True).encode()).hexdigest()[:16]
        release = config['home'] / 'releases' / identifier
        if release.exists():
            raise RuntimeError('This source release already exists．Use start or change the source before rebuilding．')
        release.mkdir(parents=True)
        try:
            copy_bundle(runtime, release / 'Ephy Runtime.app')
            copy_bundle(karte, release / 'Karte.app')
            from scripts.asr.configure import configuration
            asr = configuration(release / 'Ephy Runtime.app/Contents/Helpers/ephy-whisper', config['asr_models'], config.get('asr_model_id', 'large-v3-turbo-f16'))
            write_json(release / 'asr.json', asr)
            proof['files'] = {str(p.relative_to(release)): digest(p) for p in [
                release / 'Ephy Runtime.app/Contents/MacOS/ephy-runtime',
                release / 'Ephy Runtime.app/Contents/Helpers/ephy-whisper',
                release / 'Ephy Runtime.app/Contents/Helpers/karte-ephy-control',
                release / 'Karte.app/Contents/MacOS/karte', release / 'asr.json']}
            write_json(release / 'manifest.json', proof)
            write_json(config['home'] / 'current.json', {'release': identifier})
        except Exception:
            shutil.rmtree(release)
            raise
    print('Karte，Runtime，Whisperを同じ起動構成へビルドしました．', flush=True)


def release_files(config):
    current = json.loads((config['home'] / 'current.json').read_text())
    release = config['home'] / 'releases' / current['release']
    if release.parent.resolve() != (config['home'] / 'releases').resolve():
        raise ValueError('Invalid release path．')
    proof = json.loads((release / 'manifest.json').read_text())
    if proof.get('karte_native_api') is not True:
        raise ValueError('The prepared Karte frontend is not native．')
    for relative, expected in proof['files'].items():
        path = release / relative
        if not path.resolve().is_relative_to(release.resolve()) or digest(path) != expected:
            raise ValueError('A prepared integration file differs from its build．')
    for name in ('Ephy Runtime.app', 'Karte.app'):
        subprocess.run(['codesign', '--verify', '--strict', '--deep', str(release / name)], check=True)
    return release, proof


def find_app(name, expected):
    result = subprocess.run(['pgrep', '-x', name], text=True, capture_output=True)
    if result.returncode not in (0, 1):
        raise RuntimeError('Cannot inspect native applications．')
    candidates = result.stdout.split()
    if len(candidates) > 1:
        raise RuntimeError(name + ' is running more than once．')
    if not candidates:
        return None
    executable = Path(command('ps', '-p', candidates[0], '-o', 'comm='))
    if executable != expected:
        raise RuntimeError(name + ' is running from another release．Close it before starting this release．')
    return int(candidates[0])


def open_app(config, bundle, env):
    name = 'karte' if bundle.name == 'Karte.app' else 'ephy-runtime'
    pid = find_app(name, bundle / 'Contents/MacOS' / name)
    existing = pid is not None
    if pid is None:
        pid = json.loads(command(native_tool(config), 'open', bundle, env=env))['pid']
    return {'pid': pid, 'identity': process_identity(pid), 'started': not existing,
            'app': str(bundle)}


def whisper_child(runtime_pid, expected):
    for line in command('ps', '-axo', 'pid=,ppid=,comm=').splitlines():
        fields = line.strip().split(None, 2)
        if len(fields) == 3 and fields[1] == str(runtime_pid) and fields[2] == str(expected):
            pid = int(fields[0])
            return {'pid': pid, 'identity': process_identity(pid), 'owner': runtime_pid}
    return None


def warm_tts(config, record, token, profile):
    stamp = config['home'] / 'tts-ready.json'
    expected = {key: record[key] for key in ('pid', 'identity')}
    expected.update({key: profile[key] for key in ('voice_profile_id', 'model_revision', 'reference_group_digest', 'synthesis_config_digest')})
    if stamp.is_file() and json.loads(stamp.read_text()) == expected:
        return
    print('Irodori：初回の音声生成を確認しています．', flush=True)
    identity = str(uuid.uuid4())
    body = {**profile['default_style'], **{key: profile[key] for key in ('voice_profile_id', 'model_revision', 'reference_group_digest', 'synthesis_config_digest')},
            'request_id': identity, 'operation_id': identity, 'session_id': 'integration-preflight',
            'turn_id': identity, 'generation_revision': 1, 'speech_unit_sequence': 1,
            'speech_text': 'こんにちは．準備ができました．'}
    request = urllib.request.Request('http://127.0.0.1:8767/v1/speech',
        data=json.dumps(body, ensure_ascii=False).encode(),
        headers={'Authorization': 'Bearer ' + token, 'Content-Type': 'application/json'})
    audio = completed = False
    with OPENER.open(request, timeout=68) as response:
        for line in response:
            frame = json.loads(line)
            if frame.get('type') == 'error':
                raise RuntimeError('Irodori speech generation failed during startup．')
            audio = audio or (frame.get('type') == 'audio' and bool(frame.get('wav_base64')))
            completed = completed or frame.get('type') == 'completed'
    if not audio or not completed:
        raise RuntimeError('Irodori did not complete its speech preflight．')
    # Audio is neither played nor saved．Only model and process identity are retained．
    write_json(stamp, expected)


def start(config):
    release, proof = release_files(config)
    records = {}
    # Validate both apps before starting dependencies or opening a second window．
    for name, bundle in (('karte', 'Karte.app'), ('ephy-runtime', 'Ephy Runtime.app')):
        pid = find_app(name, release / bundle / 'Contents/MacOS' / name)
        if pid:
            key = 'runtime' if name == 'ephy-runtime' else name
            records[key] = {'pid': pid, 'identity': process_identity(pid), 'started': False, 'app': str(release / bundle)}
    def remember(name, record):
        records[name] = record
        write_json(config['home'] / 'processes.json', {'state': 'starting', 'release': release.name, 'services': records})
    from packages.config_core.loader import load_app_config
    settings = load_app_config()
    env = {**os.environ, 'PYTHONPATH': str(ROOT), 'KARTE_DATA_DIR': str(config['data_root'])}
    env['EPHY_LOG_DIR'] = str(config['logs'])
    env['KARTE_LOG_DIR'] = str(config['logs'] / 'karte')
    if config.get('preference_data'):
        env['EPHY_PREFERENCE_DATA_ROOT'] = str((config['config_path'].parent / config['preference_data']).resolve())
    for role, port in (('fast', 8081), ('work', 8082), ('code', 8083), ('embedding', 8090)):
        model = settings.models.get(role)
        if model and model.provider == 'llama_cpp':
            require_local(model.base_url, port)
            remember(role, ensure_service(config, role, port, '/health', [ROOT / ('scripts/start_llama_' + role + '.sh')], env=env))
    if settings.vector_db.provider == 'qdrant':
        require_local(settings.vector_db.url, 6333)
        remember('qdrant', ensure_service(config, 'qdrant', 6333, '/healthz',
            [ROOT / 'bin/qdrant', '--config-path', ROOT / 'data/runtime/qdrant/config.yaml'], env=env))
    if settings.web_search.enabled:
        require_local(settings.web_search.base_url, 8888)
        search_env = {**env, 'SEARXNG_SETTINGS_PATH': str(ROOT / 'data/runtime/searxng/settings.yml'),
                      'SEARXNG_BIND_ADDRESS': '127.0.0.1', 'SEARXNG_PORT': '8888'}
        search_env.pop('PYTHONPATH', None)
        remember('searxng', ensure_service(config, 'searxng', 8888, '/healthz',
            [ROOT / 'tools/searxng/.venv/bin/python', '-m', 'searx.webapp'],
            cwd=ROOT / 'tools/searxng/src', env=search_env))
    token = config['tts_token_file'].read_text().strip()
    if not token:
        raise ValueError('The saved TTS credential is empty．')
    speech_env = {**env, 'EPHY_TTS_BEARER_TOKEN': token, 'EPHY_TTS_ENDPOINT': 'http://127.0.0.1:8767'}
    remember('irodori', ensure_service(config, 'irodori', 8767, '/health',
        [config['tts_python'], '-m', 'apps.speech', 'serve', '--config', config['tts_config'], '--port', '8767'], env=speech_env, token=token))
    profiles = fetch('http://127.0.0.1:8767/v1/voice-profiles', token)
    profile = next((p for p in profiles.get('profiles', []) if p.get('voice_profile_id') == config.get('voice_profile_id', 'voice-irodori-anime-exp') and p.get('available')), None)
    if profile is None:
        raise RuntimeError('The configured Irodori voice is unavailable．')
    warm_tts(config, records['irodori'], token, profile)
    remember('karte', open_app(config, release / 'Karte.app', env))
    marker = config['data_root'] / '.mdsys/runtime/karte.pid'
    until = time.monotonic() + 30
    while time.monotonic() < until:
        try:
            capabilities = json.loads((config['data_root'] / '.mdsys/context/v2/capabilities.json').read_text())
            if int(marker.read_text()) == records['karte']['pid'] and capabilities['protocol_version'] == '2.0':
                break
        except (OSError, ValueError, KeyError):
            pass
        time.sleep(.2)
    else:
        raise RuntimeError('Karte receiver readiness timed out．')
    env['EPHY_KARTE_EXECUTABLE'] = str(release / 'Karte.app/Contents/MacOS/karte')
    remember('gateway', ensure_service(config, 'gateway', 8000, '/health', [ROOT / 'scripts/start_gateway.sh'], env=env))
    gateway = fetch('http://127.0.0.1:8000/health')
    if not gateway.get('karte_enabled') or not gateway.get('karte_context_enabled'):
        raise RuntimeError('Gateway Karte integration is disabled．')
    asr = json.loads((release / 'asr.json').read_text())
    for field in ('model', 'vad'):
        if digest(asr[field]) != asr[field + '_sha256']:
            raise ValueError('Configured ASR assets differ from the prepared model．')
    runtime_env = {**speech_env, 'EPHY_RUNTIME_ROOT': str(ROOT),
                   'EPHY_ASR_PROVIDER': 'whisper-cpp', 'EPHY_ASR_CONFIG': str(release / 'asr.json')}
    remember('runtime', open_app(config, release / 'Ephy Runtime.app', runtime_env))
    until = time.monotonic() + 30
    while time.monotonic() < until:
        worker = whisper_child(records['runtime']['pid'], release / 'Ephy Runtime.app/Contents/Helpers/ephy-whisper')
        if worker:
            remember('asr', worker)
            break
        time.sleep(.25)
    else:
        raise RuntimeError('Runtime did not start its configured Whisper worker．')
    subprocess.run([str(native_tool(config)), 'activate', str(records['runtime']['pid'])], check=True)
    report = {'state': 'running', 'timestamp': datetime.now(timezone.utc).isoformat(), 'release': release.name,
              'services': records, 'asr_model_id': asr['model_id'], 'tts_catalog_available': True,
              'karte_native_api': proof['karte_native_api']}
    write_json(config['home'] / 'processes.json', report)
    write_json(config['logs'] / 'startup.json', report)
    print('Ephy，Karte，ASR，TTSと関連サービスを起動しました．', flush=True)
    return report


def stop(config):
    report = json.loads((config['home'] / 'processes.json').read_text())
    services = report['services']
    # A canceled unsaved-changes dialog stops shutdown before any dependency is terminated．
    for name in ('runtime', 'asr', 'karte', 'gateway', 'irodori', 'searxng', 'qdrant', 'embedding', 'code', 'work', 'fast'):
        record = services.get(name)
        if not record or process_identity(record['pid']) is None:
            continue
        if process_identity(record['pid']) != record['identity']:
            raise RuntimeError(name + ' changed process identity．It was not stopped．')
        if 'app' in record:
            subprocess.run([str(native_tool(config)), 'quit', str(record['pid'])], check=True)
        else:
            os.kill(record['pid'], signal.SIGTERM)
        until = time.monotonic() + 20
        while time.monotonic() < until and process_identity(record['pid']) is not None:
            time.sleep(.2)
        if process_identity(record['pid']) is not None:
            raise RuntimeError(name + ' has not closed．Check its window or local log．')
    print('統合環境のアプリとサービスを終了しました．', flush=True)


def status(config):
    path = config['home'] / 'processes.json'
    report = json.loads(path.read_text()) if path.exists() else {'services': {}}
    return {name: {'running': process_identity(record['pid']) == record['identity'], 'pid': record['pid']}
            for name, record in report['services'].items()}


@contextmanager
def exclusive(config):
    if fcntl is None:
        raise RuntimeError('This native integration launcher requires macOS．')
    with (config['home'] / 'lifecycle.lock').open('a') as lock:
        try:
            fcntl.flock(lock.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            raise RuntimeError('Another integration operation is still running．') from None
        yield


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=('build', 'start', 'stop', 'status'))
    parser.add_argument('--config', type=Path, required=True)
    args = parser.parse_args()
    config = load_config(args.config)
    if args.action == 'status':
        print(json.dumps(status(config), ensure_ascii=False, indent=2))
        return
    with exclusive(config):
        {'build': build, 'start': start, 'stop': stop}[args.action](config)


if __name__ == '__main__':
    sys.path.insert(0, str(ROOT))
    try:
        main()
    except Exception as error:
        print(str(error), file=sys.stderr)
        sys.exit(1)

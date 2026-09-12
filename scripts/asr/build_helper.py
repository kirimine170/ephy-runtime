#!/usr/bin/env python3
"""Build the pinned resident Whisper helper from an explicit local checkout．"""
import argparse
import hashlib
import json
from pathlib import Path
import platform
import shutil
import subprocess


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--source', type=Path, required=True)
    parser.add_argument('--jobs', type=int, default=6)
    args = parser.parse_args()
    root = Path(__file__).resolve().parents[2]
    source = args.source.resolve(strict=True)
    if platform.system() != 'Darwin' or platform.machine() != 'arm64':
        parser.error('This Metal package targets Apple Silicon macOS．')
    if source.is_relative_to(root) or not 1 <= args.jobs <= 16:
        parser.error('Use an external source checkout and 1–16 build jobs．')
    manifest = json.loads(Path(__file__).with_name('assets.json').read_text())
    revision = subprocess.check_output(['git', '-C', str(source), 'rev-parse', 'HEAD']).decode().strip()
    dirty = subprocess.check_output(['git', '-C', str(source), 'status', '--porcelain', '--untracked-files=no']).strip()
    if revision != manifest['whisper_cpp']['commit'] or dirty:
        parser.error('Source must match the pinned commit without tracked changes．')
    build = root / 'desktop/build/whisper'
    flags = ['-DCMAKE_BUILD_TYPE=Release', '-DGGML_METAL=ON', '-DGGML_METAL_EMBED_LIBRARY=ON', '-DGGML_OPENMP=OFF', '-DGGML_NATIVE=OFF']
    subprocess.run(['cmake', '-S', str(root / 'desktop/voice/whisper'), '-B', str(build), '-DWHISPER_SOURCE_DIR='+str(source), *flags], check=True)
    subprocess.run(['cmake', '--build', str(build), '--config', 'Release', '-j', str(args.jobs)], check=True)
    subprocess.run(['ctest', '--test-dir', str(build), '--output-on-failure'], check=True)
    destination = root / 'bin/ephy-whisper'
    destination.parent.mkdir(parents=True, exist_ok=True)
    shutil.copyfile(build / 'ephy-whisper', destination); destination.chmod(0o755)
    subprocess.run(['xattr', '-c', str(destination)], check=True)
    subprocess.run(['codesign', '--force', '--sign', '-', '--identifier', 'jp.ephy.runtime.whisper-asr', str(destination)], check=True)
    subprocess.run(['codesign', '--verify', '--strict', str(destination)], check=True)
    notices = destination.parent / 'whisper-notices'
    notices.mkdir(exist_ok=True)
    shutil.copyfile(source / 'LICENSE', notices / 'whisper.cpp-LICENSE.txt')
    shutil.copyfile(Path(__file__).with_name('nlohmann-json-LICENSE.txt'), notices / 'nlohmann-json-LICENSE.txt')
    shutil.copyfile(Path(__file__).with_name('assets.json'), notices / 'assets.json')
    value = {'schema_version':1, 'whisper_cpp':manifest['whisper_cpp'], 'cmake_flags':flags,
             'helper_sha256':hashlib.sha256(destination.read_bytes()).hexdigest(),
             'language':'ja', 'backend':'metal', 'warmup':'one second of zero PCM through encoder and decoder',
             'local_source_files': {str(p.relative_to(root)):hashlib.sha256(p.read_bytes()).hexdigest() for p in sorted((root/'desktop/voice/whisper').glob('*')) if p.is_file()}}
    (destination.parent/'whisper-build-provenance.json').write_text(json.dumps(value,indent=2)+'\n')
    print('Built pinned，signed resident ASR helper．')


if __name__ == '__main__':
    main()

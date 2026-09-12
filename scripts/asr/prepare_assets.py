#!/usr/bin/env python3
"""Explicitly install pinned public ASR weights outside the source repository．"""
from __future__ import annotations

import argparse
import concurrent.futures
import hashlib
import json
from pathlib import Path
import time
import urllib.request


def digest(path: Path) -> str:
    h = hashlib.sha256()
    with path.open('rb') as source:
        while data := source.read(1024 * 1024):
            h.update(data)
    return h.hexdigest()


def install(asset: dict, destination: Path) -> dict:
    final = destination / asset['file']
    if final.exists():
        if final.stat().st_size == asset['bytes'] and digest(final) == asset['sha256']:
            return {'id': asset['id'], 'status': 'verified', 'sha256': asset['sha256']}
        raise ValueError('existing_asset_mismatch')
    partial = final.with_suffix('.download')
    url = f"https://huggingface.co/{asset['repository']}/resolve/{asset['revision']}/{asset['file']}"
    started = time.monotonic()
    with urllib.request.urlopen(url, timeout=90) as response, partial.open('wb') as out:
        size = 0
        while data := response.read(1024 * 1024):
            size += len(data)
            if size > asset['bytes']:
                raise ValueError('asset_size_mismatch')
            out.write(data)
    if partial.stat().st_size != asset['bytes'] or digest(partial) != asset['sha256']:
        raise ValueError('asset_digest_mismatch')
    partial.replace(final)
    return {'id': asset['id'], 'status': 'installed', 'sha256': asset['sha256'],
            'elapsed_ms': round((time.monotonic() - started) * 1000)}


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument('--destination', type=Path, required=True)
    parser.add_argument('--model-id', choices=['all','large-v3-turbo-f16','kotoba-v2.0-f16'], default='large-v3-turbo-f16')
    args = parser.parse_args()
    destination = args.destination.resolve()
    repository = Path(__file__).resolve().parents[2]
    if destination.is_relative_to(repository):
        parser.error('Model destination must be outside the source repository．')
    destination.mkdir(parents=True, exist_ok=True)
    manifest = json.loads(Path(__file__).with_name('assets.json').read_text())
    with concurrent.futures.ThreadPoolExecutor(max_workers=3) as pool:
        jobs = [pool.submit(install, a, destination) for a in manifest['models'] if args.model_id=='all' or a['id'] in (args.model_id,'silero-v6.2.0')]
        for job in concurrent.futures.as_completed(jobs):
            print(json.dumps(job.result()), flush=True)


if __name__ == '__main__':
    main()

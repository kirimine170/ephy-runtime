#!/usr/bin/env python3
"""Write explicit local ASR settings after verifying pinned model assets．"""
import argparse
import hashlib
import json
from pathlib import Path


def digest(path):
    with path.open('rb') as file:
        return hashlib.file_digest(file, 'sha256').hexdigest()


def configuration(helper, model_dir, model_id):
    manifest = json.loads(Path(__file__).with_name('assets.json').read_text())
    assets = {asset['id']: asset for asset in manifest['models']}
    if model_id not in ('large-v3-turbo-f16', 'kotoba-v2.0-f16'):
        raise ValueError('unsupported_model')
    helper = helper.resolve(strict=True)
    if not helper.is_file() or not helper.stat().st_mode & 0o111:
        raise ValueError('invalid_helper')
    result = {'helper': str(helper), 'helper_sha256': digest(helper), 'model_id': model_id,
              'language': 'ja', 'backend': 'metal', 'threads': 4, 'step_ms': 500}
    for field, asset in [('model', assets[model_id]), ('vad', assets['silero-v6.2.0'])]:
        path = (model_dir / asset['file']).resolve(strict=True)
        if path.stat().st_size != asset['bytes'] or digest(path) != asset['sha256']:
            raise ValueError('model_asset_mismatch')
        result[field] = str(path); result[field+'_sha256'] = asset['sha256']
    return result


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--helper', type=Path, required=True)
    parser.add_argument('--model-dir', type=Path, required=True)
    parser.add_argument('--model-id', default='large-v3-turbo-f16')
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    value = configuration(args.helper, args.model_dir, args.model_id)
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(value, indent=2)+'\n')
    print(json.dumps({'model_id': value['model_id'], 'helper_sha256': value['helper_sha256'], 'model_sha256': value['model_sha256']}))


if __name__ == '__main__':
    main()

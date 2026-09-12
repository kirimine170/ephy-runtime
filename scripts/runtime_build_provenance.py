#!/usr/bin/env python3
"""Bind a build to its source and the final signed executable without storing source text．"""
import argparse
import hashlib
import json
from pathlib import Path
import subprocess


def git(root, *args):
    return subprocess.check_output(['git', '-C', str(root), *args])


def source_snapshot(root):
    revision = git(root, 'rev-parse', 'HEAD').decode().strip()
    status = git(root, 'status', '--porcelain=v1', '-z')
    digest = hashlib.sha256(git(root, 'diff', '--binary', 'HEAD'))
    for raw in sorted(git(root, 'ls-files', '--others', '--exclude-standard', '-z').split(b'\0')):
        if raw:
            digest.update(raw)
            digest.update((root / raw.decode()).read_bytes())
    return {'source_revision': revision, 'source_tree': git(root, 'rev-parse', 'HEAD^{tree}').decode().strip(),
            'source_dirty': bool(status), 'source_changes_sha256': digest.hexdigest()}


def finish(root, before, binary, output):
    after = source_snapshot(root)
    if before != after:
        raise ValueError('Source changed during build; rebuild before acceptance')
    result = {**after, 'schema_version': 1, 'executable_sha256': hashlib.sha256(binary.read_bytes()).hexdigest(),
              'executable_name': binary.name, 'go_version': subprocess.check_output(['go', 'version']).decode().strip(),
              'build_tags': 'desktop,wv2runtime.download,production'}
    output.write_text(json.dumps(result, indent=2) + '\n')
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=['snapshot', 'finish'])
    parser.add_argument('snapshot', type=Path)
    parser.add_argument('--binary', type=Path)
    parser.add_argument('--output', type=Path)
    args = parser.parse_args()
    root = Path(__file__).resolve().parents[1]
    if args.action == 'snapshot':
        args.snapshot.write_text(json.dumps(source_snapshot(root)))
    else:
        finish(root, json.loads(args.snapshot.read_text()), args.binary, args.output)
        print(f'Build provenance: {args.output}')


if __name__ == '__main__':
    main()

#!/usr/bin/env python3
"""Generate Wails bindings，then normalize its map-dependent class order．"""
from pathlib import Path
import re
import subprocess


def normalize_models(source: str) -> str:
    source = '\n'.join(line.rstrip() for line in source.splitlines()).rstrip() + '\n'
    # Wails v2 emits class declarations with one tab，inside one namespace．
    matches = list(re.finditer(r'^\texport class (\w+) \{\n', source, re.MULTILINE))
    if not matches:
        return source
    closing = source.rfind('\n}')
    if closing < matches[-1].end():
        raise ValueError('Unexpected Wails namespace ending')
    classes = {
        match.group(1): source[match.start():matches[i+1].start() if i+1 < len(matches) else closing].rstrip()
        for i, match in enumerate(matches)
    }
    if len(classes) != len(matches):
        raise ValueError('Duplicate generated class')
    return source[:matches[0].start()].rstrip() + '\n\n' + '\n'.join(classes[name] for name in sorted(classes)) + source[closing:].rstrip() + '\n'


def main() -> None:
    desktop = Path(__file__).resolve().parents[1] / 'desktop'
    subprocess.run(['wails', 'generate', 'module'], cwd=desktop, check=True)
    root = desktop / 'frontend/wailsjs'
    for path in sorted(root.rglob('*')):
        if path.is_file() and path.suffix in {'.ts', '.js', '.json'}:
            source = path.read_text()
            normalized = normalize_models(source) if path == root / 'go/models.ts' else '\n'.join(line.rstrip() for line in source.splitlines()).rstrip() + '\n'
            path.write_text(normalized)
            if path.parent.name == 'runtime':
                path.chmod(0o644)


if __name__ == '__main__':
    main()

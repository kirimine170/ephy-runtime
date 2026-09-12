#!/usr/bin/env python3
"""Extract a bounded public Japanese read-speech comparison set．No microphone．"""
import argparse
import hashlib
import json
from pathlib import Path
import subprocess
import wave


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--parquet', type=Path, required=True)
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--count', type=int, default=20)
    parser.add_argument('--max-seconds', type=int, default=10)
    args = parser.parse_args()
    import pyarrow.parquet as pq
    if not 1 <= args.count <= 100 or not 2 <= args.max_seconds <= 60:
        parser.error('count must be in 1..100 and max-seconds in 2..60')
    if args.output.resolve().is_relative_to(Path(__file__).resolve().parents[2]):
        parser.error('Evaluation audio must be outside the source repository．')
    table = pq.read_table(args.parquet)
    args.output.mkdir(parents=True, exist_ok=True)
    seen, samples = set(), []
    for row in table.to_pylist():
        if row['id'] in seen or not 32000 <= row['num_samples'] <= args.max_seconds * 16000:
            continue
        seen.add(row['id'])
        name = f"fleurs-ja-{len(samples):02d}"
        result = subprocess.run(['ffmpeg', '-v', 'error', '-i', 'pipe:0', '-f', 's16le', '-ar', '16000', '-ac', '1', 'pipe:1'],
                                input=row['audio']['bytes'], capture_output=True, check=True)
        pcm = result.stdout
        path = args.output / (name + '.wav')
        with wave.open(str(path), 'wb') as output:
            output.setnchannels(1); output.setsampwidth(2); output.setframerate(16000); output.writeframes(pcm)
        samples.append({'id': name, 'dataset_id': row['id'], 'audio': path.name,
                        'reference': row['raw_transcription'], 'duration_ms': len(pcm) // 32,
                        'pcm_sha256': hashlib.sha256(pcm).hexdigest(), 'kind': 'public-read-speech'})
        if len(samples) == args.count:
            break
    manifest = {'schema_version': 1, 'dataset': 'google/fleurs', 'config': 'ja_jp', 'split': 'test',
                'source': 'https://huggingface.co/datasets/google/fleurs', 'license': 'CC-BY-4.0',
                'selection': f'first distinct dataset IDs with 2–{args.max_seconds} seconds of audio in parquet row order',
                'parquet_sha256': hashlib.file_digest(args.parquet.open('rb'), 'sha256').hexdigest(),
                'user_conversation_quality': 'not_measured', 'samples': samples}
    (args.output / 'manifest.json').write_text(json.dumps(manifest, ensure_ascii=False, indent=2) + '\n')
    print(json.dumps({'count': len(samples), 'total_duration_ms': sum(s['duration_ms'] for s in samples),
                      'microphone_opened': False, 'user_conversation_quality': 'not_measured'}))


if __name__ == '__main__':
    main()

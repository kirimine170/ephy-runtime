#!/usr/bin/env python3
"""Explicit synthetic regression fixtures．No microphone or playback．"""
import argparse
import array
import hashlib
import json
import math
from pathlib import Path
import random
import subprocess
import sys
import wave


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    if args.output.resolve().is_relative_to(Path(__file__).resolve().parents[2]):
        parser.error('Keep generated audio outside the source repository．')
    args.output.mkdir(parents=True, exist_ok=True)
    samples = []
    def save(name, pcm, text, kind, rate=16000, **extra):
        path = args.output / (name + '.wav')
        with wave.open(str(path), 'wb') as audio:
            audio.setnchannels(1); audio.setsampwidth(2); audio.setframerate(rate); audio.writeframes(pcm)
        sample = {'id': name, 'audio': path.name, 'reference': text, 'kind': kind,
                  'sample_rate': rate, 'pcm_sha256': hashlib.sha256(pcm).hexdigest(), **extra}
        samples.append(sample)
        return sample
    def synth(name, text):
        aiff = args.output / (name + '.aiff')
        subprocess.run(['say', '-v', 'Kyoko', '-r', '185', '-o', str(aiff), text], check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        result = subprocess.run(['ffmpeg', '-v', 'error', '-i', str(aiff), '-f', 's16le', '-ar', '16000', '-ac', '1', 'pipe:1'], check=True, capture_output=True)
        aiff.unlink()
        if not 0 < len(result.stdout) <= 16000*2*30:
            raise RuntimeError('synthetic_speech_unavailable')
        return result.stdout
    phrases = ['はい', 'いや', '待って', '違う', 'はい，でも違います',
               '明日の午後3時ではなく，午後4時に変更して',
               'Visual Studio CodeでPythonのテストを実行して', 'えっと，来週の予定を確認したいです']
    for index, text in enumerate(phrases):
        pcm = synth(f'short-{index}', text)
        save(f'short-{index}', pcm, text, 'synthetic-kyoko', critical_terms=[text] if index < 4 else [])
        if index == 2:
            raw = array.array('h', pcm)
            if sys.byteorder != 'little': raw.byteswap()
            quiet = array.array('h', (round(value * .12) for value in raw))
            if sys.byteorder != 'little': quiet.byteswap()
            save('quiet-command', quiet.tobytes(), text, 'synthetic-quiet-kyoko')
        if index == 5:
            for rate in [44100, 48000]:
                converted = subprocess.run(['ffmpeg', '-v', 'error', '-f', 's16le', '-ar', '16000', '-ac', '1', '-i', 'pipe:0', '-f', 's16le', '-ar', str(rate), 'pipe:1'], input=pcm, check=True, capture_output=True).stdout
                save(f'sample-rate-{rate}', converted, text, 'synthetic-resampling', rate=rate)
    for index in range(40):
        rng = random.Random(8700 + index)
        length = 16000 * (2 + index % 4)
        group = index // 10
        filtered = 0.
        values = array.array('h')
        for i in range(length):
            if group == 0: value = 0.
            elif group == 1: value = rng.uniform(-1, 1) * (.005 + .005 * (index % 10))
            elif group == 2:
                filtered = .97 * filtered + .03 * rng.uniform(-1, 1)
                value = .04 * math.sin(i * math.tau * (90 + index) / 16000) + filtered * .2
            else: value = rng.uniform(-.7, .7) if i % 8000 < 80 else 0.
            values.append(round(max(-1, min(1, value)) * 32767))
        if sys.byteorder != 'little': values.byteswap()
        save(f'noise-{index:02d}', values.tobytes(), '', ['synthetic-silence','synthetic-white-noise','synthetic-fan','synthetic-clicks'][group])
    first, last = '最初の合言葉は桜です．', '最後の合言葉は紅葉です．'
    head, tail = synth('long-head', first), synth('long-tail', last)
    pcm = head + bytes(60*32000 - len(head) - len(tail)) + tail
    save('long-60-seconds', pcm, first + last, 'synthetic-long-boundary', critical_terms=['最初', '桜', '最後', '紅葉'])
    manifest = {'schema_version': 1, 'source': 'explicit synthetic Kyoko and deterministic signal fixtures',
                'user_conversation_quality': 'not_measured', 'microphone_opened': False, 'audio_played': False,
                'samples': samples}
    (args.output / 'manifest.json').write_text(json.dumps(manifest, ensure_ascii=False, indent=2) + '\n')
    for group, subset in [('short', [s for s in samples if s['id'].startswith('short-') or s['id']=='quiet-command']),
                          ('noise', [s for s in samples if s['id'].startswith('noise-')]),
                          ('boundaries', [s for s in samples if s['id'].startswith(('sample-rate-', 'long-'))])]:
        (args.output / (group + '.json')).write_text(json.dumps({**manifest, 'samples': subset}, ensure_ascii=False, indent=2)+'\n')
    print(json.dumps({'samples': len(samples), 'microphone_opened': False, 'audio_played': False}))


if __name__ == '__main__':
    main()

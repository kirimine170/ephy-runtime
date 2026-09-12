#!/usr/bin/env python3
"""Compare local ASR on explicit fixtures．Persist metrics，never hypotheses．"""
from __future__ import annotations

import argparse
import base64
import hashlib
import json
import math
from pathlib import Path
import queue
import subprocess
import threading
import time
import unicodedata
import wave


def normalize(text: str) -> str:
    return ''.join(c for c in unicodedata.normalize('NFKC', text).lower()
                   if not unicodedata.category(c).startswith(('P', 'Z')) and not c.isspace())


def distance(reference: str, hypothesis: str) -> int:
    previous = list(range(len(hypothesis) + 1))
    for row, a in enumerate(reference, 1):
        current = [row]
        for column, b in enumerate(hypothesis, 1):
            current.append(min(previous[column] + 1, current[-1] + 1, previous[column-1] + (a != b)))
        previous = current
    return previous[-1]


def percentile(values, quantile):
    values = sorted(v for v in values if v is not None)
    return values[max(0, math.ceil(len(values)*quantile)-1)] if values else None


def error_code(error):
    value = str(error)
    codes = {'asr_timeout','asr_worker_exited','asr_not_ready','asr_protocol_error',
             'asset_hash_mismatch','fixture_hash_mismatch','invalid_audio','asr_write_failed'}
    return value if value in codes else 'runner_failed'


class Process:
    def __init__(self, args):
        self.process = subprocess.Popen(args, stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
        self.events = queue.Queue(maxsize=4096)
        self.started = time.monotonic()
        def read():
            try:
                while line := self.process.stdout.readline(96*1024 + 1):
                    if len(line) > 96*1024:
                        break
                    self.events.put((time.monotonic(), json.loads(line)), timeout=2)
            except Exception:
                pass
            self.events.put((time.monotonic(), {'type': 'eof'}), timeout=2)
        threading.Thread(target=read, daemon=True).start()

    def event(self, deadline):
        remaining = deadline - time.monotonic()
        if remaining <= 0:
            raise TimeoutError('asr_timeout')
        at, event = self.events.get(timeout=remaining)
        if event.get('type') in ('fatal', 'eof'):
            raise RuntimeError('asr_worker_exited')
        return at, event

    def send(self, frame):
        self.process.stdin.write((json.dumps(frame, separators=(',', ':')) + '\n').encode())
        self.process.stdin.flush()

    def close(self):
        self.process.terminate()
        try:
            self.process.wait(timeout=3)
        except subprocess.TimeoutExpired:
            self.process.kill(); self.process.wait(timeout=3)


def one_trial(process, sample, manifest_dir, realtime, apple):
    with wave.open(str(manifest_dir / sample['audio']), 'rb') as audio:
        rate = audio.getframerate()
        assert 8000 <= rate <= 48000 and audio.getsampwidth() == 2 and audio.getnchannels() == 1
        pcm = audio.readframes(audio.getnframes())
    if hashlib.sha256(pcm).hexdigest() != sample['pcm_sha256']:
        raise RuntimeError('fixture_hash_mismatch')
    if not 0 < len(pcm) <= rate*2*60:
        raise RuntimeError('invalid_audio')
    bytes_per_second = rate * 2
    identity = dict.fromkeys(['operation_id', 'session_id', 'turn_id', 'segment_id'], sample['id'])
    frame = {'type': 'start', 'protocol': 1, 'sample_rate': rate, 'partial': realtime, **identity}
    begin = time.monotonic()
    deadline = begin + len(pcm)/bytes_per_second + 45
    process.send(frame)
    if not apple:
        _, ack = process.event(deadline)
        if ack.get('type') != 'started':
            raise RuntimeError('asr_protocol_error')
    timing = {'end_sent': None, 'writer_error': False}
    def write():
        try:
            size = rate * 2 // 25 if realtime else 64000
            for sequence, offset in enumerate(range(0, len(pcm), size), 1):
                if realtime:
                    wait = begin + offset/bytes_per_second - time.monotonic()
                    if wait > 0:
                        time.sleep(wait)
                process.send({'type': 'audio', 'protocol': 1, **identity, 'sequence': sequence,
                              'pcm_base64': base64.b64encode(pcm[offset:offset+size]).decode()})
            if realtime:
                wait = begin + len(pcm)/bytes_per_second - time.monotonic()
                if wait > 0:
                    time.sleep(wait)
            timing['end_sent'] = time.monotonic()
            process.send({'type': 'finish', 'protocol': 1, **identity})
        except Exception:
            timing['writer_error'] = True
    sender = threading.Thread(target=write, daemon=True); sender.start()
    text, phase, error = '', '', ''
    partial_at, final_at, last_speech, speech_start = None, None, None, None
    revisions = 0
    while True:
        at, event = process.event(deadline)
        if event.get('operation_id') != identity['operation_id']:
            raise RuntimeError('asr_protocol_error')
        if event.get('type') == 'done':
            break
        revisions += 1
        activity = event.get('activity')
        if activity and activity.get('has_speech'):
            last_speech = activity['last_speech_ms']
            if speech_start is None:
                speech_start = max(0, last_speech-activity['speech_ms'])
        if event.get('phase') == 'partial' and event.get('transcript') and partial_at is None:
            partial_at = at
        if event.get('phase') in ('final', 'no_speech', 'failure', 'timeout', 'canceled'):
            final_at = at
            text, phase, error = event.get('transcript', ''), event['phase'], event.get('error_code', '')
            if apple:
                break
    sender.join(timeout=3)
    if sender.is_alive() or timing['writer_error']:
        raise RuntimeError('asr_write_failed')
    normalized_reference, normalized_hypothesis = normalize(sample['reference']), normalize(text)
    success = phase == ('final' if normalized_reference else 'no_speech')
    critical_terms = [normalize(term) for term in sample.get('critical_terms', [])]
    return {'id': sample['id'], 'kind': sample.get('kind', 'explicit-fixture'), 'phase': phase, 'error_code': error,
            'success': success, 'duration_ms': len(pcm)*1000//bytes_per_second, 'input_sample_rate':rate, 'revisions': revisions,
            'critical_terms_total': len(critical_terms), 'critical_terms_matched': sum(term in normalized_hypothesis for term in critical_terms),
            'critical_term_matches': [term in normalized_hypothesis for term in critical_terms],
            'reference_characters': len(normalized_reference),
            'edit_distance': distance(normalized_reference, normalized_hypothesis),
            'raw_edit_distance': distance(sample['reference'], text), 'raw_reference_characters': len(sample['reference']),
            'hypothesis_characters': len(normalized_hypothesis),
            'first_partial_ms': round((partial_at-begin)*1000) if partial_at else None,
            'first_partial_from_vad_speech_ms': round((partial_at-begin)*1000-speech_start) if partial_at and speech_start is not None else None,
            'finalization_ms': round((final_at-timing['end_sent'])*1000) if final_at and timing['end_sent'] else None,
            'total_ms': round((time.monotonic()-begin)*1000),
            'vad_last_speech_ms': last_speech,
            'end_to_final_from_vad_ms': round((final_at-begin)*1000-last_speech) if realtime and final_at and last_speech is not None else None}


def failed_trial(sample, error):
    # Failed attempts contribute the full deletion distance，never disappear
    # from the CER or success denominator．Unattempted rows remain separate．
    reference = normalize(sample['reference'])
    return {'id': sample['id'], 'kind':sample.get('kind', 'explicit-fixture'), 'phase':'failure',
            'error_code': error_code(error),
            'success': False, 'reference_characters':len(reference), 'edit_distance':len(reference),
            'critical_terms_total':len(sample.get('critical_terms', [])), 'critical_terms_matched':0,
            'raw_reference_characters':len(sample['reference']), 'raw_edit_distance':len(sample['reference']),
            'hypothesis_characters':0, 'first_partial_ms':None, 'finalization_ms':None}


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--manifest', type=Path, required=True)
    parser.add_argument('--config', type=Path)
    parser.add_argument('--apple-helper', type=Path)
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--limit', type=int, default=20)
    parser.add_argument('--realtime', action='store_true')
    args = parser.parse_args()
    if bool(args.config) == bool(args.apple_helper):
        parser.error('select one provider')
    manifest = json.loads(args.manifest.read_text())
    apple = bool(args.apple_helper)
    results = []
    summary = {'schema_version': 1, 'provider': 'macos-speech' if apple else 'whisper-cpp',
               'source': manifest.get('source', 'explicit-fixtures'), 'realtime': args.realtime,
               'user_conversation_quality': 'not_measured', 'microphone_opened': False, 'audio_played': False,
               'normalization': 'NFKC，lowercase，remove punctuation and whitespace，preserve numerals and words', 'results': results}
    process = None
    try:
        if apple:
            check = subprocess.run([str(args.apple_helper), '--check'], capture_output=True, timeout=10)
            if check.returncode or check.stdout.strip() != b'ready':
                summary['availability'] = 'unavailable_or_permission_required'
                return
        else:
            config = json.loads(args.config.read_text())
            for asset in ['helper', 'model', 'vad']:
                with Path(config[asset]).open('rb') as file:
                    digest = hashlib.file_digest(file, 'sha256').hexdigest()
                if digest != config[asset+'_sha256']:
                    raise RuntimeError('asset_hash_mismatch')
            summary['model_id'] = config['model_id']; summary['model_sha256'] = config['model_sha256']
            summary['helper_sha256'] = config['helper_sha256']; summary['vad_sha256'] = config['vad_sha256']
            summary['threads'] = config.get('threads', 4); summary['step_ms'] = config.get('step_ms', 500)
            command = [config['helper'], '--model', config['model'], '--vad', config['vad'], '--model-revision',
                       config['model_id'] + ':sha256:' + config['model_sha256'][:16], '--threads', str(config.get('threads', 4)),
                       '--step-ms', str(config.get('step_ms', 500))]
            process = Process(command)
            at, ready = process.event(time.monotonic()+60)
            if ready.get('type') != 'ready':
                raise RuntimeError('asr_not_ready')
            summary['load_and_warmup_ms'] = round((at-process.started)*1000)
        for sample in manifest['samples'][:args.limit]:
            if apple:
                process = Process([str(args.apple_helper), '--stream', '--locale', 'ja-JP'])
            try:
                result = one_trial(process, sample, args.manifest.parent, args.realtime, apple)
            except Exception as error:
                results.append(failed_trial(sample, error))
                raise
            results.append(result)
            print(json.dumps({k: result[k] for k in ['id', 'phase', 'edit_distance', 'reference_characters', 'finalization_ms']}), flush=True)
            if apple:
                process.close(); process = None
    except Exception as error:
        summary['runner_error'] = error_code(error)
    finally:
        if process:
            process.close()
        characters = sum(r['reference_characters'] for r in results)
        raw_characters = sum(r['raw_reference_characters'] for r in results)
        summary.update({'trials': len(results), 'successful': sum(r['success'] for r in results),
                        'unattempted': max(0, len(manifest['samples'][:args.limit])-len(results)),
                        'final_count':sum(r['phase']=='final' for r in results), 'no_speech_count':sum(r['phase']=='no_speech' for r in results),
                        'cer': sum(r['edit_distance'] for r in results)/characters if characters else None,
                        'raw_cer':sum(r['raw_edit_distance'] for r in results)/raw_characters if raw_characters else None,
                        'critical_terms_total':sum(r.get('critical_terms_total',0) for r in results),
                        'critical_terms_matched':sum(r.get('critical_terms_matched',0) for r in results),
                        'first_partial_from_vad_p95_ms':percentile([r.get('first_partial_from_vad_speech_ms') for r in results],.95),
                        'first_partial_p50_ms': percentile([r['first_partial_ms'] for r in results], .5),
                        'first_partial_p95_ms': percentile([r['first_partial_ms'] for r in results], .95),
                        'finalization_p50_ms': percentile([r['finalization_ms'] for r in results], .5),
                        'finalization_p95_ms': percentile([r['finalization_ms'] for r in results], .95),
                        'partial_missing_trials': sum(r['first_partial_ms'] is None for r in results)})
        args.output.parent.mkdir(parents=True, exist_ok=True)
        args.output.write_text(json.dumps(summary, ensure_ascii=False, indent=2)+'\n')
    if summary.get('runner_error'):
        raise SystemExit(1)


if __name__ == '__main__':
    main()

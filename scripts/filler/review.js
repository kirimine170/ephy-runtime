import {createFillerController, createFillerPlayer, createFillerBackchannel} from './fillerController.js';
import {startFillerBargeIn} from './fillerBargeIn.js';

const $ = id => document.getElementById(id);
let trial = null, version = 0, rows = [];
const setStatus = s => { $('status').textContent = s; };
function stop(reason = 'cancel') {
  version++;
  if (trial) {
    const t = trial; trial = null;
    clearTimeout(t.timer); t.controller?.cancel(reason); t.monitor?.stop(); t.backchannel?.stop();
    try { t.answer?.stop(); } catch { /* Already stopped． */ }
    void t.context.close();
  }
  $('start').disabled = false; $('stop').disabled = true; $('interrupt').disabled = true;
  setStatus(reason === 'barge_in' ? '発話を検出して停止しました．' : '停止しました．');
}
$('stop').onclick = () => stop();
function interrupt() {
  const t = trial;
  if (!t || t.interrupted) return;
  const detected = performance.now(); t.interrupted = true;
  clearTimeout(t.timer); t.controller?.cancel('barge_in'); t.monitor?.stop();
  if (t.answer) { t.answer.onended = null; try { t.answer.stop(); } catch {} }
  $('interrupt').disabled = true;
  if (!t.backchannel) { stop('barge_in'); return; }
  setStatus('少し間を置いて，短い応答で話を譲ります．');
  t.backchannel.play(detected);
}
$('interrupt').onclick = interrupt;
window.addEventListener('pagehide', () => stop('invalidated'));
navigator.mediaDevices?.addEventListener('devicechange', () => { if (trial) stop('invalidated'); });
$('start').onclick = async () => {
  stop();
  // A separate candidate preview must never overlap the trial．
  for (const audio of document.querySelectorAll('audio')) { audio.pause(); audio.currentTime = 0; }
  if ($('microphone').checked && !$('headphones').checked) { setStatus('マイク割込みの試験ではヘッドホンを使用してください．'); return; }
  const epoch = ++version, context = new AudioContext();
  const t = {context}; trial = t;
  $('start').disabled = true; $('stop').disabled = false; $('trace').textContent = '';
  const log = event => { $('trace').textContent += JSON.stringify(event) + '\n'; };
  const current = () => trial === t && version === epoch;
  try {
    await context.resume();
    const row = rows.find(r => r.candidate === $('candidate').value);
    const [filler, answer] = await Promise.all([row.file, 'review_answer.wav'].map(async path => context.decodeAudioData(await (await fetch(path)).arrayBuffer())));
    if (!current()) return;
    const acknowledgement = rows.find(r => r.candidate === $('backchannel').value && r.kind === 'backchannel');
    if (acknowledgement) {
      const buffer = await context.decodeAudioData(await (await fetch(acknowledgement.file)).arrayBuffer());
      if (!current()) return;
      t.backchannel = createFillerBackchannel({context, buffer, isCurrent: current, onTrace: log,
        onEnd: () => { if (current() && t.interrupted) { stop(); setStatus('話を譲りました．元の応答は再開しません．'); } }});
    }
    if ($('microphone').checked) {
      t.monitor = await startFillerBargeIn({context, mediaDevices: navigator.mediaDevices, isCurrent: current,
        onSpeech: interrupt, onUnavailable: () => stop('invalidated')});
      if (!current()) { t.monitor?.stop(); return; }
      if (!t.monitor) { stop(); setStatus('マイクを利用できません．試験を停止しました．'); return; }
    }
    const player = createFillerPlayer(context, filler);
    const d = filler.duration * 1000;
    const samples = Array.from({length: 100}, () => ({llm_request_ms: 20, llm_first_ms: 900, tts_request_ms: 1100,
      tts_chunk_ms: 4970, answer_ready_ms: 5000, llm_ttft_ms: 880, tts_latency_ms: 3870}));
    t.controller = createFillerController({samples, durationMS: d, ledger: {used: false}, isCurrent: current,
      play: end => { setStatus('フィラーを再生中です．'); player.play(() => { end(); setStatus('本文を待っています．'); }); },
      stop: player.stop, onTrace: log, onUnsafeStop: () => stop('invalidated')});
    const origin = performance.now();
    t.controller.progress({llm_request_ms: 20, llm_first_ms: 900, tts_request_ms: 1100});
    t.controller.arm(origin); setStatus('応答を待っています．');
    $('interrupt').disabled = false;
    const delay = {normal: 5000, fast: 500, preempt: 5000 - d - 150 + d / 2, gap: 7000}[$('scenario').value];
    t.timer = setTimeout(async () => {
      if (!current() || t.interrupted) return;
      const boundary = t.controller.answerReady();
      if (boundary) { setStatus('フィラーの終わりを待って，本文へ接続します．'); await boundary; }
      if (!current() || t.interrupted) return;
      t.answer = context.createBufferSource(); t.answer.buffer = answer; t.answer.connect(context.destination);
      t.answer.onended = () => { if (current()) { stop(); setStatus('試験が完了しました．'); } };
      t.answer.start(); t.controller.answerStarted(); setStatus('本文を再生中です．');
    }, delay);
  } catch { if (current()) { stop(); setStatus('音声を準備できませんでした．'); } }
};

try {
  rows = await (await fetch('manifest')).json();
  for (const row of rows) {
    const box = document.createElement('div'); box.className = 'row';
    const name = document.createElement('strong'); name.textContent = `${row.kind === 'backchannel' ? '割込みへの返し' : 'フィラー'}：${row.phrase}`;
    const audio = document.createElement('audio'); audio.controls = true; audio.src = row.file; audio.preload = 'none';
    audio.addEventListener('play', () => {
      if (trial) stop();
      for (const other of document.querySelectorAll('audio')) if (other !== audio) other.pause();
    });
    const vote = document.createElement('button'); vote.textContent = row.approved ? '承認を取り消す' : 'この候補を承認';
    vote.disabled = !row.approved;
    audio.addEventListener('ended', () => { vote.disabled = false; });
    vote.onclick = async () => {
      vote.disabled = true;
      try {
        const res = await fetch('review', {method: 'POST', headers: {'Content-Type': 'application/json'},
          body: JSON.stringify({candidate: row.candidate, approved: !row.approved})});
        if (!res.ok) throw new Error();
        row.approved = !row.approved; vote.textContent = row.approved ? '承認を取り消す' : 'この候補を承認';
        $('votes').textContent = '候補音声の評価を保存しました．live設定は変更していません．';
      } catch { $('votes').textContent = '評価を保存できませんでした．ページを読み直してください．'; }
      vote.disabled = false;
    };
    box.append(name, audio, vote); $('candidates').append(box);
    const option = document.createElement('option'); option.value = row.candidate; option.textContent = `${row.phrase} · ${(row.duration_ms / 1000).toFixed(2)}秒`;
    const selector = row.kind === 'backchannel' ? $('backchannel') : $('candidate');
    selector.append(option);
    if (row.kind === 'backchannel' && selector.options.length === 2) selector.value = row.candidate;
    if (row.candidate === 'backchannel_gomen_iiyo') selector.value = row.candidate;
  }
  setStatus('候補音声と試験条件を選べます．');
} catch { setStatus('候補音声を読み込めませんでした．'); $('start').disabled = true; }

const axisLabels = Object.freeze({
  cuteness: '可愛さ',
  ephy_identity: 'Ephyらしさ',
  reference_match: '参照声との一致',
  naturalness: '自然さ',
  emotion_fit: '感情の適切さ',
  japanese_pronunciation: '日本語発音',
  low_fatigue: '長時間聞いた際の疲れにくさ',
});
const comparisonOptions = [
  ['strong_a', 'Aがかなり良い'], ['slight_a', 'Aが少し良い'], ['tie', '同程度'],
  ['slight_b', 'Bが少し良い'], ['strong_b', 'Bがかなり良い'],
];
const overallOptions = [['A', 'A'], ['B', 'B'], ['tie', '同程度'], ['neither', 'どちらも選ばない']];

const base = new URL('.', window.location.href);
const manifest = await fetch(new URL('manifest.json', base), {cache: 'no-store'}).then(response => {
  if (!response.ok) throw new Error('manifest_unavailable');
  return response.json();
});
const state = await fetch(new URL('api/state', base), {cache: 'no-store'}).then(response => {
  if (!response.ok) throw new Error('state_unavailable');
  return response.json();
});
const votes = new Map(state.votes.map(vote => [vote.case_id, vote]));
const firstPending = manifest.cases.findIndex(item => !votes.has(item.case_id));
let current = firstPending < 0 ? manifest.cases.length : firstPending;

const node = id => document.getElementById(id);
const review = node('review');
const complete = node('complete');
const status = node('status');
const axesRoot = node('axes');
const audios = [node('reference-audio'), node('audio-a'), node('audio-b')];
node('reference-audio').src = new URL(manifest.reference_audio, base);

function pauseOthers(active) {
  for (const audio of audios) if (audio !== active) audio.pause();
}
for (const audio of audios) audio.addEventListener('play', () => pauseOthers(audio));

function optionMarkup(name, options, selected = '') {
  return options.map(([value, label]) => `<label><input type="radio" name="${name}" value="${value}" ${selected === value ? 'checked' : ''}><span>${label}</span></label>`).join('');
}

function updateProgress() {
  const count = votes.size;
  node('progress-bar').style.width = `${100 * count / manifest.case_count}%`;
  node('progress-label').textContent = `${count}／${manifest.case_count}件を保存済み`;
}

function showComplete() {
  pauseOthers(null);
  review.hidden = true;
  complete.hidden = false;
  updateProgress();
}

function render() {
  if (votes.size === manifest.case_count && current >= manifest.case_count) return showComplete();
  complete.hidden = true;
  review.hidden = false;
  const item = manifest.cases[current];
  const vote = votes.get(item.case_id);
  node('case-number').textContent = `${current + 1}／${manifest.case_count}`;
  node('case-category').textContent = item.category;
  node('case-text').textContent = item.text;
  node('audio-a').src = new URL(item.audio_a, base);
  node('audio-b').src = new URL(item.audio_b, base);
  axesRoot.innerHTML = manifest.axes.map(axis => `<fieldset class="axis"><legend>${axisLabels[axis]}</legend><div class="choice-row" data-choice="${axis}">${optionMarkup(`axis-${axis}`, comparisonOptions, vote?.axes?.[axis])}</div></fieldset>`).join('');
  const overall = document.querySelector('[data-choice="overall"]');
  overall.classList.add('overall-options');
  overall.innerHTML = optionMarkup('overall', overallOptions, vote?.overall);
  node('note').value = vote?.note || '';
  node('previous').disabled = current === 0;
  node('save').textContent = current === manifest.case_count - 1 ? '回答を保存して完了' : '保存して次へ';
  status.textContent = '';
  updateProgress();
  window.scrollTo({top: review.offsetTop - 12, behavior: 'smooth'});
}

function selected(name) {
  return document.querySelector(`input[name="${name}"]:checked`)?.value || '';
}

async function save() {
  const item = manifest.cases[current];
  const axes = Object.fromEntries(manifest.axes.map(axis => [axis, selected(`axis-${axis}`)]));
  if (Object.values(axes).some(value => !value) || !selected('overall')) {
    status.textContent = '7つの評価軸と総合選好をすべて選んでください．';
    return;
  }
  node('save').disabled = true;
  const vote = {case_id: item.case_id, axes, overall: selected('overall'), note: node('note').value};
  try {
    const response = await fetch(new URL('api/vote', base), {
      method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify(vote),
    });
    if (!response.ok) throw new Error('save_failed');
    votes.set(item.case_id, vote);
    current += 1;
    render();
  } catch {
    status.textContent = '保存できませんでした．再試行してください．';
  } finally {
    node('save').disabled = false;
  }
}

node('save').addEventListener('click', save);
node('previous').addEventListener('click', () => { if (current > 0) { current -= 1; render(); } });
node('return').addEventListener('click', () => { current = 0; render(); });
render();

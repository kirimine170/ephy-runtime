const labels = {
  recording_off: '記録OFF', ready: '次の発言から記録',
  locally_preserved: 'ローカル保全済み', delivery_pending: '配送待ち',
  canonical_saved: 'Karte保存済み', save_failed: '保存失敗',
  permission_blocked: '現在の権限で利用できません', conflict: 'Karteとの競合',
};
export function recordingSummary(status = {}) {
  const enabled = Boolean(status.settings?.enabled);
  return `${enabled ? '記録ON' : '記録OFF'} · ローカル保全 ${status.local || 0} · 配送待ち ${status.pending || 0} · Karte保存済み ${status.saved || 0} · 失敗 ${status.failed || 0}${status.code ? ` · 要確認：${status.code}` : ''}`;
}
export function recordingMarkup() {
  return `<details id="conversation-recording" class="conversation-recording">
    <summary id="recording-summary">会話の記録を確認中</summary>
    <div class="recording-panel">
      <p>確定した発言と回答をKarteへ自動保存します．本文の正本はKarteです．</p>
      <form id="recording-form">
        <label class="field"><span>Karteのデータ保存先</span><input id="recording-root" class="text-input" required></label>
        <label class="field"><span>保存先プロジェクト</span><input id="recording-project" class="text-input" required pattern="[a-z0-9][a-z0-9._-]{0,63}"></label>
        <label class="field"><span>タイムゾーン</span><input id="recording-timezone" class="text-input" required></label>
        <p class="helper-text">分類はinternalです．OFF以前の配送待ちは，現在の許可が有効なら配送を続けます．OFF中の発言を後から追加することはありません．</p>
        <div class="actions"><button id="recording-on" class="primary-btn" type="submit">設定して記録ON</button><button id="recording-off" class="ghost-btn" type="button">記録OFF</button><button id="recording-retry" class="ghost-btn" type="button">配送を再試行</button></div>
      </form>
      <p id="recording-state" role="status" aria-live="polite"></p>
      <p id="recording-destination" class="helper-text"></p>
      <p class="helper-text">件数は発言・回答eventの数です．ローカル保全には配送待ちと失敗を含みます．Karte保存済みはreceiptと本文のID・hashを照合した累計です．読戻しは現在の権限を毎回確認します．</p>
      <div id="recording-records"></div>
      <pre id="recording-readback" class="output-block" hidden></pre>
    </div>
  </details>`;
}
export function mountRecording({bridge, onConversation}) {
  const byId = (id) => document.getElementById(id);
  const panel = byId('conversation-recording');
  let initial = true;
  let status;
  let reading = false;
  let target;
  let readEpoch = 0;
  let refreshRunning = false;
  const clearRead = () => { readEpoch++; byId('recording-readback').textContent = ''; byId('recording-readback').hidden = true; };
  const report = (error) => { byId('recording-state').textContent = String(error); };
  const readCurrent = async () => {
    if (!target || reading || !panel.open) return;
    clearRead();
    const epoch = readEpoch;
    reading = true;
    try {
      const result = await bridge.ReadRecordedConversation(target);
      if (epoch !== readEpoch || !panel.open) return;
      byId('recording-readback').textContent = result.markdown;
      byId('recording-readback').hidden = false;
    } catch (error) { if (epoch === readEpoch) { target = null; report(`読戻しできません．${error}`); } }
    finally { reading = false; }
  };
  const render = (next) => {
    status = next;
    const settings = next.settings || {};
    byId('recording-summary').textContent = recordingSummary(next);
    if (initial) {
      byId('recording-root').value = settings.data_root || '';
      byId('recording-project').value = settings.project || 'ephy-conversations';
      byId('recording-timezone').value = settings.timezone || 'Asia/Tokyo';
      if (settings.conversation_id) onConversation(settings.conversation_id);
      initial = false;
    }
    for (const id of ['recording-root', 'recording-project', 'recording-timezone']) byId(id).readOnly = Boolean(settings.configured);
    byId('recording-on').disabled = Boolean(settings.enabled);
    byId('recording-on').textContent = settings.configured ? '記録ON' : '設定して記録ON';
    byId('recording-off').disabled = !settings.enabled;
    byId('recording-state').textContent = `${labels[next.state] || next.state}${next.code ? ` · ${next.code}` : ''}${next.capacity_warning ? ' · queueの残容量が少なくなっています' : ''}`;
    byId('recording-destination').textContent = settings.configured ? `保存先：${settings.data_root}/content/projects/${settings.project}/note/YYYY-MM/` : '';
    const list = byId('recording-records');
    list.replaceChildren();
    const records = [...(next.records || [])].sort((a, b) => Number(b.conversation_id === settings.conversation_id) - Number(a.conversation_id === settings.conversation_id));
    for (const record of records.slice(0, 32)) {
      const row = document.createElement('div');
      row.className = 'recording-record';
      const description = document.createElement('span');
      description.textContent = `会話 ${record.conversation_id.slice(0, 8)} · 保存 ${record.saved} · 待ち ${record.pending} · 失敗 ${record.failed}${record.code ? ` · ${record.code}` : ''}`;
      row.append(description);
      if (record.target?.doc_id) {
        const button = document.createElement('button');
        button.type = 'button'; button.className = 'ghost-btn'; button.textContent = '最新区間をKarteから読戻す';
        button.addEventListener('click', () => { target = record.target; void readCurrent(); });
        row.append(button);
      }
      list.append(row);
    }
  };
  const refresh = async () => {
    if (refreshRunning) return;
    refreshRunning = true;
    try { render(await bridge.GetRecordingStatus()); await readCurrent(); }
    catch (error) { clearRead(); report(error); }
    finally { refreshRunning = false; }
  };
  const configure = async (enabled) => {
    try {
      render(await bridge.ConfigureRecording({enabled, data_root: byId('recording-root').value.trim(), project: byId('recording-project').value.trim(), timezone: byId('recording-timezone').value.trim()}));
    } catch (error) { report(error); }
  };
  byId('recording-form').addEventListener('submit', (event) => { event.preventDefault(); void configure(true); });
  byId('recording-off').addEventListener('click', () => void configure(false));
  byId('recording-retry').addEventListener('click', async () => { try { render(await bridge.RetryRecording()); } catch (error) { report(error); } });
  panel.addEventListener('toggle', () => { if (!panel.open) { target = null; clearRead(); } });
  document.addEventListener('visibilitychange', () => { if (document.hidden) { target = null; clearRead(); } });
  return {refresh, ownsAutomaticRecording: () => Boolean(status?.settings?.configured || status?.settings?.recording_epoch > 1), newConversation: async (id) => { target = null; clearRead(); try { await bridge.SetRecordingConversation(id); await refresh(); } catch (error) { report(error); } }};
}

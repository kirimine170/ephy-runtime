const LABELS = {paused:'休止中', reactive:'待受中', observe:'観測中', companion:'会話へ参加'};
const FIELDS={response_length:'返答の長さ',call_name_frequency:'呼びかけの頻度',proactive:'自発発話',restricted_memory_ids:'共有を控える記憶'};
const VALUES={default:'既定',brief:'短め',detailed:'詳しく',never:'呼ばない',low:'控えめ',moderate:'ほどほど',high:'多め',allowed:'許可',suppressed:'控える'};
const valueLabel=value=>value==null?'既定':Array.isArray(value)?`${value.length}件`:VALUES[value]||String(value);
const CHANGE = {expired:'有効期限または会話が終了しました',no_change:'既に同じ設定です',unsaved:'この会話だけに反映しました（保存なし）',applied:'次の会話から反映しました', failed:'設定を反映できませんでした', reverted:'指定した変更を戻しました', pending:'反映を確認中です', superseded:'新しい設定に置き換わりました'};
export function residentMarkup() {
 return `<section id="resident-controls" class="resident-controls" aria-label="常駐会話" hidden>
  <div class="actions"><strong id="resident-mode-status" role="status" aria-live="polite">休止中</strong>
  <label>会話モード <select id="resident-mode"><option value="reactive">待受</option><option value="observe">観測</option><option value="companion">会話へ参加</option></select></label>
  <button id="resident-start" class="ghost-btn" type="button">待受を開始／再開</button>
  <button id="resident-pause" class="ghost-btn" type="button">マイクを休止</button>
  <button id="resident-stop" class="ghost-btn" type="button">今の発話を停止</button>
  <button id="resident-exit" class="ghost-btn" type="button">アプリを終了</button></div>
  <p class="helper-text">最小化しても待受を続けます．ウィンドウを閉じると終了します．観測・参加中は「Ephy，…」で直接話しかけてください．</p>
  <div class="actions"><label><input id="resident-owner" type="checkbox">この会話の発言者は所有者です</label>
  <label><input id="resident-consent" type="checkbox">指摘と設定変更の保存を許可</label>
  <label><input id="resident-participants" type="checkbox">観測する参加者全員の許可があります</label></div>
  <p class="helper-text">声から本人認証しません．所有者が不明な会話では長期設定を更新しません．学習利用は無効です．</p>
  <p id="resident-policy" class="helper-text"></p>
  <label class="field"><span>指摘の対象</span><select id="resident-target"><option value="latest">直近の発言</option></select></label>
  <div class="actions"><label class="field"><span>参加者ID（観測・参加時，カンマ区切り）</span><input id="resident-participant-ids" maxlength="500"></label><label class="field"><span>発言者ID</span><input id="resident-speaker-id" maxlength="128"></label></div>
  <div class="actions"><button id="resident-good" class="ghost-btn" type="button">よかった</button><button id="resident-bad" class="ghost-btn" type="button">嫌だった</button>
  <label class="field"><span>今の発言への指摘（任意）</span><input id="resident-feedback-text" maxlength="1000" placeholder="もっと短くして"></label>
  <button id="resident-feedback-send" class="ghost-btn" type="button">指摘を送る</button><button id="resident-suppress" class="ghost-btn" type="button">この会話の自発発話を控える</button></div>
  <p id="resident-feedback-status" role="status" aria-live="polite"></p>
  <p id="resident-candidate-status" role="status" aria-live="polite"></p>
  <details><summary>指摘と設定変更の履歴</summary><button id="resident-refresh" class="ghost-btn" type="button">履歴を更新</button><div id="resident-history"></div></details>
 </section>`;
}
export function feedbackTarget(snapshot, sessionID) {
 if (!snapshot?.operation_id || snapshot.session_id !== sessionID) return undefined;
 const units = snapshot.speech_units || [];
 return {turn_id:snapshot.turn_id,run_id:snapshot.operation_id,generation_id:`${snapshot.operation_id}:${snapshot.generation_revision || 1}`,
  speech_unit_ids:units.map(unit => unit.unit_id),memory_ids:[...(snapshot.memory_ids||[])],
  played_text:units.reduce((acc,unit)=>acc.open && unit.state==='completed'&&unit.synthesis_complete?{open:true,text:acc.text+(unit.text||'')}:{...acc,open:false},{open:true,text:''}).text.slice(0,2000),
  delivery_status:units.length && snapshot.generation?.complete && units.every(unit => unit.state === 'completed' && unit.synthesis_complete) ? 'played'
   : units.some(unit => unit.playback_started || unit.state === 'interrupted') ? 'partial' : units.length?'unplayed':'unknown'};
}
export function residentResultText(result) {
 if (result?.status === 'failed') return '指摘を保存できませんでした．停止操作は引き続き使えます．';
 if (result?.feedback?.correction_status === 'karte_review_required') return '事実の訂正を記録しました．Karteの元の記録を確認して訂正できます．';
 if (result?.feedback?.status === 'retracted') return '指摘を取り消しました．関連する現在の設定変更も確認できます．';
 if (result?.feedback?.clarification_reason === 'owner_not_confirmed') return '所有者を確認できないため，長期設定は変更していません．';
 if (result?.feedback?.status === 'needs_clarification') return '対象を特定できません．対象の発言を選んでから指摘できます．';
 const change = result?.change;
 if(change?.status==='unsaved' && change.reason!=='session_only_without_storage') return '保存の同意がないため，指摘と継続設定を保存していません．';
 if (change) return `${CHANGE[change.status] || '設定変更を確認しました'}．範囲：${change.scope === 'owner' || change.scope === 'persistent' ? '今後の会話' : 'この会話'}．`;
 if (result?.feedback?.status === 'recorded') return '指摘を記録しました．';
 if (result?.recognized === false) return '設定変更として扱える指摘を確認できませんでした．';
 return '指摘の状態を更新しました．';
}
// Local stop is synchronous up to the controller's first await．Snapshot capture
// precedes it，and persistence owns a separate promise from the canceled run．
export function submitResidentFeedback({bridge,controller,sessionID,revision,owner,text='',kind,mode='reactive',dedupeID=globalThis.crypto.randomUUID(),target,sourceSnapshot}) {
 const snapshot=sourceSnapshot||controller.snapshotFeedbackTarget();
 const captured = target || feedbackTarget(snapshot,sessionID);
 const payload={session_id:sessionID,dedupe_id:dedupeID,expected_revision:revision,text,input_source:'ui',speaker:owner?'owner':'unknown',mode:mode==='reactive'?'assistant':mode,target:captured,model_id:snapshot?.model_id||null,voice_id:snapshot?.voice_id||null,prompt_id:snapshot?.prompt_id||null,configuration_id:snapshot?.configuration_id||null};
 if (kind) payload.kind=kind;
 if (/^(止めて|今は話しかけないで|しばらく話しかけないで)[．。!！\s]*$/.test(text)) void controller.stopPlayback();
 return bridge.SubmitResidentFeedback(payload);
}
export function mountResident({root,bridge,controller,getSessionID,subscribe=()=>()=>{},subscribeInteraction=()=>()=>{},onObservation=()=>{}}) {
 const node=root.querySelector('#resident-controls');
 const find=id=>root.querySelector(`#resident-${id}`);
 let config=null,state=null,configuredSession='',disposed=false;
 let preparePromise=null,prepareSession='';
 let observationRevision=0,pendingCandidate=null,lastSpokeAt=0;
 const targets=new Map();
 const doc=root.ownerDocument||root;
 const listeners=[];
 const listen=(id,event,handler)=>{const element=find(id);element?.addEventListener(event,handler);listeners.push([element,event,handler]);};
 const status=text=>{find('feedback-status').textContent=text;};
 function renderState(value) {
  if (!value?.state && !value?.policy) return;
  state=value?.state||value;
  find('policy').textContent=`現在の設定：${Object.entries(state.policy||{}).map(([field,value])=>`${FIELDS[field]||field}は${valueLabel(value)}`).join('，')}．`;
  const history=find('history'); history.replaceChildren();
  for (const change of (state?.changes||[]).slice(0,20)) {
   const line=doc.createElement('p'); line.textContent=`${FIELDS[change.field]||change.field}：${valueLabel(change.before)} → ${valueLabel(change.after)} · ${CHANGE[change.status]||change.status}． `;
   if (change.status==='applied') { const undo=doc.createElement('button');undo.type='button';undo.className='ghost-btn';undo.textContent='この変更を戻す';undo.addEventListener('click',async()=>{
    try {const result=await bridge.UndoResidentChange(change.change_id,{session_id:getSessionID(),dedupe_id:globalThis.crypto.randomUUID(),expected_revision:state.revision});renderState(result);status(residentResultText(result));}catch{status('変更を戻せませんでした．履歴を更新して確認できます．');}
   });line.appendChild(undo); } history.appendChild(line);
  }
  for (const feedback of (state?.feedback||[]).slice(0,20)) {const line=doc.createElement('p');line.textContent=`${feedback.kind||'指摘'} · ${feedback.status} · 聞こえた範囲：${({played:'全区間',partial:'一部',unplayed:'未再生',unknown:'不明'})[feedback.target?.delivery_status||'unknown']} · ${feedback.text||''}`;
   if(feedback.status!=='retracted' && feedback.event_id){for(const [label,remove] of [['指摘を取り消す',false],['指摘を削除する',true]]){const button=doc.createElement('button');button.type='button';button.className='ghost-btn';button.textContent=label;button.addEventListener('click',async()=>{try{invalidateCandidate();const result=await bridge.RetractResidentFeedback(feedback.event_id,{session_id:getSessionID(),dedupe_id:globalThis.crypto.randomUUID(),expected_revision:state.revision,delete:remove});renderState(result);status(remove?'指摘を削除しました．':residentResultText(result));}catch{status('取消・削除を完了できませんでした．履歴を更新できます．');}});line.appendChild(button);}}
   history.appendChild(line);}
 }
 async function prepare(sessionID=getSessionID()) {
  if (!config) await ready;
  if (!config?.enabled) return;
  const mode=find('mode').value;
  if (['observe','companion'].includes(mode) && !find('participants').checked) throw new Error('参加者全員の許可を確認してから観測を開始できます．');
  const participants=find('participant-ids').value.split(',').map(x=>x.trim()).filter(Boolean);
  if (['observe','companion'].includes(mode) && (!participants.length || participants.length>8 || !participants.includes(find('speaker-id').value.trim()))) throw new Error('参加者IDと，その中の発言者IDを指定してください．');
  if (preparePromise) {if(prepareSession===sessionID)return preparePromise;await preparePromise;}
  prepareSession=sessionID;
  preparePromise=(async()=>{const result=await bridge.ConfigureResidentSession(sessionID,find('owner').checked,find('consent').checked,mode);if(!disposed&&sessionID===getSessionID()){configuredSession=sessionID;renderState(result);}})();
  try {await preparePromise;}finally{preparePromise=null;}
 }
 const ready=Promise.resolve(bridge.GetResidentConfig()).then(async value=>{config=value;if(!value?.enabled||disposed)return;node.hidden=false;root.body?.classList.add('resident-enabled');for(const id of ['voice-session','voice-pause','voice-end','voice-record']){const element=root.querySelector(`#${id}`);if(element)element.hidden=true;}await prepare(getSessionID());status('休止中です．所有者と保存の設定を選び，待受を開始できます．');}).catch(()=>{if(node)node.hidden=false;status('常駐設定を取得できませんでした．Gatewayの起動を確認できます．');});
 listen('start','click',()=>{void(async()=>{try{await prepare();if(await controller.startSession())find('mode-status').textContent=LABELS[find('mode').value];}catch(error){status(error.message||'待受を開始できませんでした．');}})();});
 listen('pause','click',()=>{invalidateCandidate();void controller.pauseSession();find('mode-status').textContent=LABELS.paused;});
 listen('stop','click',()=>{invalidateCandidate();void controller.stopPlayback();status('現在の発話を停止しました．マイクの待受は継続します．');});
 listen('exit','click',()=>{void controller.endSession();void bridge.ExitResident();});
 for(const id of ['owner','consent','mode','participants','participant-ids','speaker-id'])listen(id,'change',()=>{void(async()=>{invalidateCandidate();await controller.pauseSession();find('mode-status').textContent=LABELS.paused;try{await prepare();status('設定を変更しました．待受を再開できます．');}catch(error){status(error.message||'設定を変更できませんでした．');}})();});
 async function submit(kind,text) {
  // Preserve the actual target before even preparing/reconfiguring the session．
  invalidateCandidate();
  const sid=getSessionID(),sourceSnapshot=find('target').value==='latest'?controller.snapshotFeedbackTarget():targets.get(find('target').value),target=feedbackTarget(sourceSnapshot,sid);
  if (/^(止めて|今は話しかけないで)/.test(text)) void controller.stopPlayback();
  try{if(configuredSession!==sid)await prepare(sid);status('指摘を受け付けました．保存を確認中です．');const result=await submitResidentFeedback({bridge,controller,sessionID:sid,revision:state.revision,owner:find('owner').checked,text,kind,mode:find('mode').value,target,sourceSnapshot});if(sid!==getSessionID())return;renderState(result);status(residentResultText(result));find('feedback-text').value='';}catch{status('指摘を保存できませんでした．履歴を更新して再送できます．');}
 }
 listen('good','click',()=>{void submit('positive',find('feedback-text').value);});
 listen('bad','click',()=>{void submit('negative',find('feedback-text').value);});
 listen('feedback-send','click',()=>{void submit(undefined,find('feedback-text').value);});
 listen('suppress','click',()=>{void submit(undefined,'今は話しかけないで');});
 listen('refresh','click',()=>{void bridge.GetResidentState(getSessionID()).then(renderState).catch(()=>status('履歴を取得できませんでした．'));});
 function invalidateCandidate() {
  controller.stopPreparedCandidate?.();
  observationRevision++;
  if(pendingCandidate){void bridge.CancelResidentCandidate(pendingCandidate.session_id,pendingCandidate.operation_id).catch(()=>{});pendingCandidate=null;}
 }
 async function observe(event) {
  invalidateCandidate();
  if(!find('participants').checked || !['observe','companion'].includes(find('mode').value))return;
  const revision=observationRevision,sid=getSessionID(),operationID=event.operation_id;
  const participants=find('participant-ids').value.split(',').map(x=>x.trim()).filter(Boolean),speaker=find('speaker-id').value.trim();
  if(!participants.length || !participants.includes(speaker))return;
  pendingCandidate={session_id:sid,operation_id:operationID};
  find('candidate-status').textContent='共有が許可された記憶を確認しています．';
  try {
   const result=await bridge.CreateResidentCandidate({session_id:sid,operation_id:operationID,revision,observation:event.observation.slice(0,2048),participants,speaker_id:speaker,observation_allowed:true});
   if(disposed||revision!==observationRevision||sid!==getSessionID())return;
   pendingCandidate=null;
   if(result.status!=='candidate'){find('candidate-status').textContent='今は参加できる候補がありません．';return;}
   if(find('mode').value==='observe'){find('candidate-status').textContent=`観測候補（再生なし）：${result.candidate.text}`;return;}
   if(controller.isHumanSpeaking() || Date.now()-lastSpokeAt<60000 || state.revision!==result.policy_revision){find('candidate-status').textContent='今は発言を控え，候補を破棄しました．';return;}
   if(await controller.startPreparedCandidate(operationID)){lastSpokeAt=Date.now();find('candidate-status').textContent='参加する音声を準備しています．再生直前に共有範囲と期限を再確認します．';}
   else find('candidate-status').textContent='会話の状態が変わったため候補を破棄しました．';
  } catch {if(revision===observationRevision)find('candidate-status').textContent='記憶確認を利用できませんでした．次の入力で再試行できます．';}
 }
 const unsubscribeInteraction=subscribeInteraction(event=>{
  const snapshot=event.snapshot;
  if(snapshot?.session_id!==getSessionID() || !snapshot?.response_plan?.text)return;
  targets.set(snapshot.operation_id,structuredClone(snapshot));while(targets.size>20)targets.delete(targets.keys().next().value);
  const selected=find('target').value;find('target').replaceChildren();
  const latest=doc.createElement('option');latest.value='latest';latest.textContent='直近の発言';find('target').appendChild(latest);
  for(const[id,item]of [...targets].reverse()){if(item.session_id!==getSessionID())continue;const option=doc.createElement('option');option.value=id;option.textContent=item.response_plan.text.slice(0,70);find('target').appendChild(option);}
  if([...targets.keys()].includes(selected))find('target').value=selected;
 });
 const unsubscribe=subscribe(result=>{if(result.session_id!==getSessionID())return;if(result.status==='observed'){status('許可された会話を観測しました．');onObservation(result);void observe(result);return;}renderState(result);status(residentResultText(result));});
 return {ready,prepare,onSessionState(value){if(!config?.enabled)return;if(value.state!=='listening')invalidateCandidate();find('mode-status').textContent=value.state==='listening'?LABELS[find('mode').value]:LABELS.paused;if(value.reason)status(value.reason);},get mode(){return config?.enabled?find('mode').value:'paused';},get enabled(){return config?.enabled===true;},dispose(){disposed=true;invalidateCandidate();unsubscribe?.();unsubscribeInteraction?.();for(const[element,event,handler]of listeners)element?.removeEventListener(event,handler);}};
}

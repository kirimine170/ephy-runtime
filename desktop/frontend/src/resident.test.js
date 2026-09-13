import test from 'node:test';
import assert from 'node:assert/strict';
import {feedbackTarget,residentResultText,residentMarkup,submitResidentFeedback} from './resident.js';
const snapshot=()=>({session_id:'s',turn_id:'t',operation_id:'old-run',generation_revision:2,model_id:'model',voice_id:'voice',prompt_id:'prompt',speech_units:[{unit_id:'heard',text:'聞こえた部分．',state:'completed',synthesis_complete:true,playback_started:true},{unit_id:'partial',text:'まだ聞こえていない内容．',state:'interrupted',playback_started:true}]});
test('feedback captures old generation and delivery before synchronous stop mutates the live run',async()=>{
 const old=snapshot();let stopped=false,payload;
 const controller={snapshotFeedbackTarget:()=>old,stopPlayback(){stopped=true;old.operation_id='new-run';old.speech_units=[];return new Promise(()=>{});}};
 const bridge={SubmitResidentFeedback(request){assert.equal(stopped,true);payload=request;return Promise.resolve({feedback:{status:'recorded'}});}};
 await submitResidentFeedback({bridge,controller,sessionID:'s',revision:4,owner:true,text:'止めて',dedupeID:'one'});
 assert.equal(payload.target.run_id,'old-run');assert.equal(payload.target.generation_id,'old-run:2');assert.deepEqual(payload.target.speech_unit_ids,['heard','partial']);assert.equal(payload.target.played_text,'聞こえた部分．');assert.equal(payload.target.delivery_status,'partial');assert.equal(payload.prompt_id,'prompt');
});
test('storage failure leaves local playback stopped and never reports saved',async()=>{
 let stopped=false;
 await assert.rejects(submitResidentFeedback({bridge:{SubmitResidentFeedback:()=>Promise.reject(Error('offline'))},controller:{snapshotFeedbackTarget:snapshot,stopPlayback(){stopped=true;}},sessionID:'s',revision:0,owner:false,text:'今は話しかけないで',dedupeID:'two'}));
 assert.equal(stopped,true);assert.match(residentResultText({status:'failed'}),/保存できません/);
});
test('foreign session target and absent audio have no invented attribution',()=>{
 assert.equal(feedbackTarget(snapshot(),'other'),undefined);
 assert.equal(feedbackTarget({...snapshot(),speech_units:[]},'s').delivery_status,'unknown');
});
test('no-op unsaved and applied changes have distinct user-visible outcomes',()=>{
 assert.match(residentResultText({change:{status:'no_change',scope:'session'}}),/既に同じ設定/);
 assert.match(residentResultText({change:{status:'unsaved',reason:'session_only_without_storage',scope:'session'}}),/保存なし/);
 assert.match(residentResultText({change:{status:'applied',scope:'owner'}}),/今後の会話/);
 assert.match(residentResultText({feedback:{status:'needs_clarification'}}),/対象/);
});
test('resident controls have explicit initial consent mode target pause stop resume and exit',()=>{
 const markup=residentMarkup();for(const id of ['resident-start','resident-pause','resident-stop','resident-exit','resident-mode','resident-target','resident-owner','resident-consent','resident-history'])assert.ok(markup.includes(`id="${id}"`));
 assert.ok(!markup.includes(' checked'));assert.ok(markup.includes('閉じると終了'));assert.ok(markup.includes('最小化しても待受'));
});

test('storage consent rejection never claims that a permanent preference was applied to the session',()=>{assert.match(residentResultText({saved:false,change:{status:'unsaved',reason:'storage_consent_required'}}),/保存していません/);assert.doesNotMatch(residentResultText({saved:false,change:{status:'unsaved',reason:'storage_consent_required'}}),/反映しました/);});

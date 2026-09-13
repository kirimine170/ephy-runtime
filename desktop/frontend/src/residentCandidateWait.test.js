import test from 'node:test';
import assert from 'node:assert/strict';
import {createResidentCandidateWait} from './residentCandidateWait.js';

function clock() {
  let time=0,sequence=0,peak=0,checks=0;
  const tasks=new Map();
  return {
    now:()=>time,
    timers:{
      setTimeout(callback,delay){const id=++sequence;tasks.set(id,{at:time+delay,callback});peak=Math.max(peak,tasks.size);return id;},
      clearTimeout(id){tasks.delete(id);},
    },
    advance(milliseconds){
      const until=time+milliseconds;
      for(;;){const next=[...tasks].sort((a,b)=>a[1].at-b[1].at)[0];if(!next||next[1].at>until)break;
        const[id,item]=next;tasks.delete(id);time=item.at;checks++;assert.ok(checks<100,'unbounded retry');item.callback();}
      time=until;
    },
    get pending(){return tasks.size;},get peak(){return peak;},get checks(){return checks;},
  };
}
function harness() {
  const time=clock(),decisions=[];
  const gate=createResidentCandidateWait({...time,now:time.now,timers:time.timers,onDecision:decision=>decisions.push(decision)});
  let humanSpeaking=false,current=true;
  return {time,decisions,gate,offer:extra=>gate.offer({expiresAt:15000,isCurrent:()=>current,isHumanSpeaking:()=>humanSpeaking,...extra}),
    set speaking(value){humanSpeaking=value;},set current(value){current=value;}};
}

test('ready candidate speaks once without scheduling a wait',async()=>{
  const h=harness();assert.equal(await h.offer(),'speak');assert.deepEqual(h.decisions,['speak']);assert.equal(h.time.pending,0);
});
test('one candidate waits for a gap then rechecks and speaks without generating another candidate',async()=>{
  const h=harness();h.speaking=true;const result=h.offer();assert.deepEqual(h.decisions,['wait']);assert.equal(h.time.pending,1);
  h.time.advance(500);assert.equal(h.gate.waiting,true);h.speaking=false;h.time.advance(250);
  assert.equal(await result,'speak');assert.deepEqual(h.decisions,['wait','speak']);assert.equal(h.time.pending,0);assert.equal(h.time.peak,1);
});
test('continuous speech exhausts a three-second wait and never requeues the stale candidate',async()=>{
  const h=harness();h.speaking=true;const result=h.offer();h.time.advance(3000);
  assert.equal(await result,'discard');assert.equal(h.time.checks,12);assert.equal(h.time.pending,0);
  h.speaking=false;h.time.advance(100000);assert.deepEqual(h.decisions,['wait','discard']);assert.equal(h.time.peak,1);
});
test('ticket expiry wins over the longer wait budget',async()=>{
  const h=harness();h.speaking=true;const result=h.offer({expiresAt:600});h.time.advance(600);
  assert.equal(await result,'discard');assert.equal(h.time.checks,3);assert.equal(h.time.pending,0);
});
test('short cooldown may finish inside the wait but a long cooldown discards immediately',async()=>{
  const short=harness();const result=short.offer({cooldownUntil:1000});short.time.advance(1000);
  assert.equal(await result,'speak');assert.deepEqual(short.decisions,['wait','speak']);
  const long=harness();assert.equal(await long.offer({cooldownUntil:60000}),'discard');assert.equal(long.time.pending,0);assert.deepEqual(long.decisions,['discard']);
});
test('input mode session revision and permission cancellation each clear the only timer',async()=>{
  for(const reason of ['new-input','direct-speech','mode','session','revision','participants']){
    const h=harness();h.speaking=true;const result=h.offer();h.gate.cancel(reason);
    assert.equal(await result,'discard',reason);assert.equal(h.time.pending,0,reason);
    h.speaking=false;h.time.advance(5000);assert.deepEqual(h.decisions,['wait','discard'],reason);
  }
});
test('context validity is rechecked after waiting even without an explicit cancel callback',async()=>{
  const h=harness();h.speaking=true;const result=h.offer();h.current=false;h.speaking=false;h.time.advance(250);
  assert.equal(await result,'discard');assert.equal(h.time.pending,0);assert.deepEqual(h.decisions,['wait','discard']);
});
test('new candidate replaces the single waiting candidate and dispose ends all future work',async()=>{
  const h=harness();h.speaking=true;const old=h.offer();const replacement=h.offer();assert.equal(await old,'discard');assert.equal(h.time.pending,1);assert.equal(h.time.peak,1);
  h.gate.dispose();assert.equal(await replacement,'discard');assert.equal(h.time.pending,0);assert.equal(await h.offer(),'discard');
  h.time.advance(20000);assert.deepEqual(h.decisions,['wait','discard','wait','discard']);
});
test('invalid or expired candidate deadlines never schedule work',async()=>{
  for(const expiresAt of [0,-1,NaN,Infinity]){const h=harness();assert.equal(await h.offer({expiresAt}),'discard');assert.equal(h.time.pending,0);}
});

import {mountResident,residentMarkup} from './resident.js';
class Element {
  constructor(){this.value='';this.checked=false;this.children=[];this.listeners=new Map();this.textContent='';}
  addEventListener(name,handler){this.listeners.set(name,handler);}
  removeEventListener(name){this.listeners.delete(name);}
  replaceChildren(...children){this.children=children;}
  appendChild(child){this.children.push(child);}
  dispatch(name){this.listeners.get(name)?.();}
}
const flush=async()=>{for(let step=0;step<8;step++)await Promise.resolve();};
async function panelHarness(){
  const time=clock();const nodes=new Map([...residentMarkup().matchAll(/id="([^"]+)"/g)].map(([,id])=>[id,new Element()]));
  nodes.get('resident-mode').value='reactive';nodes.get('resident-target').value='latest';
  const root={querySelector:selector=>nodes.get(selector.slice(1)),createElement:()=>new Element()};
  const calls={generate:0,cancel:0,start:0};let interaction,notification,sid='session';
  const bridge={GetResidentConfig:async()=>({enabled:true}),ConfigureResidentSession:async()=>({revision:0,policy:{proactive:'allowed'},changes:[],feedback:[]}),
    CreateResidentCandidate:async()=>{calls.generate++;return {status:'candidate',candidate:{text:'共有された候補'},policy_revision:0,expires_in_seconds:15};},
    CancelResidentCandidate:async()=>{calls.cancel++;}};
  const controller={isHumanSpeaking:()=>true,startPreparedCandidate:async()=>{calls.start++;return true;},stopPreparedCandidate(){},pauseSession:async()=>{},snapshotFeedbackTarget:()=>null};
  const panel=mountResident({root,bridge,controller,getSessionID:()=>sid,subscribe:callback=>{notification=callback;return()=>{};},subscribeInteraction:callback=>{interaction=callback;return()=>{};},candidateNow:time.now,candidateTimers:time.timers});
  await panel.ready;
  nodes.get('resident-mode').value='companion';nodes.get('resident-participants').checked=true;nodes.get('resident-participant-ids').value='owner';nodes.get('resident-speaker-id').value='owner';
  notification({session_id:'session',status:'observed',operation_id:'candidate',observation:'合成された観測'});await flush();
  assert.equal(time.pending,1);assert.match(nodes.get('resident-candidate-status').textContent,/待っています/);
  return {time,nodes,calls,panel,interaction,notification,changeSession(value){sid=value;}};
}
test('real resident subscriptions cancel the pending wait on a new direct ASR hypothesis',async()=>{
  const h=await panelHarness();h.interaction({session_id:'session',kind:'asr_update',asr:{phase:'partial',transcript:'Ephy，質問です'}});await flush();
  assert.equal(h.time.pending,0);assert.equal(h.calls.cancel,1);h.time.advance(20000);await flush();assert.equal(h.calls.generate,1);assert.equal(h.calls.start,0);h.panel.dispose();
});
test('real resident mode and conversation changes clear pending candidate timers',async()=>{
  const mode=await panelHarness();mode.nodes.get('resident-mode').value='observe';mode.nodes.get('resident-mode').dispatch('change');await flush();
  assert.equal(mode.time.pending,0);assert.equal(mode.calls.start,0);mode.panel.dispose();
  const session=await panelHarness();session.changeSession('new-session');await session.panel.prepare('new-session');await flush();
  assert.equal(session.time.pending,0);assert.equal(session.calls.start,0);session.panel.dispose();
});
test('real resident policy updates invalidate waiting candidates without regenerating',async()=>{
  const h=await panelHarness();h.notification({session_id:'session',state:{revision:1,policy:{proactive:'suppressed'},changes:[],feedback:[]}});await flush();
  assert.equal(h.time.pending,0);assert.equal(h.calls.generate,1);assert.equal(h.calls.start,0);h.panel.dispose();
});

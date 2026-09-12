// Production intent/PCM handoff controllers with real ASR，without playback．
import {readFile,writeFile} from 'node:fs/promises';
import {dirname,resolve} from 'node:path';
import {createHash} from 'node:crypto';
import {createVoiceInterruption,interruptionIntent,interruptionSettings} from '../../desktop/frontend/src/voiceInterruption.js';
import {createVoiceInputBuffer} from '../../desktop/frontend/src/voiceInputBuffer.js';
import {createPCM16StreamEncoder} from '../../desktop/frontend/src/voiceInteraction.js';
import {WorkerClient,readPCM,floatPCM,normalize,distance,delay,percentile} from './worker_client.mjs';

const [configPath,manifestPath,outputPath,filter='']=process.argv.slice(2);
if(!configPath||!manifestPath||!outputPath)throw new Error('usage: config manifest output');
const manifest=JSON.parse(await readFile(manifestPath,'utf8'));
const expected=new Map([['short-0',false],['short-1',true],['short-2',true],['short-3',true],['short-4',true],['quiet-command',true], ...manifest.samples.filter(s=>s.id.startsWith('noise-')).map(s=>[s.id,false])]);
const report={schema_version:1,kind:'real-worker-production-interruption-and-handoff',microphone_opened:false,audio_played:false,
  user_conversation_quality:'not_measured',real_playback_stop:'not_measured',results:[]};let worker;
try {
  worker=await WorkerClient.start(configPath);report.model_id=worker.config.model_id;report.helper_sha256=worker.config.helper_sha256;
  const selection=manifest.samples.filter(s=>expected.has(s.id)&&(!filter||filter.split(',').includes(s.id)));
  for(let index=0;index<selection.length;index++) {
    const sample=selection[index];const {sampleRate,pcm}=readPCM(await readFile(resolve(dirname(manifestPath),sample.audio)));
    if(createHash('sha256').update(pcm).digest('hex')!==sample.pcm_sha256)throw new Error('fixture_hash_mismatch');
    const audio=Buffer.concat([Buffer.alloc(sampleRate*2/5),pcm,Buffer.alloc(sampleRate*2*2)]);
    const buffer=createVoiceInputBuffer(sampleRate,3000);
    const owner={operation_id:`reply-${index}`,session_id:`session-${index}`,turn_id:`turn-${index}`,generation_revision:1};
    let entry,closed=Promise.resolve(),normal,normalReady=false,normalOpening,guard,confirmed=null,total=0,heldStart=0,normalSamples=0,failCode='',peakRMS=0;
    const observations=[],rejections=[];
    const entries=new Map();
    const start=performance.now();
    const snapshot=current=>({candidate_id:current.id,request:current.request,update:current.latest,activity:current.activity});
    const bridge={
      async BeginInteractionInterruptionCandidate(op,id,revision,rate){
        heldStart=total-buffer.samples;
        entry={id,request:{operation_id:op,session_id:owner.session_id,turn_id:owner.turn_id,segment_id:`segment-${index}-${id}`,sample_rate:rate}};
        const current=entry;
        entries.set(id,current);
        const previousCleanup=closed;
        entry.open=previousCleanup.then(()=>worker.open(rate,u=>{
          if(u.phase==='activity')current.activity=u;else if(['partial','stable','final'].includes(u.phase))current.latest=u;
          if(observations.length<64)observations.push({at_ms:Math.round(performance.now()-start),phase:u.phase,intent:u.transcript?interruptionIntent(u.transcript):undefined,
            speech_ms:u.activity?.speech_ms,has_speech:u.activity?.has_speech});
        },true,
          Object.fromEntries(Object.entries(current.request).filter(([key])=>key!=='sample_rate')),200));
        current.session=await current.open;return snapshot(current);
      },
      async AppendInteractionInterruptionCandidate(op,id,sequence,encoded){
        const current=entries.get(id);await current.session.append(Buffer.from(encoded,'base64'));return snapshot(current);
      },
      CancelInteractionInterruptionCandidate(op,id,reason){rejections.push(reason);const current=entries.get(id);closed=Promise.all([closed,(async()=>{if(current){const session=await current.open;await session.cancel();}})()]);closed.catch(()=>{});return closed;},
    };
    const sendNormal=async values=>{const encoded=createPCM16StreamEncoder(sampleRate);const bytes=encoded.push(values);normalSamples+=values.length;await normal.append(Buffer.from(bytes));};
    guard=createVoiceInterruption({bridge,identity:()=>owner,sampleRate,buffer,createEncoder:createPCM16StreamEncoder,
      modelActivity:true,settings:interruptionSettings({},true),
      encodeBase64:value=>Buffer.from(value).toString('base64'),now:()=>performance.now(),timers:globalThis,isCurrent:()=>true,phase:()=> 'PLAYING',onDuck:()=>{},
      onConfirm:({candidateMS})=>{
        confirmed={candidate_ms:candidateMS,from_fixture_audio_start_ms:Math.round(performance.now()-start-200)};
        normalOpening=(async()=>{
          await closed;normal=await worker.open(sampleRate,()=>{},false);
          let frame;while((frame=buffer.shift()))await sendNormal(frame);
          buffer.release();normalReady=true;
        })();normalOpening.catch(()=>{});
      }});
    try {
      const step=sampleRate*2/25;
      for(let offset=0;offset<audio.length;offset+=step){
        await delay(start+offset/(sampleRate*2)*1000-performance.now());
        const bytes=audio.subarray(offset,Math.min(audio.length,offset+step)),values=floatPCM(bytes);total+=values.length;
        peakRMS=Math.max(peakRMS,Math.sqrt(values.reduce((sum,v)=>sum+v*v,0)/values.length));
        if(!confirmed)guard.audio(values);
        else if(normalReady)await sendNormal(values);
        else if(!buffer.push(values))throw new Error('input_handoff_overflow');
      }
      await delay(60);
      if(normalOpening)await normalOpening;
      let final=null;
      if(normalReady)final=await normal.finish();
      const ref=normalize(sample.reference),hyp=normalize(final?.transcript||'');
      report.results.push({id:sample.id,expected_interrupt:expected.get(sample.id),confirmed:!!confirmed,...confirmed,
        final_phase:final?.phase||null,reference_characters:[...ref].length,edit_distance:distance(ref,hyp),
        expected_handoff_samples:confirmed?total-heldStart:0,received_handoff_samples:normalSamples,
        handoff_exact_once:!!confirmed&&total-heldStart===normalSamples,peak_rms:peakRMS,observations,rejections});
      const {observations:_,...compact}=report.results.at(-1);console.log(JSON.stringify(compact));
    }catch(error){failCode=/^[a-z_]+$/.test(error.message)?error.message:'runner_failed';throw error;}
    finally{guard.stop();await closed;if(normal&&!normal.closed)await normal.cancel();if(failCode)report.results.push({id:sample.id,error_code:failCode});}
  }
}catch(error){report.runner_error=/^[a-z_]+$/.test(error.message)?error.message:'runner_failed';process.exitCode=1;}
finally{
  worker?.close();const expected=report.results.filter(r=>r.expected_interrupt);
  Object.assign(report,{trials:report.results.length,expected_interrupts:expected.length,confirmed_interrupts:expected.filter(r=>r.confirmed).length,
    false_interrupts:report.results.filter(r=>r.expected_interrupt===false&&r.confirmed).length,
    confirmation_from_fixture_audio_start_p95_ms:percentile(expected.map(r=>r.from_fixture_audio_start_ms),.95),
    handoff_failures:report.results.filter(r=>r.confirmed&&!r.handoff_exact_once).length});
  report.candidate_unavailable_trials=report.results.filter(r=>r.rejections?.includes('unavailable')).length;
  await writeFile(outputPath,JSON.stringify(report,null,2)+'\n');
}

// Exercise the production endpoint with a real resident worker and explicit PCM．
import {readFile,writeFile} from 'node:fs/promises';
import {dirname,resolve} from 'node:path';
import {createHash} from 'node:crypto';
import {createVoiceEndpoint} from '../../desktop/frontend/src/voiceEndpoint.js';
import {WorkerClient,readPCM,floatPCM,normalize,distance,delay,percentile} from './worker_client.mjs';

const [configPath,manifestPath,outputPath,limitValue='20']=process.argv.slice(2);
if(!configPath||!manifestPath||!outputPath)throw new Error('usage: config manifest output [limit]');
const manifest=JSON.parse(await readFile(manifestPath,'utf8'));
const report={schema_version:1,kind:'real-worker-production-endpoint',source:manifest.source,microphone_opened:false,audio_played:false,
  user_conversation_quality:'not_measured',speech_timing_reference:'model VAD sample clock，not manually annotated speech',results:[]};
let worker,activeSample;
try {
  worker=await WorkerClient.start(configPath);report.model_id=worker.config.model_id;report.helper_sha256=worker.config.helper_sha256;report.load_and_warmup_ms=worker.loadMS;
  for(const sample of manifest.samples.slice(0,Number(limitValue))) {
    activeSample=sample;
    const {sampleRate,pcm}=readPCM(await readFile(resolve(dirname(manifestPath),sample.audio)));
    if(createHash('sha256').update(pcm).digest('hex')!==sample.pcm_sha256)throw new Error('fixture_hash_mismatch');
    const endpoint=createVoiceEndpoint({now:()=>performance.now()});endpoint.setActivityMode(true);
    let firstPartial=null,speechStart=null,lastSpeech=null,endpointAt=null,finalAt=null,ended=false,submitted=0,revisionCount=0;
    const s=await worker.open(sampleRate,frame=>{
      revisionCount++;
      if(frame.phase==='activity'){
        endpoint.activity(frame.activity);
        if(frame.activity.has_speech){lastSpeech=frame.activity.last_speech_ms;speechStart??=Math.max(0,lastSpeech-frame.activity.speech_ms);}
      }
      if(frame.phase==='partial'&&frame.transcript){firstPartial??=performance.now();endpoint.hypothesis(frame.transcript);}
    });
    const begin=performance.now();let pendingFinal,timer;
    const end=()=>{if(ended)return;ended=true;endpointAt=performance.now();clearInterval(timer);pendingFinal=s.finish().then(frame=>{finalAt=performance.now();return frame;});pendingFinal.catch(()=>{});};
    timer=setInterval(()=>{const decision=endpoint.tick();if(decision==='endpoint'||decision==='limit')end();},20);
    try {
      const frameBytes=sampleRate*2/25;
      const maxBytes=Math.min(sampleRate*2*60,pcm.length+sampleRate*2*3);
      while(!ended&&submitted<maxBytes){
        await delay(begin+submitted/(sampleRate*2)*1000-performance.now());if(ended)break;
        const size=Math.min(frameBytes,maxBytes-submitted);
        const bytes=Buffer.alloc(size);
        if(submitted<pcm.length)pcm.copy(bytes,0,submitted,Math.min(pcm.length,submitted+size));
        endpoint.audio(floatPCM(bytes),sampleRate);submitted+=size;await s.append(bytes);
      }
      if(!ended)end();
      const final=await pendingFinal;
      const ref=normalize(sample.reference),hyp=normalize(final.transcript||'');
      const result={id:sample.id,kind:sample.kind,phase:final.phase,reference_characters:[...ref].length,edit_distance:distance(ref,hyp),
        input_duration_ms:pcm.length/(sampleRate*2)*1000,submitted_audio_ms:submitted/(sampleRate*2)*1000,
        first_partial_from_vad_ms:firstPartial!==null&&speechStart!==null?Math.round(firstPartial-begin-speechStart):null,
        vad_to_endpoint_ms:lastSpeech===null?null:Math.round(endpointAt-begin-lastSpeech),endpoint_to_final_ms:Math.round(finalAt-endpointAt),
        vad_to_final_ms:lastSpeech===null?null:Math.round(finalAt-begin-lastSpeech),revision_count:revisionCount,worker_rss_kib:worker.rssKiB()};
      report.results.push(result);console.log(JSON.stringify({id:result.id,phase:result.phase,edit_distance:result.edit_distance,vad_to_final_ms:result.vad_to_final_ms}));
      activeSample=null;
    }finally{clearInterval(timer);if(!s.closed)await s.cancel();}
  }
}catch(error){
  report.runner_error=/^[a-z_]+$/.test(error.message)?error.message:'runner_failed';process.exitCode=1;
  if(activeSample){const length=[...normalize(activeSample.reference)].length;report.results.push({id:activeSample.id,phase:'failure',reference_characters:length,edit_distance:length});}
}
finally {
  worker?.close();const r=report.results;const chars=r.reduce((sum,v)=>sum+v.reference_characters,0);
  Object.assign(report,{trials:r.length,final_count:r.filter(v=>v.phase==='final').length,cer:chars?r.reduce((sum,v)=>sum+v.edit_distance,0)/chars:null,
    unattempted:Math.max(0,manifest.samples.slice(0,Number(limitValue)).length-r.length),
    first_partial_from_vad_p95_ms:percentile(r.map(v=>v.first_partial_from_vad_ms),.95),vad_to_final_p95_ms:percentile(r.map(v=>v.vad_to_final_ms),.95),
    endpoint_to_final_p95_ms:percentile(r.map(v=>v.endpoint_to_final_ms),.95),worker_peak_rss_kib:Math.max(0,...r.map(v=>v.worker_rss_kib||0))});
  await writeFile(outputPath,JSON.stringify(report,null,2)+'\n');
}

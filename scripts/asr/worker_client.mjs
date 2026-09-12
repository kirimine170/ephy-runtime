// Explicit fixture clients only．Transcripts stay in memory and never enter logs．
import {readFile} from 'node:fs/promises';
import {createReadStream} from 'node:fs';
import {createHash} from 'node:crypto';
import {spawn, execFileSync} from 'node:child_process';

export const normalize = text => text.normalize('NFKC').toLowerCase().replace(/[\p{P}\p{Z}\s]/gu, '');
export function distance(reference, hypothesis) {
  const a = [...reference], b = [...hypothesis]; let previous = b.map((_, i) => i + 1); previous.unshift(0);
  for (let i = 0; i < a.length; i++) {
    const current = [i + 1];
    for (let j = 0; j < b.length; j++) current.push(Math.min(current[j] + 1, previous[j+1] + 1, previous[j] + (a[i] !== b[j])));
    previous = current;
  }
  return previous.at(-1);
}
export const percentile = (values, q) => { const sorted = values.filter(Number.isFinite).sort((a,b) => a-b); return sorted.length ? sorted[Math.max(0, Math.ceil(sorted.length*q)-1)] : null; };
export const delay = ms => new Promise(resolve => setTimeout(resolve, Math.max(0, ms)));
const deferred = () => { let resolve, reject; const promise = new Promise((yes,no) => {resolve=yes;reject=no;}); promise.catch(() => {}); return {promise,resolve,reject}; };
export async function bounded(promise, ms, code) {
  let timer;
  try {return await Promise.race([promise, new Promise((_,reject) => {timer=setTimeout(() => reject(new Error(code)),ms);})]);}
  finally {clearTimeout(timer);}
}
export function readPCM(wav) {
  if (wav.toString('ascii',0,4)!=='RIFF' || wav.toString('ascii',8,12)!=='WAVE' || wav.readUInt32LE(4)+8!==wav.length) throw new Error('invalid_fixture');
  let sampleRate, pcm;
  for (let offset=12;offset+8<=wav.length;) {
    const kind=wav.toString('ascii',offset,offset+4), size=wav.readUInt32LE(offset+4), start=offset+8;
    if(start+size>wav.length)throw new Error('invalid_fixture');
    if(kind==='fmt ') {
      if(size<16 || wav.readUInt16LE(start)!==1 || wav.readUInt16LE(start+2)!==1 || wav.readUInt16LE(start+14)!==16)throw new Error('invalid_fixture');
      sampleRate=wav.readUInt32LE(start+4);
    }
    if(kind==='data')pcm=wav.subarray(start,start+size);
    offset=start+size+(size%2);
  }
  if(!sampleRate || sampleRate<8000 || sampleRate>48000 || !pcm?.length || pcm.length%2 || pcm.length>sampleRate*2*60)throw new Error('invalid_fixture');
  return {sampleRate,pcm};
}
export function floatPCM(pcm) {
  const values = new Float32Array(pcm.length/2);
  for(let i=0;i<values.length;i++)values[i]=pcm.readInt16LE(i*2)/32768;
  return values;
}
export class WorkerClient {
  static async start(configPath) {
    const config = JSON.parse(await readFile(configPath,'utf8'));
    for(const key of ['helper','model','vad']) {
      const hash=createHash('sha256');for await(const data of createReadStream(config[key]))hash.update(data);
      const digest=hash.digest('hex');
      if(digest!==config[`${key}_sha256`])throw new Error('asset_hash_mismatch');
    }
    const client=new WorkerClient(config);
    const started=performance.now();
    try {await bounded(client.ready.promise,60000,'asr_loading_timeout');}catch(error){client.close();throw error;}
    client.loadMS=Math.round(performance.now()-started);return client;
  }
  constructor(config) {
    this.config=config;this.ready=deferred();this.active=null;this.failure=null;this.buffer=Buffer.alloc(0);this.next=0;
    this.process=spawn(config.helper,['--model',config.model,'--vad',config.vad,'--model-revision',`${config.model_id}:sha256:${config.model_sha256.slice(0,16)}`,
      '--threads',String(config.threads||4),'--step-ms',String(config.step_ms||500)],{stdio:['pipe','pipe','ignore']});
    this.process.on('error',()=>this.fail('asr_worker_exited'));
    this.process.on('exit',()=>this.fail('asr_worker_exited'));
    this.process.stdout.on('data',data=>{
      try {
        this.buffer=Buffer.concat([this.buffer,data]);
        while(this.buffer.includes(10)) {
          const at=this.buffer.indexOf(10);if(at>96*1024)throw new Error('asr_stream_invalid');
          const line=this.buffer.subarray(0,at);this.buffer=this.buffer.subarray(at+1);this.receive(JSON.parse(line.toString('utf8')));
        }
        if(this.buffer.length>96*1024)throw new Error('asr_stream_invalid');
      }catch{this.fail('asr_stream_invalid');}
    });
  }
  fail(code) {
    this.failure ??= code;this.ready.reject(new Error(code));this.active?.started.reject(new Error(code));this.active?.done.reject(new Error(code));
  }
  receive(frame) {
    if(frame.protocol!==1)throw new Error('asr_stream_invalid');
    if(frame.type==='ready'){if(frame.provider!=='whisper-cpp'||frame.sample_rate!==16000)throw new Error('asr_stream_invalid');this.ready.resolve(frame);return;}
    if(frame.type==='fatal'){this.fail('asr_worker_exited');return;}
    const s=this.active;
    if(!s || Object.entries(s.identity).some(([key,value])=>frame[key]!==value))throw new Error('asr_stream_invalid');
    if(frame.type==='started'){s.started.resolve(frame);return;}
    if(frame.type==='update'){
      if(!Number.isSafeInteger(frame.revision)||frame.revision<=s.revision)throw new Error('asr_stream_invalid');
      s.revision=frame.revision;
      if(['final','no_speech','failure','timeout','canceled'].includes(frame.phase))s.terminal=frame;
      s.onUpdate(frame);return;
    }
    if(frame.type==='done' && s.terminal?.phase===frame.phase){s.closed=true;this.active=null;s.done.resolve(s.terminal);return;}
    throw new Error('asr_stream_invalid');
  }
  async send(s,frame) {
    if(this.failure)throw new Error(this.failure);
    await bounded(new Promise((resolve,reject)=>this.process.stdin.write(JSON.stringify({...frame,protocol:1,...s.identity})+'\n',err=>err?reject(new Error('asr_write_failed')):resolve())),2500,'asr_backpressure');
  }
  async open(sampleRate,onUpdate=()=>{},partial=true,identity=null,stepMS=this.config.step_ms||500) {
    if(this.active)throw new Error('asr_busy');
    const id=`fixture-${++this.next}`;
    const s={identity:identity||Object.fromEntries(['operation_id','session_id','turn_id','segment_id'].map(key=>[key,id])),sampleRate,onUpdate,
      started:deferred(),done:deferred(),sequence:0,revision:0,closed:false,terminal:null};this.active=s;
    s.append=pcm=>this.send(s,{type:'audio',sequence:++s.sequence,pcm_base64:pcm.toString('base64')});
    s.finish=async()=>{await this.send(s,{type:'finish'});return bounded(s.done.promise,30000,'asr_timeout');};
    s.cancel=async()=>{if(s.closed)return;await this.send(s,{type:'cancel'});return bounded(s.done.promise,4000,'asr_cancel_timeout');};
    await this.send(s,{type:'start',sample_rate:sampleRate,partial,step_ms:stepMS});await bounded(s.started.promise,2500,'asr_timeout');return s;
  }
  rssKiB() {try{return Number(execFileSync('ps',['-o','rss=','-p',String(this.process.pid)],{encoding:'utf8'}).trim())||null;}catch{return null;}}
  close() {this.process.stdin.destroy();this.process.kill('SIGTERM');}
}

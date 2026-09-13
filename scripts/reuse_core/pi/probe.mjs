// Probe only：no coding CLI，shell tools，extensions or persistent history．
import {createInterface} from 'node:readline';
import {Agent} from '@earendil-works/pi-agent-core';
import {streamSimple} from '@earendil-works/pi-ai/api/openai-completions';
const lines=createInterface({input:process.stdin,crlfDelay:Infinity})[Symbol.asyncIterator]();
const receive=async()=>{const value=await lines.next();if(value.done||Buffer.byteLength(value.value)>65536)throw Error('bounded_ipc_eof');return JSON.parse(value.value)};
const send=value=>process.stdout.write(JSON.stringify(value)+'\n');
const config=await receive();
const u=new URL(config.endpoint);if(u.protocol!=='http:'||u.hostname!=='127.0.0.1'||u.pathname!=='/v1')throw Error('local_endpoint_required');
let modelRequests=0,toolAttempts=0;
const tools=['karte_search','karte_read'].map(name=>{
 const key=name==='karte_search'?'query':'doc_id';
 return {name,label:name,description:name==='karte_search'?'Search shared memories．':'Read one shared memory by ID．',executionMode:'sequential',parameters:{type:'object',properties:{[key]:{type:'string',minLength:1,maxLength:key==='query'?2048:256}},required:[key],additionalProperties:false},execute:async(id,args,signal)=>{
  if(signal?.aborted||++toolAttempts>6)throw Error('tool_budget');
  send({type:'tool',name,arguments:args});const response=await receive();
  if(signal?.aborted||response.type!=='result')throw Error('tool_failed');
  return {content:[{type:'text',text:JSON.stringify(response.result)}],details:{}};
 }};
});
const agent=new Agent({initialState:{systemPrompt:config.instructions,model:{id:config.model,name:config.model,api:'openai-completions',provider:'local',baseUrl:config.endpoint,reasoning:true,input:['text'],cost:{input:0,output:0,cacheRead:0,cacheWrite:0},contextWindow:8192,maxTokens:2048,compat:{supportsStore:false,supportsDeveloperRole:false,supportsReasoningEffort:false,maxTokensField:'max_tokens',thinkingFormat:'qwen-chat-template',requiresReasoningContentOnAssistantMessages:true}},thinkingLevel:'medium',tools},toolExecution:'sequential',streamFn:async(model,context,options)=>{
 if(++modelRequests>4)throw Error('model_budget');send({type:'model'});const response=await receive();if(response.type!=='continue')throw Error('stale_run');
 return streamSimple(model,context,{...options,apiKey:'local-only',temperature:.3,maxTokens:2048,onPayload:payload=>({...payload,parallel_tool_calls:false,chat_template_kwargs:{enable_thinking:true,preserve_thinking:true}})});
}});
const timeout=setTimeout(()=>agent.abort(),60000);
process.on('SIGTERM',()=>{agent.abort();setTimeout(()=>process.exit(143),100).unref()});
try{
 await agent.prompt(config.query);
 const final=agent.state.messages.filter(m=>m.role==='assistant').at(-1);
 if(!final||final.stopReason!=='stop')throw Error('incomplete_generation');
 send({type:'final',text:final.content.filter(c=>c.type==='text').map(c=>c.text).join('')});
}catch(error){send({type:'error',code:String(error).slice(0,150)})}
finally{clearTimeout(timeout);process.stdin.destroy()}

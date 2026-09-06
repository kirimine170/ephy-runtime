import test from 'node:test';
import assert from 'node:assert/strict';
import {conversationHistory} from './conversationHistory.js';
test('text and voice history retains only completed messages，not context or stream drafts', () => {
  assert.deepEqual(conversationHistory([{role:'system',text:'private profile'},{role:'user',text:'one'},{role:'assistant',text:'two',sources:[{text:'private context'}]},{role:'assistant',text:'draft',streaming:true}]),[{role:'user',content:'one'},{role:'assistant',content:'two'}]);
});
test('conversation history is bounded and does not mutate UI entries', () => {
  const entries=Array.from({length:50},(_,i)=>({role:i%2?'assistant':'user',text:String(i)}));
  const result=conversationHistory(entries);
  assert.equal(result.length,28);assert.equal(result.at(-1).content,'49');assert.equal(entries.length,50);
  assert.equal(conversationHistory([{role:'user',text:'あ'.repeat(6000)}]).length,0);
});

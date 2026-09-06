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

test('failed canceled incomplete and pending voice text never become completed history', () => {
  const rejected = ['FAILED', 'CANCELED', 'INCOMPLETE', ''].map(terminalState => ({role: 'assistant', voice: true, terminalState, text: '確定した一文．'}));
  rejected.push({role: 'assistant', text: '上限で途中', finishReason: 'length'});
  rejected.push({role: 'assistant', text: '通信切断', finishReason: 'transport_eof'});
  rejected.push({role: 'assistant', text: 'tool request is not a completed answer', finishReason: 'tool_calls'});
  const complete = {role: 'assistant', voice: true, terminalState: 'COMPLETED', finishReason: 'stop', text: '回答は完結しました．', pendingText: 'not history', sources: [{text: 'private'}]};
  assert.deepEqual(conversationHistory([...rejected, complete]), [{role: 'assistant', content: '回答は完結しました．'}]);
});

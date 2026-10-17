import assert from 'node:assert/strict'
import type { AnalysisNode } from '../src/api/types.ts'
import { analysisDisplayLines } from '../src/analysis/display.ts'
import { setLanguage } from '../src/i18n/core.ts'
import { align } from '../src/analysis/sequence.ts'

const node: AnalysisNode = {
  id: 'stable-node', parent: '', turn: 1, sessions: [0, 1], hash: 'stable-hash', preview: '提问', truncated: false,
  text: '提问\n提问\n回复\n工具\n上下文\n思考\n调用 raw\n结果（ok）\n附件 x\n\n回复\n调用 提问\n工具\n结果（ok）\n思考\n原始正文\n附件 回复 image/png\nsha',
  labels: [
    { line: 0, kind: 'user' }, { line: 10, kind: 'assistant' }, { line: 11, kind: 'call', value: '提问' },
    { line: 12, kind: 'tool' }, { line: 13, kind: 'result', value: 'ok' }, { line: 14, kind: 'reasoning' },
    { line: 16, kind: 'attachment', value: '回复 image/png' },
  ],
}
const original = JSON.stringify(node), native = node.text.split('\n')
const baseline = align(native, [...native, 'new body'])
for (const [locale, prompt, reply, call, result] of [
  ['zh', '提问', '回复', '调用 提问', '结果（ok）'],
  ['zh-TW', '提問', '回覆', '呼叫 提问', '結果（ok）'],
  ['en', 'Prompt', 'Reply', 'Call 提问', 'Result (ok)'],
  ['ja', 'プロンプト', '回答', '呼び出し 提问', '結果（ok）'],
  ['de', 'Eingabe', 'Antwort', 'Aufruf 提问', 'Ergebnis (ok)'],
]) {
  setLanguage(locale)
  const displayed = analysisDisplayLines(node)
  assert.equal(displayed[0], prompt)
  assert.equal(displayed[10], reply)
  assert.equal(displayed[11], call, 'native tool name is not translated')
  assert.equal(displayed[13], result)
  assert.deepEqual(displayed.slice(1, 10), native.slice(1, 10), 'body lines that look like labels remain literal')
  assert.equal(displayed[15], '原始正文')
  assert.ok(displayed[16].endsWith('回复 image/png'), 'attachment name and media type remain literal')
  assert.equal(displayed.length, native.length, 'localized rows preserve original diff positions')
  assert.deepEqual(align(node.text.split('\n'), [...native, 'new body']), baseline, 'locale does not change alignment')
  assert.equal(JSON.stringify(node), original, 'localization does not mutate canonical display data, IDs, previews or hashes')
}
setLanguage('en')
assert.deepEqual(analysisDisplayLines({ ...node, labels: undefined }), native, 'legacy unannotated text remains untouched')
assert.deepEqual(analysisDisplayLines({ ...node, labels: [{ line: 0, kind: 'unknown' }, { line: -1, kind: 'user' }, { line: 99, kind: 'user' }] }), native)
assert.deepEqual(analysisDisplayLines(undefined), [])
assert.deepEqual(analysisDisplayLines({ ...node, text: '上下文\n正文\n…（内容较长，请在会话中查看全文）', labels: [{ line: 0, kind: 'context' }, { line: 2, kind: 'truncated' }] }), ['Context', '正文', '… (Long content; open the session to read it in full)'])
setLanguage('zh')
console.log('Analysis display: five locales, literal native content, stable diff rows, legacy data and truncation passed')

import assert from 'node:assert/strict'
import { threadTitle, turnsOf, userDisplayText } from '../src/api/model.ts'
import { beginSelection, selectionSource } from '../src/screens/selection.ts'
import type { HubEvent } from '../src/api/types.ts'
import type { Conversation } from '../src/store'

const wrapped = '<timestamp>2026-10-18</timestamp>\n<user_query>Read my diagram.</user_query>'
const event: HubEvent = {
  id: 'q1', thread: 'native', parents: [], actor: 'user', kind: 'message',
  attrs: { displayText: 'Read my diagram.' },
  parts: [
    { type: 'text', text: wrapped },
    { type: 'image', mediaType: 'image/png', blob: { sha256: 'image-bytes' } },
    { type: 'file', name: 'notes.md', mediaType: 'text/markdown', blob: { sha256: 'file-bytes' } },
  ],
  provenance: { source: { connector: 'cursor@native', harness: 'cursor', version: 'test' }, native: { session: 'native', id: 'q1' } },
}
const before = JSON.stringify(event), turns = turnsOf([event])
assert.equal(turns[0].user, 'Read my diagram.')
assert.equal(threadTitle({ bindings: [], events: [event] }), 'Read my diagram.')
assert.equal(turns[0].images[0].sha, 'image-bytes')
assert.equal(turns[0].files[0].sha, 'file-bytes')
const conversation = { turns, completeTurns: 1 } as Conversation
assert.equal(selectionSource(conversation, beginSelection(conversation))?.selector, 'q1')
assert.deepEqual(turns[0].eventIds, ['q1'])
assert.equal(JSON.stringify(event), before, 'displaying raw user text does not rewrite the actual model parts or copy IDs')
assert.equal(userDisplayText({ ...event, attrs: { displayText: '' } }), '', 'an explicit empty native field does not fall back to the wrapper')
assert.equal(userDisplayText({ ...event, attrs: {} }), wrapped, 'without evidence, the original request stays literal')
assert.equal(userDisplayText({ ...event, attrs: { displayText: 42 } }), wrapped)
assert.equal(userDisplayText({ ...event, attrs: { displayText: '<user_query>My own XML</user_query>' } }), '<user_query>My own XML</user_query>')
assert.equal(userDisplayText({ ...event, actor: 'assistant' }), wrapped, 'the display field is only interpreted on user messages')
assert.ok(userDisplayText({ ...event, parts: [{ type: 'text', text: wrapped }, { type: 'image', mediaType: 'image/png' }] }).includes('Read my diagram.'), 'unavailable material notices do not replace native text')
console.log('Native user display: original text, explicit empty text, attachments and immutable copy content passed')

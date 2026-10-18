import assert from 'node:assert/strict'
import { questionIDs, replyEventIDs, turnsOf } from '../src/api/model.ts'
import { beginSelection, paintSelection, selectionSource } from '../src/screens/selection.ts'
import type { HubEvent } from '../src/api/types.ts'
import type { Conversation } from '../src/store'

const event = (id: string, kind: string, actor: string, parent?: string): HubEvent => ({
  id, kind, actor, parents: parent ? [parent] : [], thread: 'native', parts: [{ type: 'text', text: id }],
  provenance: { source: { connector: 'desktop', harness: 'claude-code', version: 'test' }, native: { session: 'native', id } },
})
const file = (id: string, parent: string): HubEvent => ({ ...event(id, 'attachment', 'user', parent), parts: [{ type: 'file', name: '说明 中文🐙.txt', mediaType: 'text/plain', blob: { sha256: id } }] })
const events = [event('q1', 'message', 'user'), event('a1', 'message', 'assistant', 'q1'), event('q2', 'message', 'user', 'a1'), file('f1', 'q2'), file('f2', 'f1'), event('hidden', 'context', 'harness', 'f2'), event('a2', 'message', 'assistant', 'hidden')]
const before = JSON.stringify(events)
const turns = turnsOf(events)
assert.equal(turns.length, 2, 'post-question material is not another human turn')
assert.deepEqual(questionIDs(turns[1]), ['q2', 'f1', 'f2'])
assert.deepEqual(replyEventIDs(turns[1]), ['a2'])
assert.deepEqual(turns[1].files.map(f => f.sha), ['f1', 'f2'])
assert.deepEqual(turns[1].steps, [{ kind: 'text', text: 'a2' }])
assert.deepEqual(questionIDs(turns[0]), ['q1'])
const c = { turns, completeTurns: 2 } as Conversation
const all = beginSelection(c)!
const empty = { ...all, all: false, ids: new Set<string>() }
assert.deepEqual(selectionSource(c, paintSelection(empty, questionIDs(turns[1]), true))?.eventIds, ['q2', 'f1', 'f2'])
assert.deepEqual(selectionSource(c, paintSelection(empty, replyEventIDs(turns[1]), true))?.eventIds, ['a2'])
assert.deepEqual(selectionSource(c, paintSelection(all, questionIDs(turns[1]), false))?.excludeEventIds, ['q2', 'f1', 'f2'])
assert.equal(JSON.stringify(events), before, 'grouping does not mutate model parts or source IDs')
const wrongParent = turnsOf([event('q', 'message', 'user'), event('a', 'message', 'assistant', 'q'), file('orphan', 'a')])
assert.deepEqual(questionIDs(wrongParent[0]), ['q'], 'do not attach arbitrary files to the most recent human question')
assert.deepEqual(replyEventIDs(wrongParent[0]), ['a', 'orphan'])
assert.equal(turnsOf([file('f1', 'unloaded')], true)[0].partial, true, 'a page starting at material remains a partial turn')
console.log('Native question attachments: exact parent, one turn, independent question/reply selection and immutable material passed')

import assert from 'node:assert/strict'
import { mergeConversationTail, prependConversationPage } from '../src/conversationPages.ts'
import type { ThreadDetail, HubEvent } from '../src/api/types.ts'

const page = (from: number, to: number, total = to): ThreadDetail => ({ thread: 't', head: `e${total}`, events: Array.from({ length: to - from + 1 }, (_, i) => ({ id: `e${from + i}` }) as HubEvent), offset: from - 1, turns: total, completeTurns: total, bindings: [] })
const ids = (d?: ThreadDetail) => d?.events.map(event => event.id)
const expanded = page(41, 100)
const fresh = page(72, 101)
const joined = mergeConversationTail(expanded, fresh)!
assert.deepEqual(ids(joined), ids(page(41, 101)))
assert.equal(joined.head, 'e101')
assert.equal(joined.offset, 40)
const pendingEarlier = page(11, 40, 100)
const afterLateEarlier = prependConversationPage(joined, pendingEarlier, 'e41')!
assert.deepEqual(ids(afterLateEarlier), ids(page(11, 101)))
assert.equal(afterLateEarlier.head, 'e101', 'old pagination must not replace the new tail/head')
assert.equal(afterLateEarlier.turns, 101)
assert.deepEqual(ids(mergeConversationTail(page(1, 600), page(572, 601))), ids(page(1, 601)), 'refresh preserves more than 500 loaded turns')
assert.equal(mergeConversationTail(page(1, 30), page(60, 90)), undefined, 'no shared event means no invented gap')
assert.equal(prependConversationPage(fresh, pendingEarlier, 'e41'), undefined, 'an obsolete history boundary cannot reintroduce removed history')
const branch = { ...page(20, 21), events: [...page(20, 20).events, { id: 'branch-new' } as HubEvent] }
assert.deepEqual(ids(mergeConversationTail(page(1, 30), branch)), [...ids(page(1, 20))!, 'branch-new'], 'shared ancestor keeps prefix but drops the obsolete branch')
assert.deepEqual(ids(mergeConversationTail(page(40, 70), page(10, 80))), ids(page(10, 80)))
console.log('Conversation pages: concurrent tail/prepend, expanded history, missing joins and branch replacement passed')

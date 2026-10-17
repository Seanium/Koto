import assert from 'node:assert/strict'
import { beginSelection, paintSelection, selected, selectionEvents, selectionInvalid, selectionSource } from '../src/screens/selection.ts'
import type { Conversation } from '../src/store'
import { setLanguage } from '../src/i18n/core.ts'
import { turnsOf } from '../src/api/model.ts'
import type { HubEvent } from '../src/api/types.ts'

setLanguage('zh')

const conversation = (first: number, total: number) => ({ completeTurns: total, turns: Array.from({ length: total - first + 1 }, (_, i) => ({ index: first + i, eventIds: [`q${first + i}`, `a${first + i}`] })) }) as Conversation
const page = conversation(71, 100), all = beginSelection(page)!
assert.equal(selectionSource(page, all)?.label, '全部 100 轮', 'unloaded turns are included without loading their content')
assert.equal(all.ids.size, 0)
const cut = paintSelection(all, ['q99', 'a99', 'q100', 'a100'], false)
assert.equal(selectionSource(page, cut)?.label, '所选 98 轮')
assert.deepEqual(selectionSource(page, cut)?.excludeEventIds, ['q99', 'a99', 'q100', 'a100'])
assert.equal(selectionSource(conversation(1, 105), cut)?.selector, 'a100', 'new messages cannot move the frozen boundary')
assert.equal(selectionSource(conversation(1, 105), cut)?.label, '所选 98 轮')
const reverse = paintSelection(all, ['q100', 'a100'], false)
assert.equal(selectionSource(page, reverse)?.label, '所选 99 轮', 'dragging backwards restores items outside the new range')
assert.equal(selected(reverse, 'q99'), true)
const empty = { ...all, all: false, ids: new Set<string>() }
assert.equal(selectionSource(page, empty), undefined)
const excerpt = paintSelection(empty, ['q72', 'a100'], true)
assert.deepEqual(selectionSource(page, excerpt)?.eventIds, ['q72', 'a100'])
assert.equal(selectionSource(page, excerpt)?.label, '所选 2 轮')
assert.equal(selectionSource(page, paintSelection(all, page.turns.flatMap(t => t.eventIds), false))?.label, '所选 70 轮')
const edited = conversation(71, 100); edited.turns.at(-1)!.eventIds = ['replacement']
assert.equal(selectionSource(edited, cut), undefined, 'edited boundary never silently falls back to full copy')
assert.equal(selectionInvalid(edited, cut), true, 'a replaced boundary is different from an empty selection')
assert.equal(selectionInvalid(conversation(1, 3), beginSelection(conversation(1, 5))), true, 'a truncated source cannot keep its removed selection boundary')
assert.equal(selectionSource(conversation(1, 3), beginSelection(conversation(1, 5))), undefined)
const appended = conversation(71, 100)
appended.turns.at(-1)!.eventIds.push('a100-later')
assert.equal(selectionSource(appended, all)?.label, '全部 100 轮', 'appending to the final turn does not invalidate checked content')
assert.equal(selectionSource(appended, excerpt)?.label, '所选 2 轮')
assert.equal(selectionSource(appended, cut)?.label, '所选 98 轮', 'new final-turn content cannot undo an exclusion')
assert.equal(selectionSource(appended, all)?.selector, 'a100', 'same-turn appends cannot move the copy boundary')
assert.deepEqual(selectionEvents(appended.turns.at(-1)!, all), ['q100', 'a100'], 'new content is not offered as part of the frozen selection')
assert.deepEqual(selectionEvents(conversation(101, 101).turns[0], all), [])
assert.equal(beginSelection(undefined), undefined)

// Native context records can appear before, inside and after visible turns. They are not
// selectable replies, even when their parts contain images, files or reasoning.
const event = (id: string, kind: string, actor: HubEvent['actor'], parts: HubEvent['parts'] = [{ type: 'text', text: id }]): HubEvent => ({
  id, kind, actor, parts, parents: [], thread: 'test',
  provenance: { source: { connector: 'codex', harness: 'codex', version: 'test' }, native: { session: 'test', id } },
})
const events = [
  event('system-before', 'context', 'system'), event('q1', 'message', 'user'),
  event('hidden', 'context', 'harness', [{ type: 'image', mediaType: 'image/png', blob: { sha256: 'hidden-image' } }, { type: 'reasoning', text: 'hidden reasoning' }]),
  event('a1', 'message', 'assistant'), event('environment', 'context', 'user'),
  event('q2', 'message', 'user', [{ type: 'image', mediaType: 'image/png', blob: { sha256: 'user-image' } }]),
  event('a2', 'message', 'assistant'), event('system-after', 'context', 'system'),
]
const withContext = { completeTurns: 2, turns: turnsOf(events) } as Conversation
assert.deepEqual(withContext.turns.map(t => t.eventIds), [['q1', 'a1'], ['q2', 'a2']], 'hidden context never enters a selectable turn')
assert.deepEqual(withContext.turns[0].steps, [{ kind: 'text', text: 'a1' }], 'context media cannot leak into the visible reply')
assert.equal(withContext.turns[1].images[0].sha, 'user-image', 'user attachments remain selectable')
const contextAll = beginSelection(withContext)!
assert.equal(contextAll.head, 'a2', 'trailing context cannot become the copy boundary')
const pickFirst = paintSelection({ ...contextAll, all: false, ids: new Set<string>() }, withContext.turns[0].eventIds, true)
assert.deepEqual(selectionSource(withContext, pickFirst)?.eventIds, ['q1', 'a1'], 'clear then select a turn submits only conversation content')
assert.deepEqual(turnsOf(events.slice(2, 4), true).map(t => ({ ids: t.eventIds, partial: t.partial })), [{ ids: ['a1'], partial: true }], 'mid-turn pagination ignores context without losing the reply')
assert.deepEqual(turnsOf(events.filter(e => e.kind === 'context')), [], 'context alone is not a conversation turn')
console.log('Selection: paged default-all, excluded tails, disjoint messages and frozen source passed')

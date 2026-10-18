import assert from 'node:assert/strict'
import { coalescedRefresh } from '../src/refresh.ts'

let source = ['source', 'copy'], visible: string[] = [], reads = 0
let release: (() => void) | undefined
const refresh = coalescedRefresh(async () => {
  reads++
  const snapshot = [...source]
  await new Promise<void>(resolve => { release = resolve })
  visible = snapshot
})
const before = refresh()
source = ['source'] // source no longer appears while an earlier read is still in flight
const after = refresh(), also = refresh()
assert.equal(before, after)
assert.equal(after, also)
release!()
await new Promise(resolve => setTimeout(resolve, 0))
assert.equal(reads, 2, 'concurrent refresh requests share one follow-up read')
release!()
await after
assert.deepEqual(visible, ['source'], 'the mutation waits for a snapshot taken after it')
source.push('new-session')
const added = refresh(); release!(); await added
assert.deepEqual(visible, ['source', 'new-session'])

let fail = true
const retry = coalescedRefresh(async () => { if (fail) throw Error('offline') })
await assert.rejects(retry(), /offline/)
fail = false
await retry()
console.log('Refresh: source changes during read, coalesced updates, new sessions and retry passed')

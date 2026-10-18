import assert from 'node:assert/strict'
import { acquireMedia } from '../src/media.ts'

const urls = new Set<string>()
const create = URL.createObjectURL, revoke = URL.revokeObjectURL
URL.createObjectURL = blob => { const url = create(blob); urls.add(url); return url }
URL.revokeObjectURL = url => { urls.delete(url); revoke(url) }
let calls = 0, active = 0, peak = 0
const source = async () => { calls++; active++; peak = Math.max(peak, active); await new Promise(r => setTimeout(r, 1)); active--; return 'data:text/plain;base64,a290bw==' }
const a = acquireMedia(source, 'same'), b = acquireMedia(source, 'same')
assert.equal(await a.promise, await b.promise)
assert.equal(calls, 1)
a.release(); assert.equal(urls.size, 1)
b.release(); b.release(); assert.equal(urls.size, 0)
for (let cycle = 0; cycle < 25; cycle++) {
  const resources = Array.from({ length: 12 }, (_, i) => acquireMedia(source, String(i)))
  resources.slice(1).forEach(r => r.release()) // scrolling/unmount while pending
  await Promise.all(resources.map(r => r.promise))
  resources[0].release()
  assert.equal(urls.size, 0)
}
assert.ok(peak <= 2)
const error = acquireMedia(async () => { throw Error('unavailable') }, 'x')
await assert.rejects(error.promise, /unavailable/); error.release()
assert.equal(urls.size, 0)
let cancelled = 0
const slowSource = async (_sha: string, signal?: AbortSignal) => {
  await new Promise<void>((_resolve, reject) => signal!.addEventListener('abort', () => {
    cancelled++; reject(signal!.reason)
  }, { once: true }))
  return 'data:text/plain;base64,a290bw=='
}
const pendingA = acquireMedia(slowSource, 'native-pending'), pendingB = acquireMedia(slowSource, 'native-pending')
pendingA.release(); assert.equal(cancelled, 0, 'another mounted consumer still owns the native read')
pendingB.release()
assert.equal(cancelled, 1, 'last unmount cancels the native read instead of merely ignoring its result')
assert.deepEqual(await Promise.all([pendingA.promise, pendingB.promise]), ['', ''])
assert.equal(urls.size, 0)
console.log('Media lifecycle: shared loads, max two concurrent requests, 300 cancellations, no retained object URLs passed')

type Source = (sha: string) => Promise<string>
interface Entry { refs: number; promise: Promise<string>; url?: string; start: () => void }
const sources = new WeakMap<Source, Map<string, Entry>>()
const queue: Entry[] = []
let active = 0
const pump = () => { while (active < 2 && queue.length) queue.shift()!.start() }

/** Share only mounted consumers. No base64/image cache survives the last release.
 * Limit native IPC and decoding to two attachments at a time. */
export function acquireMedia(source: Source, sha: string) {
  let entries = sources.get(source)
  if (!entries) { entries = new Map(); sources.set(source, entries) }
  let entry = entries.get(sha)
  if (!entry) {
    let resolve!: (url: string) => void, reject!: (error: unknown) => void
    const promise = new Promise<string>((yes, no) => { resolve = yes; reject = no })
    const item: Entry = { refs: 0, promise, start: () => {
      if (!item.refs) { resolve(''); return }
      active++
      void (async () => {
        try {
          const blob = await (await fetch(await source(sha))).blob()
          const url = URL.createObjectURL(blob)
          if (item.refs) item.url = url
          else URL.revokeObjectURL(url)
          resolve(item.refs ? url : '')
        } catch (e) { reject(e) } finally { active--; pump() }
      })()
    } }
    entry = item; entries.set(sha, item); queue.push(item)
  }
  const held = entry, cache = entries
  held.refs++; pump()
  let released = false
  return { promise: held.promise, release: () => {
    if (released) return
    released = true
    if (--held.refs) return
    if (held.url) { URL.revokeObjectURL(held.url); held.url = undefined }
    if (cache.get(sha) === held) cache.delete(sha)
    if (!cache.size) sources.delete(source)
    const index = queue.indexOf(held)
    if (index >= 0) { queue.splice(index, 1); held.start() }
  } }
}

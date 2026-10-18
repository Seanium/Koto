import type { ThreadDetail } from './api/types.ts'

/** Refresh the tail without dropping the reader's earlier pages. Shared event IDs are
 * the join, so a rewritten or unrelated history is never concatenated by position. */
export function mergeConversationTail(previous: ThreadDetail, fresh: ThreadDetail): ThreadDetail | undefined {
  if (!previous.events.length || !fresh.events.length) return fresh
  const positions = new Map(previous.events.map((event, index) => [event.id, index]))
  const firstShared = fresh.events.findIndex(event => positions.has(event.id))
  if (firstShared < 0) return undefined
  // The refresh already reaches farther back than the visible history.
  if (firstShared > 0) return fresh
  const at = positions.get(fresh.events[0].id)!
  return { ...fresh, events: [...previous.events.slice(0, at), ...fresh.events], offset: previous.offset }
}

/** A delayed earlier page may finish after a newer tail. Keep the current tail and
 * only prepend at the exact boundary used for the request. */
export function prependConversationPage(current: ThreadDetail, page: ThreadDetail, before: string): ThreadDetail | undefined {
  const at = current.events.findIndex(event => event.id === before)
  if (at < 0 || !page.events.length) return undefined
  if (at > 0) {
    // Another read already supplied this boundary. Keep whichever prefix reaches farther.
    return mergeConversationTail(page, current)
  }
  const seen = new Set(current.events.map(event => event.id))
  return { ...current, events: [...page.events.filter(event => !seen.has(event.id)), ...current.events], offset: page.offset }
}

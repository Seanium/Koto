import { tr } from '../i18n/core.ts'
import type { Conversation } from '../store'
import type { CopySource } from './Continue'
import type { Turn } from '../api/model'

export interface Selection { head: string; through: number; all: boolean; ids: Set<string> }
export const selected = (s: Selection, id: string) => s.all !== s.ids.has(id)
/** Appends may extend the final turn, but cannot extend an already chosen copy boundary. */
export function selectionEvents(t: Turn, s: Selection): string[] {
  if (t.partial || t.index > s.through) return []
  if (t.index < s.through) return t.eventIds
  const end = t.eventIds.indexOf(s.head)
  return end < 0 ? [] : t.eventIds.slice(0, end + 1)
}
export function selectionInvalid(c: Conversation | undefined, s?: Selection): boolean {
  if (c && s && (c.totalTurns ?? c.completeTurns) < s.through) return true
  const boundary = s && c?.turns.find(t => t.index === s.through)
  return !!(s && boundary && !boundary.eventIds.includes(s.head))
}
export function beginSelection(c?: Conversation): Selection | undefined {
  const head = c?.turns.find(t => t.index === c.completeTurns)?.eventIds.at(-1)
  return head && c ? { head, through: c.completeTurns, all: true, ids: new Set() } : undefined
}
export function paintSelection(s: Selection, events: string[], value: boolean): Selection {
  const ids = new Set(s.ids)
  for (const id of events) s.all === value ? ids.delete(id) : ids.add(id)
  return { ...s, ids }
}
export function selectionSource(c: Conversation | undefined, s?: Selection): CopySource | undefined {
  if (!c || !s) return
  const turns = c.turns.filter(t => t.index <= s.through && !t.partial)
  if (selectionInvalid(c, s)) return // replaced source: never silently copy a different range
  const count = s.all ? s.through - turns.filter(t => selectionEvents(t, s).every(id => !selected(s, id))).length : turns.filter(t => selectionEvents(t, s).some(id => selected(s, id))).length
  if (!count) return
  return { selector: s.head, ...(s.all ? s.ids.size ? { excludeEventIds: [...s.ids] } : {} : { eventIds: [...s.ids] }), label: s.all && !s.ids.size ? tr("全部 {0} 轮", [count]) : tr("所选 {0} 轮", [count]) }
}

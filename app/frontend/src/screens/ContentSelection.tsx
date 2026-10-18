import { createContext, useContext, useLayoutEffect, useMemo, useRef, useState } from 'react'
import type { Conversation } from '../store'
import type { Turn } from '../api/model'
import { replyEventIDs } from '../api/model'
import { beginSelection, paintSelection, selected, selectionEvents, selectionInvalid, selectionSource, type Selection } from './selection'

export interface MessageSelection { state: Selection; has: (id: string) => boolean; events: (ids: string[]) => string[]; toggle: (events: string[]) => void; apply: (s: Selection) => void }
export const SelectionContext = createContext<MessageSelection | undefined>(undefined)
export const replyIDs = (t: Turn) => replyEventIDs(t)

/** Transient choices stay in the current conversation; no second view or modal. */
export function useContentSelection(c?: Conversation) {
  const [state, setState] = useState<Selection>()
  const eligible = useMemo(() => new Set(state ? c?.turns.flatMap(t => selectionEvents(t, state)) : []), [c, state?.head, state?.through])
  const events = (ids: string[]) => ids.filter(id => eligible.has(id))
  const toggle = (ids: string[]) => { const allowed = events(ids); setState(old => old && allowed.length ? paintSelection(old, allowed, !allowed.every(id => selected(old, id))) : old) }
  const source = selectionSource(c, state)
  return { active: !!state, start: () => setState(beginSelection(c)), exit: () => setState(undefined),
    canStart: !!beginSelection(c), clear: () => setState(s => s ? { ...s, all: false, ids: new Set() } : s),
    all: () => setState(beginSelection(c)), source, invalid: selectionInvalid(c, state),
    selection: state ? { state, has: (id: string) => eligible.has(id) && selected(state, id), events, toggle, apply: setState } : undefined }
}

/** Shared controls live beside the real turn, prompt and reply in both native views. */
export function SelectionCheck({ events: ids, label, disabled = false, scope = 'message' }: { events: string[]; label: string; disabled?: boolean; scope?: 'turn' | 'message' }) {
  const selection = useContext(SelectionContext)
  const events = selection?.events(ids) ?? []
  const ref = useRef<HTMLInputElement>(null)
  const count = events.filter(id => selection?.has(id)).length
  useLayoutEffect(() => { if (ref.current) ref.current.indeterminate = count > 0 && count < events.length }, [count, events.length])
  if (!selection || !events.length) return null
  return <label className="message-check no-drag" data-selection-unit={disabled ? undefined : scope} data-selection-events={events.join(' ')} data-selected={count > 0 || undefined} title={label} onClick={e => e.stopPropagation()}>
    <input ref={ref} type="checkbox" checked={count === events.length} disabled={disabled} onChange={() => selection.toggle(events)} aria-label={label} />
    <span className="sr-only">{label}</span>
  </label>
}

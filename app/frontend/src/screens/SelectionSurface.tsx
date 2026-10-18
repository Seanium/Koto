import { useContext, useEffect, useRef, type ReactNode } from 'react'
import { SelectionContext } from './ContentSelection'
import { paintSelection, type Selection } from './selection'

/** Paint an inclusive range from the pointer-down snapshot, so reversing a drag restores it.
 * The only animation loop runs while dragging, including edge scrolling within that pane. */
export function SelectionSurface({ children, onFocusTurn }: { children: ReactNode; onFocusTurn?: (turn: number) => void }) {
  const selection = useContext(SelectionContext)
  const current = useRef(selection); current.current = selection
  const focusTurn = useRef(onFocusTurn); focusTurn.current = onFocusTurn
  const showTurn = (el: Element) => {
    const turn = Number(el.closest<HTMLElement>('[data-turn]')?.dataset.turn)
    if (turn > 0) focusTurn.current?.(turn)
  }
  const host = useRef<HTMLDivElement>(null)
  const suppress = useRef(false)
  const frame = useRef(0)
  const drag = useRef<{ base: Selection; value: boolean; anchor: string; scope: string; pane: HTMLElement; y: number; last: string } | undefined>(undefined)
  const stop = (cancel = false) => {
    if (cancel && drag.current) current.current?.apply(drag.current.base)
    drag.current = undefined; cancelAnimationFrame(frame.current); frame.current = 0
  }
  useEffect(() => {
    const blur = () => stop()
    window.addEventListener('blur', blur)
    return () => { stop(); window.removeEventListener('blur', blur) }
  }, [])
  const paint = () => {
    const d = drag.current
    if (!d || !current.current) return
    const box = d.pane.getBoundingClientRect(), y = Math.max(box.top + 2, Math.min(box.bottom - 2, d.y))
    if (d.y < box.top + 32) d.pane.scrollTop -= Math.min(18, (box.top + 32 - d.y) / 2)
    else if (d.y > box.bottom - 32) d.pane.scrollTop += Math.min(18, (d.y - box.bottom + 32) / 2)
    const unique = new Map<string, HTMLElement>()
    for (const el of d.pane.querySelectorAll<HTMLElement>(`[data-selection-unit="${d.scope}"]`)) {
      const key = el.dataset.selectionEvents!
      if (!unique.has(key)) unique.set(key, el)
    }
    const units = [...unique.entries()], anchor = units.findIndex(([key]) => key === d.anchor)
    if (anchor >= 0) {
      let end = units.findIndex(([, el]) => el.getBoundingClientRect().bottom >= y)
      if (end < 0) end = units.length - 1
      const last = units[end]?.[0]
      if (last && last !== d.last) {
        d.last = last
        showTurn(units[end][1])
        const events = units.slice(Math.min(anchor, end), Math.max(anchor, end) + 1).flatMap(([key]) => key.split(' '))
        current.current.apply(paintSelection(d.base, events, d.value))
      }
    }
    frame.current = requestAnimationFrame(paint)
  }
  return <div ref={host} className="selection-surface col grow" data-selecting={!!selection || undefined}
    onPointerDownCapture={e => {
      suppress.current = false
      if (!selection || e.button !== 0 || !(e.target instanceof Element)) return
      const unit = e.target.closest<HTMLElement>('[data-selection-unit]')
      if (!unit) return
      const control = e.target.closest('button,a,input,textarea,select,summary,[role="button"],[role="link"],[contenteditable="true"],audio,video')
      // Range handles retain drag selection. Content controls keep their own click,
      // keyboard and focus behavior even while a copy range is being selected.
      if (control && !(control instanceof HTMLInputElement && !control.disabled && control.closest('.message-check'))) return
      const pane = unit.closest<HTMLElement>('.scroll'), events = unit.dataset.selectionEvents
      if (!pane || !events) return
      e.preventDefault(); suppress.current = true
      showTurn(unit)
      const ids = events.split(' '), value = !ids.every(selection.has)
      drag.current = { base: selection.state, value, anchor: events, scope: unit.dataset.selectionUnit!, pane, y: e.clientY, last: events }
      selection.apply(paintSelection(selection.state, ids, value))
      try { e.currentTarget.setPointerCapture(e.pointerId) } catch { /* synthetic UI audit */ }
      frame.current = requestAnimationFrame(paint)
    }}
    onFocusCapture={e => { if (selection && e.target instanceof Element) showTurn(e.target) }}
    onPointerMove={e => { if (drag.current) drag.current.y = e.clientY }}
    onPointerUp={() => { if (drag.current) { cancelAnimationFrame(frame.current); paint() }; stop() }} onPointerCancel={() => stop(true)} onLostPointerCapture={() => stop()}
    onClickCapture={e => { if (suppress.current) { e.preventDefault(); e.stopPropagation(); suppress.current = false } }}>
    {children}
  </div>
}

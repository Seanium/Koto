import { tr } from '../i18n/core.ts'
import { useEffect, useLayoutEffect, useRef, useState, type ReactNode } from 'react'
import { clampPane, paneBounds } from './paneSize'

/** A small local layout preference; no backend calls or persistent pointer listeners. */
export function SplitPane({ name, label, initial, ratio, min, max = Infinity, minRest, minRatio, maxRatio, first, second, className = '' }: {
  name: string; label: string; initial?: number; ratio?: number; min: number; max?: number; minRest: number
  minRatio?: number; maxRatio?: number; first: ReactNode; second: ReactNode; className?: string
}) {
  const key = `koto.pane.${name}`
  const [preferred, setPreferred] = useState<number | undefined>(() => {
    try { const v = JSON.parse(localStorage.getItem(key) ?? 'null'); return typeof v === 'number' && Number.isFinite(v) && v > 0 ? v : undefined } catch { return undefined }
  })
  const host = useRef<HTMLDivElement>(null)
  const [total, setTotal] = useState(0)
  const [dragging, setDragging] = useState(false)
  const drag = useRef<{ pointer: number; x: number; width: number; before?: number; next: number } | undefined>(undefined)
  const frame = useRef<number | undefined>(undefined)
  const bounds = paneBounds(Math.max(0, total - 1), min, max, minRest, minRatio, maxRatio)
  const width = clampPane(preferred ?? initial ?? total * (ratio ?? .5), bounds)
  const save = (value?: number) => {
    setPreferred(value)
    try { if (value === undefined) localStorage.removeItem(key); else localStorage.setItem(key, String(value)) } catch { /* storage is optional */ }
  }
  const finish = (cancel = false) => {
    const d = drag.current
    if (!d) return
    if (frame.current !== undefined) cancelAnimationFrame(frame.current)
    frame.current = undefined; drag.current = undefined
    setDragging(false); save(cancel ? d.before : d.next)
  }
  useLayoutEffect(() => {
    const el = host.current!
    const measure = () => setTotal(Math.round(el.getBoundingClientRect().width))
    measure()
    const observer = new ResizeObserver(measure)
    observer.observe(el)
    return () => observer.disconnect()
  }, [])
  useEffect(() => () => { if (frame.current !== undefined) cancelAnimationFrame(frame.current) }, [])
  useEffect(() => {
    if (!dragging) return
    document.body.classList.add('resizing-pane')
    const blur = () => finish()
    window.addEventListener('blur', blur)
    return () => { document.body.classList.remove('resizing-pane'); window.removeEventListener('blur', blur) }
  }, [dragging]) // eslint-disable-line react-hooks/exhaustive-deps
  return <div ref={host} className={`pane-split row grow ${className}`}>
    <div className="pane-primary col" style={{ width }}>{first}</div>
    <div className="pane-divider">
      <div role="separator" tabIndex={0} aria-label={label} aria-orientation="vertical" aria-valuemin={Math.round(bounds.min)} aria-valuemax={Math.round(bounds.max)} aria-valuenow={width}
        className="pane-handle no-drag" data-dragging={dragging || undefined} title={tr("拖动调整宽度，双击恢复默认")}
        onPointerDown={e => {
          if (e.button !== 0) return
          e.preventDefault(); e.currentTarget.focus({ preventScroll: true })
          drag.current = { pointer: e.pointerId, x: e.clientX, width, before: preferred, next: width }
          setDragging(true)
          try { e.currentTarget.setPointerCapture(e.pointerId) } catch { /* synthetic test events have no active pointer */ }
        }}
        onPointerMove={e => {
          const d = drag.current
          if (!d || d.pointer !== e.pointerId) return
          d.next = clampPane(d.width + e.clientX - d.x, bounds)
          if (frame.current === undefined) frame.current = requestAnimationFrame(() => { frame.current = undefined; if (drag.current) setPreferred(drag.current.next) })
        }}
        onPointerUp={e => { if (drag.current?.pointer === e.pointerId) finish() }}
        onPointerCancel={() => finish(true)} onLostPointerCapture={() => finish()}
        onDoubleClick={() => save(undefined)}
        onKeyDown={e => {
          if (e.key === 'Escape' && drag.current) { e.preventDefault(); e.stopPropagation(); finish(true); return }
          const next = e.key === 'ArrowLeft' ? width - (e.shiftKey ? 40 : 10) : e.key === 'ArrowRight' ? width + (e.shiftKey ? 40 : 10) : e.key === 'Home' ? bounds.min : e.key === 'End' ? bounds.max : undefined
          if (next !== undefined) { e.preventDefault(); e.stopPropagation(); save(clampPane(next, bounds)) }
        }} />
    </div>
    <div className="pane-secondary col grow">{second}</div>
  </div>
}

import { tr } from '../i18n/core.ts'
// The one component set every window uses (see ui.css). Screens compose these.

import { forwardRef, useEffect, useLayoutEffect, useRef, useState, type ButtonHTMLAttributes, type ReactNode } from 'react'
import { createPortal } from 'react-dom'
import './ui.css'
import { agentName } from '../api/model'
import claudeCode from '../assets/harness/claude-code.svg'
import codex from '../assets/harness/codex.svg'
import piSvg from '../assets/harness/pi.svg?raw'
import dsh from '../assets/harness/dsh.svg'
import antigravity from '../assets/harness/antigravity.svg'
import opencodeSvg from '../assets/harness/opencode.svg?raw'
import zcodeSvg from '../assets/harness/zcode.svg?raw'
import workbuddy from '../assets/harness/workbuddy.svg'
import cursorSvg from '../assets/harness/cursor.svg?raw'
import { shortcut } from './shortcut'

// ---------------------------------------------------------------- keeping panels inside the window

/** Shifts an open menu or panel sideways so it never runs past the window edge (which would scroll the
 * whole page sideways). */
function useKeepInView(open: boolean) {
  const panel = useRef<HTMLDivElement>(null)
  useLayoutEffect(() => {
    const el = panel.current
    if (!open || !el) return
    const fit = () => {
      el.style.translate = ''
      const r = el.getBoundingClientRect(), m = 8, w = document.documentElement.clientWidth
      const dx = r.right > w - m ? w - m - r.right : r.left < m ? m - r.left : 0
      if (dx) el.style.translate = `${Math.round(dx)}px 0`
    }
    fit()
    window.addEventListener('resize', fit)
    return () => window.removeEventListener('resize', fit)
  }, [open])
  return panel
}

// ---------------------------------------------------------------- Button

/** primary (one per screen) · secondary (M by default, `small` = S) · ghost (list filters, tabs, nav) · tint (project button) · link (bare text). */
export type ButtonVariant = 'primary' | 'secondary' | 'ghost' | 'tint' | 'link'
type BtnProps = ButtonHTMLAttributes<HTMLButtonElement> & { variant?: ButtonVariant; small?: boolean; icon?: boolean }

export const Button = forwardRef<HTMLButtonElement, BtnProps>(function Button({ variant = 'secondary', small, icon, className, ...rest }, ref) {
  const cls = ['btn', `btn-${variant}`, small && 'btn-small', icon && 'btn-icon', className].filter(Boolean).join(' ')
  return <button ref={ref} type="button" className={cls} {...rest} />
})

/** One-line text: measure only this label; animate only while the user asks to read it. */
export function OverflowLabel({ text }: { text: string }) {
  const ref = useRef<HTMLSpanElement>(null)
  useLayoutEffect(() => {
    const el = ref.current!, content = el.firstElementChild as HTMLElement
    const measure = () => {
      const distance = Math.max(0, content.scrollWidth - el.clientWidth)
      el.dataset.overflow = String(distance > 1)
      el.style.setProperty('--text-travel', `-${distance}px`)
      el.style.setProperty('--text-duration', `${Math.max(4, distance / 45 + 2)}s`)
    }
    const observer = new ResizeObserver(measure)
    observer.observe(el); measure()
    return () => observer.disconnect()
  }, [text])
  return <span ref={ref} className="overflow-label" title={text} tabIndex={0}><span>{text}</span></span>
}

// ---------------------------------------------------------------- Switch / Checkbox

export function Switch({ checked, onChange, disabled, label, title }: { checked: boolean; onChange: (v: boolean) => void; disabled?: boolean; label: string; title?: string }) {
  return <button type="button" role="switch" aria-checked={checked} aria-label={label} title={title ?? label} className="switch" disabled={disabled} onClick={() => onChange(!checked)} />
}

export function Checkbox({ checked, onChange, children }: { checked: boolean; onChange: (v: boolean) => void; children: ReactNode }) {
  return (
    <label className="check" onClick={e => { e.preventDefault(); onChange(!checked) }}>
      <button type="button" role="checkbox" aria-checked={checked} className="check-box">
        {checked && <svg width="10" height="10" viewBox="0 0 10 10"><path d="M1.5 5.2 4 7.6 8.5 2.6" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" /></svg>}
      </button>
      <span className="grow">{children}</span>
    </label>
  )
}

// ---------------------------------------------------------------- AgentDisc

const MARKS: Record<string, { src?: string; svg?: string }> = {
  'claude-code': { src: claudeCode },
  codex: { src: codex },
  pi: { svg: piSvg }, // monochrome: inlined so it takes the text colour in light and dark
  dsh: { src: dsh },
  antigravity: { src: antigravity },
  opencode: { svg: opencodeSvg },
  zcode: { svg: zcodeSvg },
  workbuddy: { src: workbuddy },
  'workbuddy-ai': { src: workbuddy },
  cursor: { svg: cursorSvg },
}

export { agentName }

/** Availability of an Agent icon. */
export type DiscState = 'present' | 'absent'

export function AgentDisc({ harness, size = 20, state = 'present', title }: { harness: string; size?: number; state?: DiscState; title?: string }) {
  const m = MARKS[harness]
  const g = Math.round(size * 0.64)
  return (
    <span className="disc" data-state={state} title={title ?? agentName(harness)} style={{ width: size, height: size }}>
      {!m ? (
        <span style={{ fontSize: Math.round(g * 0.8), fontWeight: 600 }}>{harness[0]?.toUpperCase()}</span>
      ) : m.svg ? (
        <span className="disc-mono" style={{ width: g, height: g }} dangerouslySetInnerHTML={{ __html: m.svg }} />
      ) : (
        <img src={m.src} width={g} height={g} alt="" draggable={false} />
      )}
    </span>
  )
}

export function Dot({ tone }: { tone: 'ok' | 'warn' | 'bad' | 'idle' }) {
  return <span className="dot" data-tone={tone} />
}

// ---------------------------------------------------------------- ListRow

type RowProps = ButtonHTMLAttributes<HTMLButtonElement> & { selected?: boolean; compact?: boolean }

export const ListRow = forwardRef<HTMLButtonElement, RowProps>(function ListRow({ selected, compact, className, ...rest }, ref) {
  return <button ref={ref} type="button" role="option" aria-selected={!!selected} className={['lrow', compact && 'lrow-compact', className].filter(Boolean).join(' ')} {...rest} />
})

// ---------------------------------------------------------------- Sheet

const scrollLocks = new WeakMap<HTMLElement, { count: number; restore: () => void }>()

/** GTK's native overlay scrollbars can paint over a web dialog. Lock only its background. */
function lockBackgroundScroll(dialog: HTMLElement) {
  const locked: HTMLElement[] = []
  const scrollers = '.scroll, .sheet-body, .analysis-session-choices, .analysis-legend, .code-open, .codeblock-body, .attachment-preview pre, .menu-floating, .outline-list'
  for (const el of document.querySelectorAll<HTMLElement>(scrollers)) {
    if (dialog.contains(el) || el.contains(dialog)) continue
    let lock = scrollLocks.get(el)
    if (!lock) {
      const properties = ['overflow-x', 'overflow-y', 'padding-right', 'padding-bottom']
      const saved = properties.map(name => [name, el.style.getPropertyValue(name), el.style.getPropertyPriority(name)])
      const style = getComputedStyle(el)
      const paddingRight = parseFloat(style.paddingRight) || 0, paddingBottom = parseFloat(style.paddingBottom) || 0
      const width = el.clientWidth, height = el.clientHeight
      el.style.setProperty('overflow-x', 'hidden')
      el.style.setProperty('overflow-y', 'hidden')
      // Preserve the content width on systems whose scrollbars reserve layout space.
      const extraWidth = el.clientWidth - width, extraHeight = el.clientHeight - height
      if (extraWidth > 0) el.style.setProperty('padding-right', `${paddingRight + extraWidth}px`)
      if (extraHeight > 0) el.style.setProperty('padding-bottom', `${paddingBottom + extraHeight}px`)
      lock = { count: 0, restore: () => {
        for (const [name, value, priority] of saved) {
          if (value) el.style.setProperty(name, value, priority)
          else el.style.removeProperty(name)
        }
      } }
      scrollLocks.set(el, lock)
    }
    lock.count++
    locked.push(el)
  }
  return () => {
    for (const el of locked) {
      const lock = scrollLocks.get(el)
      if (lock && --lock.count === 0) { lock.restore(); scrollLocks.delete(el) }
    }
  }
}

export function Sheet({ open, onClose, children, label, className }: { open: boolean; onClose: () => void; children: ReactNode; label: string; className?: string }) {
  const ref = useRef<HTMLDivElement>(null)
  const close = useRef(onClose); close.current = onClose
  useLayoutEffect(() => {
    if (open && ref.current) return lockBackgroundScroll(ref.current)
  }, [open])
  useEffect(() => {
    if (!open) return
    const prev = document.activeElement as HTMLElement | null
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && !document.querySelector('.menu')) { e.preventDefault(); close.current() }
      if (e.key === 'Tab' && ref.current) { // keep focus inside
        const f = [...ref.current.querySelectorAll<HTMLElement>('button:not(:disabled), input, [tabindex="0"]')]
        if (!f.length) return
        const i = f.indexOf(document.activeElement as HTMLElement)
        if (e.shiftKey && i <= 0) { e.preventDefault(); f[f.length - 1].focus() }
        else if (!e.shiftKey && i === f.length - 1) { e.preventDefault(); f[0].focus() }
      }
    }
    window.addEventListener('keydown', onKey, true)
    const frame = requestAnimationFrame(() => (ref.current?.querySelector<HTMLElement>('[data-autofocus]') ?? ref.current?.querySelector<HTMLElement>('button:not(:disabled)'))?.focus())
    return () => { cancelAnimationFrame(frame); window.removeEventListener('keydown', onKey, true); prev?.focus?.() }
  }, [open])
  if (!open) return null
  return (
    <div className="overlay no-drag" onMouseDown={e => { if (e.target === e.currentTarget) onClose() }}>
      <div ref={ref} className={['sheet', className].filter(Boolean).join(' ')} role="dialog" aria-modal="true" aria-label={label}>{children}</div>
    </div>
  )
}

// ---------------------------------------------------------------- Menu (a button that opens a small list of actions)

export type MenuItem =
  | { label: string; onClick: () => void; checked?: boolean; hint?: string; /** Hint in mono (a shortcut, an app name). */ hintMono?: boolean
      /** A second, muted line. */ sub?: string; key?: string; disabled?: boolean; title?: string; icon?: ReactNode }
  | { separator: true; key?: string }

const isSep = (i: MenuItem): i is { separator: true; key?: string } => 'separator' in i

/**
 * A button that opens a small list of actions. The trigger is an icon button unless `trigger` names a
 * Button variant (ghost: list filters; secondary small: a selector; tint: the project button).
 * `align`: where the list opens — 'left' (default: the right edges line up), 'right' (wide, from the
 * left edge) or 'under' (280px, from the left edge). `header` goes above the items.
 */
export function Menu({ label, items, children, trigger, pressed, searchable, align = 'left', header, title, width, disabled }: {
  label: string; items: MenuItem[]; children: ReactNode; trigger?: 'ghost' | 'secondary' | 'tint'; pressed?: boolean
  searchable?: string; align?: 'left' | 'right' | 'under'; header?: ReactNode; title?: string; width?: number; disabled?: boolean
}) {
  const [open, setOpen] = useState(false)
  const [q, setQ] = useState('')
  useEffect(() => { if (disabled) setOpen(false) }, [disabled])
  useEffect(() => { if (!open) setQ('') }, [open])
  const shown = q ? items.filter(i => !isSep(i) && `${i.label} ${i.sub ?? ''}`.toLowerCase().includes(q.toLowerCase())) : items
  const ref = useRef<HTMLDivElement>(null)
  const panel = useRef<HTMLDivElement>(null)
  // Keep action menus outside scrolling version lists. Focusing an absolutely positioned child
  // inside those lists otherwise scrolls the whole list sideways to reveal the menu.
  useLayoutEffect(() => {
    if (!open) return
    const fit = () => {
      const anchor = ref.current, el = panel.current
      if (!anchor || !el) return
      const a = anchor.getBoundingClientRect(), margin = 8
      const w = document.documentElement.clientWidth, h = document.documentElement.clientHeight
      const below = h - a.bottom - 4 - margin, above = a.top - 4 - margin
      const down = below >= Math.min(240, el.scrollHeight) || below >= above
      el.style.maxHeight = `${Math.max(0, Math.min(360, down ? below : above))}px`
      const r = el.getBoundingClientRect()
      const x = align === 'left' ? a.right - r.width : a.left
      const y = down ? a.bottom + 4 : a.top - r.height - 4
      el.style.left = `${Math.max(margin, Math.min(x, w - r.width - margin))}px`
      el.style.top = `${Math.max(margin, y)}px`
    }
    fit()
    window.addEventListener('resize', fit)
    window.addEventListener('scroll', fit, true)
    return () => { window.removeEventListener('resize', fit); window.removeEventListener('scroll', fit, true) }
  }, [open, align, shown.length])
  useEffect(() => {
    if (!open) return
    const close = (e: Event) => { if (!(e.target instanceof Node && (ref.current?.contains(e.target) || panel.current?.contains(e.target)))) setOpen(false) }
    const esc = (e: KeyboardEvent) => { if (e.key === 'Escape') { e.preventDefault(); setOpen(false); ref.current?.querySelector('button')?.focus() } }
    window.addEventListener('mousedown', close); window.addEventListener('keydown', esc)
    const frame = requestAnimationFrame(() => panel.current?.querySelector<HTMLElement>('.menu-search, .menu-item:not(:disabled)')?.focus({ preventScroll: true }))
    return () => { cancelAnimationFrame(frame); window.removeEventListener('mousedown', close); window.removeEventListener('keydown', esc) }
  }, [open])
  const common = { 'aria-haspopup': 'menu' as const, 'aria-expanded': open, disabled, onClick: () => setOpen(o => !o) }
  return (
    <div ref={ref} className="menu-anchor no-drag">
      {trigger
        ? <Button aria-label={label} variant={trigger} aria-pressed={trigger === 'ghost' ? !!pressed : undefined} title={title ?? label} {...common}>{children}</Button>
        : <Button variant="ghost" icon aria-label={label} title={title ?? label} {...common}>{children}</Button>}
      {open && createPortal(
        <div ref={panel} style={{ width }} className={`menu menu-floating ${align === 'right' ? 'menu-right' : align === 'under' ? 'menu-under' : trigger ? 'menu-left' : ''}`} role="menu" aria-label={label}>
          {header && <div className="menu-head">{header}</div>}
          {searchable && <input className="menu-search" value={q} onChange={e => setQ(e.target.value)} placeholder={searchable} aria-label={searchable} />}
          {searchable && !shown.length && <div className="t-meta" style={{ padding: '6px 8px' }}>{tr("没有匹配的项")}</div>}
          {shown.map((i, n) => isSep(i) ? <div key={i.key ?? `sep${n}`} className="menu-sep" role="separator" /> : (
            <button key={i.key ?? i.label} type="button" role={i.checked === undefined ? 'menuitem' : 'menuitemradio'} aria-checked={i.checked} disabled={i.disabled} title={i.title ?? (i.sub ? `${i.label}\n${i.sub}` : i.label)}
              className="menu-item row g2" onClick={() => { setOpen(false); i.onClick() }}>
              {i.checked !== undefined && <span style={{ width: 12, display: 'inline-flex', alignSelf: i.sub !== undefined ? 'flex-start' : undefined, paddingTop: i.sub !== undefined ? 3 : undefined }}>{i.checked && Icon.check}</span>}
              {i.icon && <span className="row" style={{ flex: 'none' }}>{i.icon}</span>}
              {i.sub !== undefined
                ? <span className="col grow" style={{ gap: 2 }}><span className="ellipsis">{i.label}</span><span className="t-meta ellipsis">{i.sub}</span></span>
                : <span className="grow ellipsis">{i.label}</span>}
              {i.hint && <span className={i.hintMono ? 'menu-hint-mono' : 't-meta'} style={{ alignSelf: i.sub !== undefined ? 'flex-start' : undefined }}>{i.hint}</span>}
            </button>
          ))}
        </div>, document.body
      )}
    </div>
  )
}

// ---------------------------------------------------------------- Popover (a button that opens a panel)

/** A trigger and a panel under it; outside click and Esc close it. `open`/`setOpen` let the caller drive it. */
export function Popover({ trigger, children, open, setOpen, width = 460, align = 'left', label }: {
  trigger: (p: { 'aria-expanded': boolean; 'aria-haspopup': 'dialog'; onClick: () => void }) => ReactNode
  children: ReactNode; open: boolean; setOpen: (v: boolean) => void; width?: number; align?: 'left' | 'right'; label: string
}) {
  const ref = useRef<HTMLDivElement>(null)
  const panel = useKeepInView(open)
  useEffect(() => {
    if (!open) return
    const close = (e: Event) => { if (!(e.target instanceof Node && ref.current?.contains(e.target)) && !(e.target instanceof Element && e.target.closest('.menu:not(.pop)'))) setOpen(false) }
    // Esc closes an open menu inside the panel first, then the panel.
    const esc = (e: KeyboardEvent) => { if (e.key === 'Escape' && !document.querySelector('.menu:not(.pop)')) { e.stopPropagation(); setOpen(false) } }
    window.addEventListener('mousedown', close); window.addEventListener('keydown', esc, true)
    return () => { window.removeEventListener('mousedown', close); window.removeEventListener('keydown', esc, true) }
  }, [open, setOpen])
  return (
    <div ref={ref} className="menu-anchor no-drag">
      {trigger({ 'aria-expanded': open, 'aria-haspopup': 'dialog', onClick: () => setOpen(!open) })}
      {open && <div ref={panel} className={`menu pop ${align === 'right' ? 'pop-right' : ''}`} role="dialog" aria-label={label} style={{ width, maxWidth: 'calc(100vw - 16px)' }}>{children}</div>}
    </div>
  )
}

// ---------------------------------------------------------------- Chip (a filter toggle with text: a ghost button)

export function Chip({ pressed, onClick, title, children }: { pressed: boolean; onClick: () => void; title: string; children: ReactNode }) {
  return <Button variant="ghost" aria-pressed={pressed} title={title} onClick={onClick}>{children}</Button>
}

// ---------------------------------------------------------------- Toast (window centred, one optional action)

export function Toast({ text, sub, action, onAction, onDone }: { text: string; sub?: string; action?: string; onAction?: () => void; onDone: () => void }) {
  // The timer restarts only for a new text, not on every render of the parent.
  const done = useRef(onDone)
  done.current = onDone
  useEffect(() => { const t = setTimeout(() => done.current(), action || sub ? 6000 : 2600); return () => clearTimeout(t) }, [text, sub, action])
  return createPortal(
    <div className="toast-region"><div className="toast" role="status" aria-atomic="true">
      <span className="toast-copy">{text}{sub && <span className="toast-sub">{sub}</span>}</span>
      {action && <button type="button" className="toast-action" onClick={() => { onAction?.(); onDone() }}>{action}</button>}
    </div></div>, document.body
  )
}

// ---------------------------------------------------------------- Banner / Segmented / SearchField / Kbd

export function Banner({ children, action }: { children: ReactNode; action?: ReactNode }) {
  return <div className="banner" role="status"><span className="grow">{children}</span>{action}</div>
}

export function OperationError({ message, detail }: { message: string; detail?: string }) {
  return <div className="operation-error" role="alert"><div>{message}</div>{detail && <details><summary>{tr("查看详细原因")}</summary><pre>{detail}</pre></details>}</div>
}

export function Segmented<T extends string>({ value, options, onChange, label }: { value: T; options: { value: T; label: string }[]; onChange: (v: T) => void; label: string }) {
  return (
    <div className="seg no-drag" role="group" aria-label={label}>
      {options.map(o => <button key={o.value} type="button" aria-pressed={o.value === value} onClick={() => onChange(o.value)}>{o.label}</button>)}
    </div>
  )
}

export const SearchField = forwardRef<HTMLInputElement, { value: string; onChange: (v: string) => void; placeholder: string; onKeyDown?: (e: React.KeyboardEvent) => void }>(
  function SearchField({ value, onChange, placeholder, onKeyDown }, ref) {
    return (
      <label className="search no-drag">
        <svg width="13" height="13" viewBox="0 0 16 16" aria-hidden><circle cx="7" cy="7" r="5" fill="none" stroke="currentColor" strokeWidth="1.6" /><path d="m11 11 3.5 3.5" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" /></svg>
        <input ref={ref} value={value} placeholder={placeholder} onChange={e => onChange(e.target.value)} onKeyDown={onKeyDown} spellCheck={false} />
        {!value && <Kbd>⌘K</Kbd>}
      </label>
    )
  })

export function Kbd({ children }: { children: ReactNode }) {
  return <kbd className="kbd">{typeof children === 'string' ? shortcut(children) : children}</kbd>
}

export function Skeleton({ w, h = 12 }: { w: number | string; h?: number }) {
  return <span className="skel" style={{ display: 'block', width: w, height: h }} />
}

// ---------------------------------------------------------------- icons (stroke, currentColor)

export const Icon = {
  info: <svg width="16" height="16" viewBox="0 0 24 24" aria-hidden fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round"><circle cx="12" cy="12" r="9" /><path d="M12 11v5M12 8h.01" /></svg>,
  file: <svg width="16" height="18" viewBox="0 0 24 24" aria-hidden fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinejoin="round"><path d="M14 3H5v18h14V8zM14 3v5h5M8 12h8M8 16h6" /></svg>,
  refresh: <svg width="15" height="15" viewBox="0 0 20 20" aria-hidden fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"><path d="M16 8a6.25 6.25 0 1 0-.6 5.2M16 3.5V8h-4.5" /></svg>,
  back: <svg width="10" height="10" viewBox="0 0 10 10" aria-hidden><path d="M6.3 2.2 3.5 5l2.8 2.8" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round" /></svg>,
  chevron: <svg width="10" height="10" viewBox="0 0 10 10" aria-hidden><path d="m2.5 3.8 2.5 2.5 2.5-2.5" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round" /></svg>,
  gear: <svg width="15" height="15" viewBox="0 0 24 24" aria-hidden fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinejoin="round"><path d="M19.18 10.22 L21.47 10.42 L21.47 13.58 L19.18 13.78 L18.34 15.82 L19.81 17.58 L17.58 19.81 L15.82 18.34 L13.78 19.18 L13.58 21.47 L10.42 21.47 L10.22 19.18 L8.18 18.34 L6.42 19.81 L4.19 17.58 L5.66 15.82 L4.82 13.78 L2.53 13.58 L2.53 10.42 L4.82 10.22 L5.66 8.18 L4.19 6.42 L6.42 4.19 L8.18 5.66 L10.22 4.82 L10.42 2.53 L13.58 2.53 L13.78 4.82 L15.82 5.66 L17.58 4.19 L19.81 6.42 L18.34 8.18Z" /><circle cx="12" cy="12" r="3" /></svg>,
  check: <svg width="14" height="14" viewBox="0 0 14 14" aria-hidden><path d="m2.5 7.3 3 3 6-6.3" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round" /></svg>,
  copy: <svg width="13" height="13" viewBox="0 0 14 14" aria-hidden><rect x="4.5" y="4.5" width="7.5" height="7.5" rx="1.5" fill="none" stroke="currentColor" strokeWidth="1.3" /><path d="M9.5 2.5V2a.5.5 0 0 0-.5-.5H2.5A.5.5 0 0 0 2 2v6.5a.5.5 0 0 0 .5.5H3" fill="none" stroke="currentColor" strokeWidth="1.3" /></svg>,
  terminal: <svg width="14" height="14" viewBox="0 0 24 24" aria-hidden fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round"><rect x="3" y="4.5" width="18" height="15" rx="2.5" /><path d="m7.5 9.5 3 2.5-3 2.5M12.5 15h4" /></svg>,
  desktop: <svg width="14" height="14" viewBox="0 0 24 24" aria-hidden fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round"><rect x="3" y="4" width="18" height="12.5" rx="2" /><path d="M9 20h6M12 16.5V20" /></svg>,
  all: <svg width="16" height="16" viewBox="0 0 24 24" aria-hidden fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round"><path d="M5 15.5 2.5 18V5.5A2 2 0 0 1 4.5 3.5h11a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5Z" /><path d="M8 18.5h11l2.5 2.5V9a2 2 0 0 0-2-2" /></svg>,
  more: <svg width="15" height="15" viewBox="0 0 16 16" aria-hidden><circle cx="3.5" cy="8" r="1.2" fill="currentColor" /><circle cx="8" cy="8" r="1.2" fill="currentColor" /><circle cx="12.5" cy="8" r="1.2" fill="currentColor" /></svg>,
  trash: <svg width="15" height="15" viewBox="0 0 24 24" aria-hidden fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round"><path d="M4 7h16" /><path d="M9 7V4h6v3" /><path d="M6 7l1 13h10l1-13" /></svg>,
  disclosure: <svg width="10" height="10" viewBox="0 0 10 10" aria-hidden><path d="m3.8 2.5 2.5 2.5-2.5 2.5" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round" /></svg>,
  folder: <svg width="13" height="13" viewBox="0 0 24 24" aria-hidden fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinejoin="round"><path d="M3 6.5A1.5 1.5 0 0 1 4.5 5H9l2 2h8.5A1.5 1.5 0 0 1 21 8.5v9a1.5 1.5 0 0 1-1.5 1.5h-15A1.5 1.5 0 0 1 3 17.5z" /></svg>,
  branch: <svg width="13" height="13" viewBox="0 0 24 24" aria-hidden fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round"><circle cx="6" cy="5" r="2" /><circle cx="6" cy="19" r="2" /><circle cx="18" cy="9" r="2" /><path d="M6 7v10" /><path d="M18 11c0 4-6 3-11 7" /></svg>,
  left: <svg width="10" height="10" viewBox="0 0 10 10" aria-hidden><path d="M6.3 2.2 3.5 5l2.8 2.8" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" /></svg>,
  right: <svg width="10" height="10" viewBox="0 0 10 10" aria-hidden><path d="m3.7 2.2 2.8 2.8-2.8 2.8" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" /></svg>,
  up: <svg width="11" height="11" viewBox="0 0 12 12" aria-hidden><path d="M6 10V2M2.5 5.5 6 2l3.5 3.5" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round" /></svg>,
  down: <svg width="11" height="11" viewBox="0 0 12 12" aria-hidden><path d="M6 2v8M2.5 6.5 6 10l3.5-3.5" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round" /></svg>,
  search: <svg width="12" height="12" viewBox="0 0 16 16" aria-hidden><circle cx="7" cy="7" r="5" fill="none" stroke="currentColor" strokeWidth="1.6" /><path d="m11 11 3.5 3.5" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" /></svg>,
  close: <svg width="10" height="10" viewBox="0 0 10 10" aria-hidden><path d="m2.5 2.5 5 5m0-5-5 5" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" /></svg>,
  sync: <svg width="15" height="15" viewBox="0 0 16 16" aria-hidden><path d="M13 6.5A5 5 0 0 0 4 4.4L3 5.5M3 9.5a5 5 0 0 0 9 2.1l1-1.1" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" /><path d="M3 2.5v3h3M13 13.5v-3h-3" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round" /></svg>,
}

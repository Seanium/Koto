import { useEffect, useRef, useState, type ButtonHTMLAttributes, type HTMLAttributes, type RefObject } from 'react'
import { shell } from '../api/client'
import { tr } from '../i18n/core'
import { agentOrder, moveAgent } from './agentOrder'
import { setAgentPreference, useAgentPreference } from './agentPreferences'

/** Native drag/drop keeps the bundle small. Order changes only on a valid drop or keyboard move. */
export function useAgentOrder(ids: string[], enabled: string[], name: (id: string) => string, scroll: RefObject<HTMLDivElement | null>) {
  const { saved, ready } = useAgentPreference()
  const [saving, setSaving] = useState(false)
  const [dragged, setDragged] = useState<string>()
  const [drop, setDrop] = useState<{ id: string; after: boolean }>()
  const [announcement, setAnnouncement] = useState('')
  const [error, setError] = useState<string>()
  const active = useRef<string | undefined>(undefined)
  const clickUntil = useRef(0)
  const scrollFrame = useRef(0)
  const pointerY = useRef<number | undefined>(undefined)
  const order = agentOrder(ids, saved, name)
  const visible = order.filter(id => enabled.includes(id))
  useEffect(() => {
    const focused = document.activeElement as HTMLElement | null
    if (focused?.closest('.side-agent-group')) focused.scrollIntoView({ block: 'nearest' })
  }, [saved])
  const stop = () => {
    active.current = undefined; pointerY.current = undefined
    cancelAnimationFrame(scrollFrame.current); scrollFrame.current = 0
    setDragged(undefined); setDrop(undefined)
  }
  useEffect(() => {
    const cancel = (e: KeyboardEvent) => { if (e.key === 'Escape' && active.current) stop() }
    window.addEventListener('keydown', cancel, true)
    window.addEventListener('blur', stop)
    return () => { window.removeEventListener('keydown', cancel, true); window.removeEventListener('blur', stop); cancelAnimationFrame(scrollFrame.current) }
  }, [])
  const autoScroll = () => {
    const el = scroll.current, y = pointerY.current
    if (el && y !== undefined && active.current) {
      const rect = el.getBoundingClientRect()
      const edge = 36
      const delta = y < rect.top + edge ? -Math.min(12, (rect.top + edge - y) / 3) : y > rect.bottom - edge ? Math.min(12, (y - rect.bottom + edge) / 3) : 0
      el.scrollTop += delta
      scrollFrame.current = requestAnimationFrame(autoScroll)
    } else scrollFrame.current = 0
  }
  const persist = async (next: string[], id: string) => {
    if (saving || next.every((v, i) => v === order[i])) return
    const before = saved
    setAgentPreference(next); setSaving(true); setError(undefined)
    try {
      await shell.setAgentOrder(next)
      setAnnouncement(tr('已调整 {0} 的位置', [name(id)]))
    } catch { setAgentPreference(before); setError(tr('顺序未能保存，请重试')) }
    finally { setSaving(false) }
  }
  const row = (id: string): ButtonHTMLAttributes<HTMLButtonElement> => ({
    draggable: ready && !saving,
    'aria-keyshortcuts': 'Alt+ArrowUp Alt+ArrowDown',
    'aria-describedby': 'agent-order-help',
    onClickCapture: e => { if (Date.now() < clickUntil.current) { e.preventDefault(); e.stopPropagation() } },
    onDragStart: e => {
      if (!ready || saving) { e.preventDefault(); return }
      active.current = id; setDragged(id); clickUntil.current = Date.now() + 500
      e.dataTransfer.effectAllowed = 'move'; e.dataTransfer.setData('text/plain', id)
    },
    onDragEnd: () => { clickUntil.current = Date.now() + 250; stop() },
    onKeyDown: e => {
      if (!e.altKey || (e.key !== 'ArrowUp' && e.key !== 'ArrowDown') || !ready || saving) return
      e.preventDefault(); e.stopPropagation()
      const down = e.key === 'ArrowDown', target = visible[visible.indexOf(id) + (down ? 1 : -1)]
      if (target) void persist(moveAgent(order, id, target, down), id)
    },
  })
  const group = (id: string): HTMLAttributes<HTMLDivElement> => ({
    onDragOver: e => {
      if (!active.current) return
      e.preventDefault(); e.dataTransfer.dropEffect = 'move'
      pointerY.current = e.clientY
      if (!scrollFrame.current) scrollFrame.current = requestAnimationFrame(autoScroll)
      const rect = e.currentTarget.getBoundingClientRect()
      setDrop({ id, after: e.clientY > rect.top + rect.height / 2 })
    },
    onDrop: e => {
      const from = active.current
      if (!from) return
      e.preventDefault(); e.stopPropagation()
      const rect = e.currentTarget.getBoundingClientRect()
      void persist(moveAgent(order, from, id, e.clientY > rect.top + rect.height / 2), from)
      clickUntil.current = Date.now() + 250; stop()
    },
  })
  return { visible: ready ? visible : [], row, group, dragged, drop, announcement, error, clearError: () => setError(undefined) }
}

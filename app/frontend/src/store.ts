// App state shared by every window: what the core knows (sessions, Agents), kept fresh by its
// events (zustand), plus the per-session views each screen loads.

import { useCallback, useEffect, useRef, useState } from 'react'
import { create } from 'zustand'
import { useShallow } from 'zustand/react/shallow'
import { api, onServerEvent, onStatus, shell } from './api/client'
import { fromDesktop, originBinding, projectName, threadCwd, threadTitle, turnsOf, type Turn } from './api/model'
import type { Binding, Connector, CoreStatus, ScanProgress, ThreadDetail, ThreadSummary } from './api/types'
import { coalescedRefresh } from './refresh'

export interface SessionItem {
  id: string
  title: string
  project: string
  cwd?: string
  updated: number | null
  harness: string
  /** Has a Claude Code session written from Claude Desktop's Code tab. */
  desktop: boolean
  bindings: Binding[]
}

export interface Hub {
  status: CoreStatus
  loaded: boolean
  error?: string
  connectors: Connector[]
  sessions: SessionItem[]
  /** Re-read everything (after an action, or an index pass that changed something). */
  reload: () => Promise<void>
  /** Bumps on every reload; screens re-fetch details when it changes. */
  version: number
  /** When the last reload succeeded (shown while the core cannot be reached). */
  loadedAt: number
  /** Set while a long index pass runs (the first one reads every session); the list fills meanwhile. */
  scanning?: ScanProgress
}

interface HubData {
  status: CoreStatus
  loaded: boolean
  error?: string
  threads: ThreadSummary[]
  connectors: Connector[]
  sessions: SessionItem[]
  version: number
  loadedAt: number
  scanning?: ScanProgress
}

/** The app's shared state (zustand): what the core knows, kept fresh by its events. */
export const useHubStore = create<HubData & { reload: () => Promise<void> }>()((set) => {
  const reload = coalescedRefresh(async () => {
      try {
        const [threads, connectors] = await Promise.all([api.threads(), api.connectors()])
        set(s => ({ threads, connectors, sessions: buildSessions(threads), error: undefined, loaded: true, loadedAt: Date.now(), version: s.version + 1 }))
        shell.mark('ready')
      } catch (e) {
        set({ error: (e as Error).message })
      }
  })
  return { status: { state: 'starting' }, loaded: false, threads: [], connectors: [], sessions: [], version: 0, loadedAt: 0, reload }
})

let started = false
/** Subscribe to the core once per window: its status, each index pass, progress and clashes. */
function startHub() {
  if (started) return
  started = true
  const { reload } = useHubStore.getState()
  const set = useHubStore.setState
  const ready = (s: CoreStatus) => { set({ status: s }); if (s.state === 'ready') void reload() }
  onStatus(ready)
  let lastReload = 0
  let quiet: ReturnType<typeof setTimeout> | undefined
  onServerEvent(e => {
    if (e.type === 'progress') {
      set({ scanning: e.done < e.total ? e : undefined })
      // A pass that stops reporting must not leave the line up.
      clearTimeout(quiet)
      quiet = setTimeout(() => set({ scanning: undefined }), 5000)
      if (e.added && Date.now() - lastReload > 1500) { lastReload = Date.now(); void reload() }
      return
    }
    if (e.type !== 'cycle') return
    clearTimeout(quiet)
    set({ scanning: undefined })
    if (e.report.changed || e.report.added) { lastReload = Date.now(); void reload() }
  })
  void shell.status().then(ready)
}

export function useHub(): Hub {
  useEffect(startHub, [])
  return useHubStore(useShallow(s => ({
    status: s.status, loaded: s.loaded, error: s.error, connectors: s.connectors, sessions: s.sessions, reload: s.reload,
    version: s.version, loadedAt: s.loadedAt, scanning: s.scanning,
  })))
}

export function buildSessions(threads: ThreadSummary[]): SessionItem[] {
  return threads
    .filter(t => t.bindings.length)
    .map(t => {
      const harness = originBinding(t.bindings)!.harness
      const cwd = threadCwd(t)
      return { id: t.id, title: threadTitle(t), cwd, project: projectName(cwd), updated: t.updated, harness, desktop: t.bindings.some(b => fromDesktop(b)), bindings: t.bindings }
    })
    .sort((a, b) => (b.updated ?? 0) - (a.updated ?? 0))
}

/** Search metadata across the index; ignore late results and release them when the query clears. */
export function useSessionSearch(query: string, scope: string, project: string | undefined, version: number) {
  const q = query.trim(), key = JSON.stringify([q, scope, project])
  const [result, setResult] = useState<{ key: string; sessions?: SessionItem[]; error?: string }>()
  useEffect(() => {
    if (!q) { setResult(undefined); return }
    let alive = true
    const timer = setTimeout(() => { void api.searchThreads(q, scope, project).then(rows => {
      if (alive) setResult({ key, sessions: buildSessions(rows) })
    }, error => { if (alive) setResult({ key, error: String(error) }) }) }, 180)
    return () => { alive = false; clearTimeout(timer) }
  }, [key, version])
  return result?.key === key ? result : undefined
}


// A native session has one current conversation path. Pages keep the displayed content stable
// during refresh; responses from a previous selection cannot replace the current selection.
export interface Conversation {
  selector: string
  detail: ThreadDetail
  turns: Turn[]
  totalTurns: number
  completeTurns: number
  hasEarlier: boolean
}
export function useConversation(id: string | undefined, reload: number) {
  const [state, setState] = useState<{ id?: string; conversation?: Conversation; error?: string }>({})
  const generation = useRef(0)
  const build = (selector: string, d: ThreadDetail): Conversation => {
    const turns = turnsOf(d.events, (d.offset ?? 0) > 0)
    const total = d.turns ?? turns.length
    const base = Math.max(0, total - turns.length)
    return { selector, detail: d, turns: turns.map(t => ({ ...t, index: t.index + base })), totalTurns: total,
      completeTurns: d.completeTurns, hasEarlier: (d.offset ?? 0) > 0 }
  }
  useEffect(() => {
    const current = ++generation.current
    if (!id) { setState({}); return }
    let alive = true
    const keep = state.id === id ? Math.max(30, state.conversation?.turns.length ?? 30) : 30
    ;(async () => {
      let d = await api.threadPage(id, Math.min(500, keep))
      let count = turnsOf(d.events).length
      while (alive && (d.offset ?? 0) > 0 && count < keep) {
        const page = await api.threadPage(d.head, Math.min(500, keep - count), d.events[0]?.id)
        if (!page.events.length) break
        d = { ...d, events: [...page.events, ...d.events], offset: page.offset }
        count = turnsOf(d.events).length
      }
      return d
    })().then(d => {
      if (alive) setState({ id, conversation: build(id, d) })
    }, e => { if (alive) setState(s => ({ ...(s.id === id ? s : {}), id, error: (e as Error).message })) })
    return () => { alive = false; if (generation.current === current) generation.current++ }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id, reload])
  const loadEarlier = useCallback(async (untilTurn?: number) => {
    const current = generation.current
    let c = state.conversation
    while (c?.hasEarlier && (untilTurn === undefined || (c.turns[0]?.index ?? 1) > untilTurn)) {
      const page = await api.threadPage(c.detail.head, 30, c.detail.events[0]?.id)
      if (generation.current !== current || !page.events.length || page.events[0]?.id === c.detail.events[0]?.id) break
      c = build(c.selector, { ...c.detail, events: [...page.events, ...c.detail.events], offset: page.offset })
      const loaded = c
      setState(s => s.id === id && s.conversation?.detail.head === loaded.detail.head ? { ...s, conversation: loaded } : s)
      if (untilTurn === undefined) break
    }
  }, [id, state.conversation])
  return { ...(state.id === id ? state : {}), loadEarlier }
}

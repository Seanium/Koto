// App state shared by every window: what the core knows (sessions, Agents), kept fresh by its
// events (zustand), plus the per-session views each screen loads.

import { useCallback, useEffect, useRef, useState } from 'react'
import { create } from 'zustand'
import { useShallow } from 'zustand/react/shallow'
import { api, onServerEvent, onStatus, shell } from './api/client'
import { fromDesktop, originBinding, projectName, threadCwd, threadTitle, turnsOf, type Turn } from './api/model'
import type { AgentProfile, Binding, Connector, CoreStatus, ScanProgress, ThreadDetail, ThreadSummary } from './api/types'
import { coalescedRefresh } from './refresh'
import { mergeConversationTail, prependConversationPage } from './conversationPages'
import { tr } from './i18n/core'

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
  /** Changes only when this session's indexed source changes. */
  revision?: string
}

export interface Hub {
  status: CoreStatus
  loaded: boolean
  error?: string
  connectors: Connector[]
  profiles: AgentProfile[]
  sessions: SessionItem[]
  /** Re-read everything (after an action, or an index pass that changed something). */
  reload: () => Promise<void>
  /** Bumps on every reload; screens re-fetch details when it changes. */
  version: number
  /** When the last reload succeeded (shown while the core cannot be reached). */
  loadedAt: number
  /** Set while a long index pass runs (the first one reads every session); the list fills meanwhile. */
  scanning?: ScanProgress
  /** The automatic first scan has not completed yet. */
  initializing?: boolean
}

interface HubData {
  status: CoreStatus
  loaded: boolean
  error?: string
  threads: ThreadSummary[]
  connectors: Connector[]
  profiles: AgentProfile[]
  sessions: SessionItem[]
  version: number
  loadedAt: number
  scanning?: ScanProgress
  initializing: boolean
}

/** The app's shared state (zustand): what the core knows, kept fresh by its events. */
export const useHubStore = create<HubData & { reload: () => Promise<void>; reloadSessions: () => Promise<void> }>()((set) => {
  const reloadSessions = coalescedRefresh(async () => {
    try {
      const threads = await api.threads()
      set(s => ({ threads, sessions: buildSessions(threads), error: undefined, loaded: true, loadedAt: Date.now(), version: s.version + 1 }))
      shell.mark('ready')
    } catch (e) { set({ error: (e as Error).message }) }
  })
  const reloadProfiles = coalescedRefresh(async () => {
    try {
      await Promise.all([
        api.connectors().then(connectors => set({ connectors })),
        api.profiles().then(profiles => set({ profiles })),
      ])
    } catch (e) { set({ error: (e as Error).message }) }
  })
  const reload = async () => { await Promise.all([reloadSessions(), reloadProfiles()]) }
  return { status: { state: 'starting' }, loaded: false, initializing: true, threads: [], connectors: [], profiles: [], sessions: [], version: 0, loadedAt: 0, reload, reloadSessions }
})

let started = false
/** Subscribe to the core once per window: its status, each index pass, progress and clashes. */
function startHub() {
  if (started) return
  started = true
  const { reload, reloadSessions } = useHubStore.getState()
  const set = useHubStore.setState
  let received = 0, checkedScan = false
  const ready = (s: CoreStatus) => {
    set({ status: s })
    if (s.state === 'starting') { checkedScan = false; set({ initializing: true, scanning: undefined }) }
    if (s.state !== 'ready') return
    void reload()
    if (checkedScan) return
    checkedScan = true
    const before = received
    void api.scanStatus().then(scan => {
      // A newer event wins over a late status response.
      if (received !== before) return
      set({ scanning: scan.progress, initializing: scan.scanning || !scan.report })
      if (scan.report) void reloadSessions()
    }).catch(e => set({ error: (e as Error).message }))
  }
  let statusEvents = 0
  onStatus(s => { statusEvents++; ready(s) })
  let lastReload = 0
  onServerEvent(e => {
    received++
    if (e.type === 'progress') {
      set({ scanning: e })
      if (Date.now() - lastReload > 750) { lastReload = Date.now(); void reloadSessions() }
      return
    }
    if (e.type !== 'cycle') return
    const initial = useHubStore.getState().initializing
    set({ scanning: undefined, initializing: false })
    if (initial || e.report.changed || e.report.added) { lastReload = Date.now(); void reload() }
  })
  void shell.status().then(s => { if (!statusEvents) ready(s) }).catch(e => set({ error: (e as Error).message }))
}

export function useHub(): Hub {
  useEffect(startHub, [])
  return useHubStore(useShallow(s => ({
    status: s.status, loaded: s.loaded, error: s.error, connectors: s.connectors, profiles: s.profiles, sessions: s.sessions, reload: s.reload,
    version: s.version, loadedAt: s.loadedAt, scanning: s.scanning, initializing: s.initializing,
  })))
}

export function buildSessions(threads: ThreadSummary[]): SessionItem[] {
  return threads
    .filter(t => t.bindings.length)
    .map(t => {
      const harness = originBinding(t.bindings)!.harness
      const cwd = threadCwd(t)
      return { id: t.id, title: threadTitle(t), cwd, project: projectName(cwd), updated: t.updated, harness, desktop: t.bindings.some(b => fromDesktop(b)), bindings: t.bindings,
        revision: t.revision }
    })
    .sort((a, b) => (b.updated ?? 0) - (a.updated ?? 0))
}

/** Search metadata across the index; ignore late results and release them when the query clears. */
export function useSessionSearch(query: string, scope: string, project: string | undefined, version: number, profile?: string, account?: string) {
  const q = query.trim(), key = JSON.stringify([q, scope, project, profile, account])
  const [result, setResult] = useState<{ key: string; sessions?: SessionItem[]; error?: string }>()
  useEffect(() => {
    if (!q && !profile && !account) { setResult(undefined); return }
    let alive = true
    const timer = setTimeout(() => { void api.searchThreads(q, scope, project, profile, account).then(rows => {
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
export function useConversation(id: string | undefined, revision?: string) {
  const [state, setState] = useState<{ id?: string; conversation?: Conversation; error?: string }>({})
  const [retry, setRetry] = useState(0)
  const reload = useCallback(() => setRetry(n => n + 1), [])
  const latest = useRef(state)
  latest.current = state
  const generation = useRef(0)
  const earlierRequest = useRef<AbortController | undefined>(undefined)
  const refreshRequest = useRef<{ controller: AbortController; queued: boolean } | undefined>(undefined)
  const publish = (next: typeof state) => { latest.current = next; setState(next) }
  const build = (selector: string, d: ThreadDetail): Conversation => {
    const turns = turnsOf(d.events, (d.offset ?? 0) > 0)
    const total = d.turns ?? turns.length
    const base = Math.max(0, total - turns.length)
    return { selector, detail: d, turns: turns.map(t => ({ ...t, index: t.index + base })), totalTurns: total,
      completeTurns: d.completeTurns, hasEarlier: (d.offset ?? 0) > 0 }
  }
  useEffect(() => {
    generation.current++
    publish(id ? { ...(latest.current.id === id ? latest.current : {}), id } : {})
    return () => {
      generation.current++
      refreshRequest.current?.controller.abort(); refreshRequest.current = undefined
      earlierRequest.current?.abort(); earlierRequest.current = undefined
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id])
  useEffect(() => {
    if (!id) return
    // A revision queues one refresh; it must not cancel an in-progress history page
    // or repeatedly restart a slow read while the Agent is streaming.
    if (refreshRequest.current) { refreshRequest.current.queued = true; return }
    const current = generation.current
    const request = { controller: new AbortController(), queued: false }
    refreshRequest.current = request
    const active = () => generation.current === current && !request.controller.signal.aborted
    void (async () => {
      try {
        do {
          request.queued = false
          let page = await api.threadPage(id, 30, undefined, request.controller.signal)
          if (!active()) return
          let previous = latest.current.conversation
          let joined = previous && mergeConversationTail(previous.detail, page)
          // More than one tail page may have arrived. Fill the gap by native event
          // boundaries before joining; never fabricate a connection across forks.
          while (previous && !joined && (page.offset ?? 0) > 0 && page.events.length) {
            const before = page.events[0].id
            const earlier = await api.threadPage(page.head, 30, before, request.controller.signal)
            if (!active()) return
            const extended = prependConversationPage(page, earlier, before)
            if (!extended || extended.events.length === page.events.length) throw Error(tr('读取失败'))
            page = extended
            previous = latest.current.conversation
            joined = previous && mergeConversationTail(previous.detail, page)
          }
          // Read latest again: a concurrently completed earlier page belongs in this join.
          previous = latest.current.conversation
          const detail = previous ? mergeConversationTail(previous.detail, page) ?? page : page
          publish({ id, conversation: build(id, detail) })
        } while (request.queued && active())
      } catch (e) {
        if (active()) publish({ ...latest.current, id, error: (e as Error).message })
      } finally { if (refreshRequest.current === request) refreshRequest.current = undefined }
    })()
    // Only changing the selected session cancels its reads, in the lifetime effect above.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id, revision, retry])
  const loadEarlier = useCallback(async (untilTurn?: number) => {
    const current = generation.current
    earlierRequest.current?.abort()
    const request = new AbortController()
    earlierRequest.current = request
    let c = latest.current.id === id ? latest.current.conversation : undefined
    try {
    while (c?.hasEarlier && (untilTurn === undefined || (c.turns[0]?.index ?? 1) > untilTurn)) {
      const before = c.detail.events[0]?.id
      if (!before) break
      const page = await api.threadPage(c.detail.head, 30, before, request.signal)
      if (request.signal.aborted || generation.current !== current) break
      const now = latest.current.id === id ? latest.current.conversation : undefined
      if (!now) break
      const detail = prependConversationPage(now.detail, page, before)
      if (!detail || detail.events.length === now.detail.events.length) break
      c = build(now.selector, detail)
      publish({ ...latest.current, conversation: c })
      if (untilTurn === undefined) break
    }
    } catch (e) {
      if (!request.signal.aborted && generation.current === current) publish({ ...latest.current, error: (e as Error).message })
    } finally { if (earlierRequest.current === request) earlierRequest.current = undefined }
  }, [id])
  return { ...(state.id === id ? state : {}), loadEarlier, reload }
}

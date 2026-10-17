import { tr } from '../i18n/core.ts'
import { shortcut } from '../ui/shortcut'
// Sidebar selects a product workspace; Agent and project filters narrow session copying.

import { Fragment, useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import { api, ApiError, onContextAction, onNavigate, onSettingsTab, shell, useOpeners } from '../api/client'
import { clockTime, fromDesktop, projectName, relativeTime, tildePath } from '../api/model'
import type { TrashEntry } from '../api/types'
import { buildSessions, useConversation, useSessionSearch, type Hub, type SessionItem } from '../store'
import { AgentDisc, agentName, Button, Icon, ListRow, Menu, OperationError, SearchField, Skeleton, Toast } from '../ui'
import { ContinueSheet, type CopySource } from './Continue'
import { Detail } from './Detail'
import { Analysis } from './Analysis'
import { isSettingsTab, Settings, type SettingsTab } from './Settings'
import { SplitPane } from '../ui/SplitPane'

type Mode = { kind: 'analysis' } | { kind: 'sessions' } | { kind: 'trash' } | { kind: 'settings'; tab: SettingsTab }

/** scope: 'all' or a harness. project: a project folder (or NO_PROJECT). Switching scope clears project. */
interface Filters { scope: string; project?: string }
const NO_FILTERS: Filters = { scope: 'all' }
const FILTERS_KEY = 'koto.filters'
/** Claude Code's two front ends, as scopes under it: the CLI, and Claude Desktop's Code tab (same files). */
const DESKTOP = 'claude-desktop'
const CLI = 'claude-cli'
const isCli = (s: SessionItem) => s.bindings.some(b => b.harness === 'claude-code' && !fromDesktop(b))
const NO_PROJECT = '__no_project__'
/** Sessions group by their project folder; two folders with the same name stay apart. */
const projectOf = (s: SessionItem) => s.cwd || NO_PROJECT
const projectLabel = (key: string) => key === NO_PROJECT ? tr("其他") : projectName(key)

function useFilters(): [Filters, (f: Filters) => void] {
  const [f, setF] = useState<Filters>(() => {
    try { return { ...NO_FILTERS, ...(JSON.parse(localStorage.getItem(FILTERS_KEY) ?? '{}') as Partial<Filters>) } } catch { return NO_FILTERS }
  })
  const set = (next: Filters) => { setF(next); try { localStorage.setItem(FILTERS_KEY, JSON.stringify(next)) } catch { /* not kept; fine */ } }
  return [f, set]
}

const desktopHint = () => tr("Claude Desktop 里可能仍显示，但打不开")

/** One short sentence for the core's trash / restore refusals. */
function trashError(e: unknown, fallback: string): string {
  if (!(e instanceof ApiError)) return fallback
  if (e.code === 'SESSION_OPEN') {
    const open = (e.body.open as { harness?: string }[] | undefined)?.map(o => o.harness).filter((h): h is string => !!h) ?? []
    return tr("{0} 里正开着这个会话，关掉后再试", [[...new Set(open)].map(agentName).join('、') || 'Agent'])
  }
  if (e.code === 'RESTORE_CONFLICT') return (e.body.missing as unknown[] | undefined)?.length ? tr("回收站里的文件不见了，无法恢复") : tr("原位置已有同名文件，无法恢复")
  if (e.code === 'TRASH_BLOCKED') return tr("有写到一半的内容，修复后才能移走")
  if (e.code === 'HUB_BUSY') return tr("正忙，稍后重试")
  if (e.code === 'NOT_FOUND') return tr("找不到这个会话")
  return fallback
}

interface ToastState { text: string; sub?: string; undo?: () => void }

export function MainWindow({ hub }: { hub: Hub }) {
  const [mode, setMode] = useState<Mode>({ kind: 'sessions' })
  const [filters, setFilters] = useFilters()
  const [query, setQuery] = useState('')
  const [selected, setSelected] = useState<string>()
  const [destination, setDestination] = useState<SessionItem>()
  // Only masks pending list/search refreshes after a successful move. Never persisted.
  const [removed, setRemoved] = useState<Map<string, boolean>>(() => new Map())
  const unmask = (id: string) => setRemoved(old => { const next = new Map(old); next.delete(id); return next })
  const [sheet, setSheet] = useState<{ title: string; source: CopySource }>()
  const [toast, setToast] = useState<ToastState>()
  const [refreshing, setRefreshing] = useState(false)
  const search = useRef<HTMLInputElement>(null)
  const trash = useTrash(hub)
  const inTrash = mode.kind === 'trash'
  const matches = useSessionSearch(mode.kind === 'sessions' ? query : '', filters.scope, filters.project, hub.version)

  // Sessions in the picked scope, and the projects among them (for 项目 ▾).
  // A newly copied old conversation can be outside the recent-list page. Keep just its summary
  // while selected, instead of fetching every indexed session to make the destination reachable.
  const sessions = useMemo(() => destination && !hub.sessions.some(s => s.id === destination.id) ? [destination, ...hub.sessions] : hub.sessions, [destination, hub.sessions])
  const inScope = useMemo(() => filters.scope === 'all' ? sessions
    : filters.scope === DESKTOP ? sessions.filter(s => s.desktop)
    : filters.scope === CLI ? sessions.filter(isCli)
    : sessions.filter(s => s.harness === filters.scope), [sessions, filters.scope])
  useEffect(() => { if (destination && selected !== destination.id) setDestination(undefined) }, [selected, destination])
  const projects = useMemo(() => {
    const n = new Map<string, number>()
    for (const s of inScope) n.set(projectOf(s), (n.get(projectOf(s)) ?? 0) + 1)
    return [...n].sort((a, b) => b[1] - a[1] || projectLabel(a[0]).localeCompare(projectLabel(b[0])))
  }, [inScope])
  const openers = useOpeners()
  // A project kept from an earlier run that has no sessions now would hide everything: drop it.
  useEffect(() => {
    if (hub.loaded && filters.project && !projects.some(([p]) => p === filters.project)) setFilters({ ...filters, project: undefined })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [hub.loaded, projects, filters.project])

  const visible = useMemo(() => {
    if (matches?.sessions) return matches.sessions.filter(s => !removed.has(s.id))
    const q = query.trim().toLowerCase()
    return inScope.filter(s =>
 !removed.has(s.id) && (!filters.project || projectOf(s) === filters.project)
      && (!q || s.id.toLowerCase().includes(q) || s.title.toLowerCase().includes(q) || s.project.toLowerCase().includes(q) || agentName(s.harness).toLowerCase().includes(q) || (s.desktop && 'claude desktop'.includes(q))
        || (!!s.cwd && (s.cwd.toLowerCase().includes(q) || tildePath(s.cwd, openers?.home).toLowerCase().includes(q)))))
  }, [inScope, filters, query, openers?.home, matches, removed])
  useEffect(() => {
    const done = [...removed].filter(([id, refreshed]) => refreshed && !hub.sessions.some(s => s.id === id) && !matches?.sessions?.some(s => s.id === id)).map(([id]) => id)
    if (done.length) setRemoved(old => { const next = new Map(old); for (const id of done) next.delete(id); return next })
  }, [removed, hub.sessions, matches])

  const trashVisible = useMemo(() => {
    const q = query.trim().toLowerCase()
    return trash.entries.filter(e => !q || (e.title ?? '').toLowerCase().includes(q) || e.items.some(i => agentName(i.harness).toLowerCase().includes(q)))
  }, [trash.entries, query])

  // Keep a selection while the list changes.
  useEffect(() => {
    if (!visible.length) return
    if (!selected || !visible.some(s => s.id === selected)) setSelected(visible[0].id)
  }, [visible, selected])

  const current = mode.kind === 'sessions' ? visible.find(s => s.id === selected) ?? visible[0] : undefined
  const { conversation, error: detailError, loadEarlier } = useConversation(current?.id, hub.version)

  const reloadAll = useCallback(() => { void hub.reload(); void trash.reload() }, [hub, trash])
  const refresh = async () => {
    if (refreshing) return
    setRefreshing(true)
    try {
      const report = await api.scan()
      await Promise.all([hub.reload(), trash.reload()])
      setToast(report.errors.length ? { text: tr("部分会话未能刷新"), sub: report.errors.map(e => e.error).join('；') } : { text: tr("会话数据已刷新") })
    } catch (e) { setToast({ text: tr("刷新失败，请重试"), sub: (e as Error).message }) }
    finally { setRefreshing(false) }
  }

  /** Move a session (or, with harness, only that Agent's copy) to 回收站; the toast undoes it. */
  /** Resolves to an error sentence when it could not (shown where it was asked), undefined when done. */
  const moveToTrash = useCallback(async (id: string, harness?: string): Promise<string | undefined> => {
    const item = hub.sessions.find(s => s.id === id)
    try {
      const entry = await api.trash(id, harness)
      setRemoved(old => new Map(old).set(id, false))
      setDestination(old => old?.id === id ? undefined : old)
      setSelected(old => old === id ? undefined : old)
      void hub.reload().then(() => setRemoved(old => old.has(id) ? new Map(old).set(id, true) : old))
      void trash.reload()
      const cc = entry.items.some(i => i.harness === 'claude-code')
      setToast({
        text: harness ? tr("已从 {0} 移到回收站", [agentName(harness)]) : tr("已移到回收站"),
        sub: cc ? desktopHint() : undefined,
        undo: () => { void api.restore(entry.trashId).then(() => { unmask(id); reloadAll() }, e => setToast({ text: trashError(e, tr("没能撤销")) })) },
      })
    } catch (e) {
      return trashError(e, tr("没能把「{0}」移到回收站", [item?.title ?? tr("这个会话")]))
    }
  }, [hub.sessions, reloadAll])
  // ⌘⌫ and the right-click menu ask through the same confirmation under the trash button.
  const [askTrash, setAskTrash] = useState(0)

  const openSession = async (id: string) => {
    const d = await api.threadPage(id, 1)
    const item = buildSessions([{ id, bindings: d.bindings, events: d.total ?? d.events.length, updated: d.events.at(-1)?.time ?? null }])[0]
    if (!item) throw Error(tr("新会话暂时不可用，请刷新后重试"))
    setDestination(item); setMode({ kind: 'sessions' }); setFilters({ scope: item.harness }); setQuery(''); setSelected(id)
  }

  const restore = async (e: TrashEntry) => {
    // No message: the session is back in the list, selected.
    try {
      await api.restore(e.trashId)
      unmask(e.thread)
      void trash.reload()
      await hub.reload() // the session must be in the list before it can stay selected
      await openSession(e.thread) // restored old sessions may be outside the recent-list page
    }
    catch (x) { setToast({ text: trashError(x, tr("没能恢复")) }) }
  }

  const openSettings = (tab?: string) => setMode(m => ({ kind: 'settings', tab: tab && isSettingsTab(tab) ? tab : m.kind === 'settings' ? m.tab : 'agents' }))

  // Shell → window: tray 「复制到…」, right-click menu, ⌘, and 设置 from the menu bar.
  useEffect(() => onNavigate(n => {
    if (n.thread) { setMode({ kind: 'sessions' }); setFilters(NO_FILTERS); setQuery(''); setSelected(n.thread) }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }), [])
  useEffect(() => onSettingsTab(openSettings), [])
  useEffect(() => onContextAction(a => {
    if (a.action === 'copy-id') void shell.copy(a.thread)
    if (a.action === 'trash') { setSelected(a.thread); setAskTrash(n => n + 1) }
  }), [moveToTrash])

  // ⌘K search · ↑↓ sessions · ⌘↵ 复制到… · ⌘⌫ move to 回收站 · ⌘, settings · Esc back
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (sheet || document.querySelector('.sheet, .menu')) return
      const mod = e.metaKey || e.ctrlKey
      const typing = e.target instanceof HTMLElement && e.target.closest('input, textarea, [contenteditable="true"]')
      if (mod && e.key === ',') { e.preventDefault(); openSettings() }
      else if (e.key === 'Escape' && mode.kind !== 'sessions') { e.preventDefault(); setMode({ kind: 'sessions' }) }
      else if (mode.kind === 'settings' || mode.kind === 'analysis') return
      else if (mod && e.key.toLowerCase() === 'r') { e.preventDefault(); document.getElementById('refresh-sessions')?.click() }
      else if (mod && e.key.toLowerCase() === 'k') { e.preventDefault(); search.current?.focus(); search.current?.select() }
      else if (mod && e.key === 'Enter' && current) { e.preventDefault(); document.getElementById('copy-main')?.click() }
      else if (mod && (e.key === 'Backspace' || e.key === 'Delete') && !typing && current) { e.preventDefault(); setAskTrash(n => n + 1) }
      else if ((e.key === 'ArrowDown' || e.key === 'ArrowUp') && !inTrash && !(e.target instanceof HTMLElement && e.target.closest('[role="listbox"]:not(#sessions), [role="switch"], [role="menu"]'))) {
        e.preventDefault()
        const i = visible.findIndex(s => s.id === selected)
        const next = visible[Math.max(0, Math.min(visible.length - 1, i + (e.key === 'ArrowDown' ? 1 : -1)))]
        if (next) { setSelected(next.id); document.getElementById(`s-${next.id}`)?.scrollIntoView({ block: 'nearest' }) }
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [visible, selected, current, sheet, inTrash, mode, moveToTrash])

  const issue = issueOf(hub, { session: id => { setMode({ kind: 'sessions' }); setFilters(NO_FILTERS); setSelected(id) }, settings: openSettings })

  const scopeTitle = inTrash ? tr("回收站") : filters.scope === 'all' ? tr("全部会话") : tr("{0} 会话", [filters.scope === DESKTOP ? 'Claude Desktop' : filters.scope === CLI ? tr("Claude Code · 命令行") : agentName(filters.scope)])
  const pickScope = (scope: string) => { setMode({ kind: 'sessions' }); setFilters({ ...filters, scope, project: scope === filters.scope ? filters.project : undefined }) }

  const down = hub.status.state === 'error' || !!hub.error
  return (
    <div className="col" style={{ height: '100%' }}>
    {down && hub.loaded && (
      <div className="banner banner-red topbanner no-drag" role="alert" title={hub.error ?? hub.status.error}>
        <span className="grow">{tr("连不上 koto 后台，列表停在 ")}{clockTime(hub.loadedAt)} {tr(" 的状态")}</span>
        <Button small onClick={() => { if (hub.status.state === 'error') void shell.restart(); else void hub.reload() }}>{tr("重新连接")}</Button>
      </div>
    )}
    <SplitPane name="sidebar" label={tr("侧边栏宽度")} initial={190} min={180} max={300} minRest={841}
      first={<Sidebar hub={hub} mode={mode} scope={filters.scope} pickScope={pickScope} openAnalysis={() => setMode({ kind: 'analysis' })} openCopy={() => setMode({ kind: 'sessions' })}
        openTrash={() => setMode({ kind: 'trash' })} openSettings={() => openSettings()} issue={issue} />}
      second={mode.kind === 'analysis' ? <Analysis hub={hub} /> : mode.kind === 'settings' ? (
        <Settings hub={hub} tab={mode.tab} setTab={tab => setMode({ kind: 'settings', tab })} />
      ) : <SplitPane name="sessions" label={tr("会话列表宽度")} initial={300} min={240} max={460} minRest={600} first={
      <section className="col grow session-list-pane" style={{ background: 'var(--bg-list)' }}>
        <div className="drag col g2" style={{ padding: '14px 12px 8px' }}>
          <div className="drag row g1" style={{ minHeight: 28 }}>
            <span className="t-head grow ellipsis" style={{ paddingLeft: 4, minWidth: '5.5em' }} title={scopeTitle}>{scopeTitle}</span>
            <Button id="refresh-sessions" variant="ghost" icon disabled={refreshing || hub.status.state !== 'ready'} onClick={() => void refresh()} aria-label={tr("刷新会话数据")} aria-busy={refreshing}
              title={shortcut(tr("刷新会话数据（⌘R）。后台每 3 秒自动检查变化，不会自动复制。"))}>{Icon.refresh}</Button>
          </div>
          <SearchField ref={search} value={query} onChange={setQuery} placeholder={inTrash ? tr("搜索回收站") : tr("搜索会话")} />
          {!inTrash && <div className="row g1 list-filters">
            {!inTrash && projects.length > 1 && (
              <Menu trigger="ghost" align="under" pressed={!!filters.project} label={tr("只看一个项目里的会话")} searchable={tr("搜索项目")}
                items={[{ label: tr("全部"), checked: !filters.project, onClick: () => setFilters({ ...filters, project: undefined }) },
                  ...projects.map(([p]) => ({ key: p, label: projectLabel(p), sub: p === NO_PROJECT ? tr("没有记录项目文件夹") : tildePath(p, openers?.home), checked: filters.project === p, onClick: () => setFilters({ ...filters, project: p }) }))]}>
                {tr(" 项目：")}{filters.project ? projectLabel(filters.project) : tr("全部")} {Icon.chevron}
              </Menu>
            )}
          </div>}
        </div>
        {!inTrash && hub.scanning && (
          <div className="scanline" title={tr("第一次打开要把各 Agent 的会话读一遍；读到的会话已经可以点开")}>
            {tr(" 正在读取 ")}{agentName(hub.scanning.harness)} {tr(" 的会话 · ")}{hub.scanning.done.toLocaleString()} / {hub.scanning.total.toLocaleString()}
            <span className="scanline-bar"><span style={{ width: `${Math.round(100 * hub.scanning.done / Math.max(1, hub.scanning.total))}%` }} /></span>
          </div>
        )}
        {matches?.error && <OperationError message={tr("读取失败")} detail={matches.error} />}
        {inTrash
          ? <TrashList trash={trash} entries={trashVisible} query={query} restore={restore} />
          : <SessionList hub={hub} items={visible} selected={selected} onSelect={setSelected} query={query} filters={filters}
              clear={() => { setFilters({ ...filters, project: undefined }); setQuery('') }} openSettings={() => openSettings('agents')} />}
      </section>} second={
      <main className="col grow" style={{ background: 'var(--bg-window)', minWidth: 0, minHeight: 0 }}>
        {current ? (
          <Detail key={current.id} hub={hub} item={current} conversation={conversation} detailError={detailError} loadEarlier={loadEarlier}
            onCopy={source => setSheet({ title: current.title, source })}
            onTrash={() => moveToTrash(current.id)} askTrash={askTrash} />
        ) : (
          <div className="drag col grow" style={{ alignItems: 'center', justifyContent: 'center', padding: 32 }}>
            <span className="t-meta">{inTrash ? tr("回收站里的会话不会被删除，点「恢复」放回原处") : ''}</span>
          </div>
        )}
      </main>} /> } />
      {sheet && (
        <ContinueSheet onClose={() => setSheet(undefined)} hub={hub} title={sheet.title}
          source={sheet.source} onDone={() => void hub.reload()} onOpen={async id => {
            await openSession(id); setSheet(undefined)
          }} />
      )}
      {toast && <Toast text={toast.text} sub={toast.sub} action={toast.undo ? tr("撤销") : undefined} onAction={toast.undo} onDone={() => setToast(undefined)} />}
    </div>
  )
}

// ---------------------------------------------------------------- sidebar: where to look

function Sidebar({ hub, mode, scope, pickScope, openTrash, openSettings, openAnalysis, openCopy, issue }: {
  hub: Hub; mode: Mode; scope: string; pickScope: (s: string) => void
  openTrash: () => void; openSettings: () => void; openAnalysis: () => void; openCopy: () => void; issue?: Issue
}) {
  const harnesses = [...new Set(hub.connectors.filter(c => c.enabled).map(c => c.harness))]
  const at = (s: string) => mode.kind === 'sessions' && scope === s
  const [expanded, setExpanded] = useRemembered('koto.sidebar.allOpen', true)
  const nav = useRef<HTMLElement>(null)
  useEffect(() => { if (mode.kind === 'sessions') setExpanded(true) }, [scope, mode.kind])
  useEffect(() => { const frame = requestAnimationFrame(() => nav.current?.querySelector('[aria-selected="true"]')?.scrollIntoView({ block: 'nearest' })); return () => cancelAnimationFrame(frame) }, [scope, expanded])
  return (
    <nav ref={nav} className="col grow drag app-sidebar" style={{ background: 'var(--bg-sidebar)' }}>
      <div style={{ height: 52, flex: 'none' }} />
      <div className="col no-drag scroll grow" style={{ padding: '0 8px', gap: 1 }} role="listbox" aria-label={tr("范围")}>
        <ListRow compact selected={mode.kind === 'sessions' && !expanded} aria-expanded={expanded && mode.kind === 'sessions'}
          onClick={() => { if (mode.kind === 'sessions') setExpanded(!expanded); else { openCopy(); setExpanded(true) } }}
          onKeyDown={e => { if (e.key === 'ArrowLeft' && expanded) { e.preventDefault(); setExpanded(false) } if (e.key === 'ArrowRight' && !expanded) { e.preventDefault(); setExpanded(true) } }}>
          <span className="row" style={{ width: 16, justifyContent: 'center', color: 'var(--ghost-ink)' }}>{Icon.copy}</span>
          <span className="grow ellipsis">{tr("会话复制")}</span>
          <span className="side-caret" data-open={expanded && mode.kind === 'sessions' || undefined} aria-hidden="true">{Icon.disclosure}</span>
        </ListRow>
        {mode.kind === 'sessions' && expanded && <div className="side-children col">
        <SideItem icon={Icon.all} label={tr("全部会话")} selected={at('all')} onClick={() => pickScope('all')} />
        {harnesses.map(h => (
          <Fragment key={h}>
            <SideItem icon={<AgentDisc harness={h} size={16} title="" />} label={agentName(h)} selected={at(h)} onClick={() => pickScope(h)} title={agentName(h)} />
            {h === 'claude-code' && hub.sessions.some(s => s.desktop) && <>
              <SideItem indent icon={Icon.terminal} label={tr("命令行")} selected={at(CLI)} onClick={() => pickScope(CLI)}
                title={tr("在终端里用 claude 命令开的会话")} />
              <SideItem indent icon={Icon.desktop} label="Desktop" selected={at(DESKTOP)} onClick={() => pickScope(DESKTOP)}
                title={tr("在 Claude Desktop 的 Code 标签里开的会话（和命令行用的是同一批文件，两边都能接着聊）")} />
            </>}
          </Fragment>
        ))}
        </div>}
        <SideItem icon={Icon.branch} label={tr("会话对比")} selected={mode.kind === 'analysis'} onClick={openAnalysis} />
      </div>
      {issue && (
        <button type="button" className={`issue no-drag ${issue.tone === 'bad' ? 't-red' : issue.tone === 'warn' ? 't-amber' : 't-3'}`} onClick={issue.run} disabled={!issue.run} title={issue.title}>
          <span className="grow ellipsis">{issue.text}</span>{issue.action && <span className="t-medium">{issue.action}</span>}
        </button>
      )}
      <div className="col no-drag" style={{ padding: '6px 8px 8px', gap: 1, borderTop: issue ? undefined : '1px solid var(--divider)' }}>
        <SideItem icon={Icon.trash} label={tr("回收站")} selected={mode.kind === 'trash'} onClick={openTrash} />
        <SideItem icon={Icon.gear} label={tr("设置")} selected={mode.kind === 'settings'} onClick={openSettings} title={shortcut('⌘,')} />
      </div>
    </nav>
  )
}

/** A boolean kept in localStorage (a per-viewer convenience: lost storage just means the default). */
function useRemembered(key: string, initial: boolean): [boolean, (v: boolean) => void] {
  const [v, setV] = useState(() => { try { const x = localStorage.getItem(key); return x === null ? initial : x === '1' } catch { return initial } })
  return [v, (next: boolean) => { setV(next); try { localStorage.setItem(key, next ? '1' : '0') } catch { /* fine */ } }]
}

/** A sidebar row: icon and label. */
function SideItem({ icon, label, selected, onClick, title, indent }: {
  icon: React.ReactNode; label: string; selected: boolean; onClick: () => void; title?: string; indent?: boolean | 2
}) {
  return (
    <ListRow compact selected={selected} onClick={onClick} title={title} style={{ paddingLeft: indent === 2 ? 58 : indent ? 34 : undefined }}>
      <span className="row" style={{ width: 16, justifyContent: 'center', color: 'var(--ghost-ink)' }}>{icon}</span>
      <span className="grow ellipsis">{label}</span>
    </ListRow>
  )
}

// ---------------------------------------------------------------- 回收站

interface Trash { loaded: boolean; error: boolean; entries: TrashEntry[]; reload: () => Promise<void> }

function useTrash(hub: Hub): Trash {
  const [entries, setEntries] = useState<TrashEntry[]>([])
  const [loaded, setLoaded] = useState(false)
  const [error, setError] = useState(false)
  const reload = useCallback(async () => {
    try { setEntries(await api.trashList()); setError(false) } catch { setError(true) } finally { setLoaded(true) }
  }, [])
  const ready = hub.status.state === 'ready'
  useEffect(() => { if (ready) void reload() }, [reload, ready, hub.version])
  return useMemo(() => ({ loaded, error, entries, reload }), [loaded, error, entries, reload])
}

function TrashList({ trash, entries, query, restore }: { trash: Trash; entries: TrashEntry[]; query: string; restore: (e: TrashEntry) => Promise<void> }) {
  const [busy, setBusy] = useState<string>()
  if (!trash.loaded) return <ListSkeleton />
  if (trash.error) return <div className="t-meta grow" style={{ padding: 24 }}>{tr("读不到回收站 ")}<Button variant="link" onClick={() => void trash.reload()}>{tr("重试")}</Button></div>
  if (!entries.length) return query.trim() ? <Empty title={tr("没有找到“{0}”", [query.trim()])} why={tr("试试别的关键词")} /> : <Empty title={tr("回收站是空的")} why={tr("移到这里的会话随时可以恢复")} />
  return (
    <div className="col scroll grow" role="list" aria-label={tr("回收站")} style={{ padding: '0 8px 8px', gap: 2 }}>
      {entries.map(e => {
        const agents = [...new Set(e.items.map(i => i.harness))]
        return (
          <div key={e.trashId} role="listitem" className="lrow" style={{ alignItems: 'flex-start', cursor: 'default' }}>
            <span className="col grow g1" style={{ padding: '2px 0', minWidth: 0 }}>
              <span className="ellipsis t-medium">{e.title || tr("未命名会话")}</span>
              <span className="t-meta ellipsis">
                {e.harness ? tr("只移走了 {0} 里的那份", [agentName(e.harness)]) : agents.map(agentName).join('、')} · {relativeTime(e.trashedAt)}{tr("移入 ")}</span>
            </span>
            <Button small disabled={busy === e.trashId} onClick={async () => { setBusy(e.trashId); await restore(e); setBusy(undefined) }}>
              {busy === e.trashId ? tr("正在恢复…") : tr("恢复")}
            </Button>
          </div>
        )
      })}
    </div>
  )
}

// ---------------------------------------------------------------- problems (shown only when something is wrong)

export interface Issue { tone: 'warn' | 'bad' | 'idle'; text: string; title?: string; action?: string; run?: () => void }

/** What is wrong right now, as one line with one action; undefined when all is well. */
export function issueOf(hub: Hub, go: { session: (id: string) => void; settings: (tab: string) => void }): Issue | undefined {
  if (hub.status.state === 'starting') return { tone: 'idle', text: tr("正在启动…") }
  if (hub.status.state === 'error' && hub.status.code === 'serve-running') return { tone: 'bad', text: tr("另一个 koto 正在使用这份数据"), title: hub.status.error, action: tr("重试"), run: () => void shell.restart() }
  if (hub.status.state === 'error') return { tone: 'bad', text: tr("后台服务没有运行"), title: hub.status.error, action: tr("重试"), run: () => void shell.restart() }
  if (hub.error) return { tone: 'warn', text: tr("连不上后台服务"), title: hub.error, action: tr("重试"), run: () => void hub.reload() }
  const failed = hub.connectors.filter(c => c.enabled && c.status === 'failed')
  if (failed.length) return { tone: 'warn', text: tr("{0} 读取失败", [failed.map(c => agentName(c.harness)).join('、')]), title: failed.map(c => c.error).filter(Boolean).join('\n'), action: tr("查看"), run: () => go.settings('agents') }
  return undefined
}

// ---------------------------------------------------------------- list

/** Empty: a title, one line why, one thing to do; centred. */
function Empty({ title, why, action }: { title: string; why?: string; action?: React.ReactNode }) {
  return (
    <div className="col grow g2" style={{ alignItems: 'center', justifyContent: 'center', padding: 24, textAlign: 'center' }}>
      <span className="t-medium">{title}</span>
      {why && <span className="t-meta">{why}</span>}
      {action && <div style={{ paddingTop: 6 }}>{action}</div>}
    </div>
  )
}

function ListSkeleton() {
  return (
    <div className="col g2 grow" style={{ padding: '4px 8px' }}>
      {[0, 1, 2, 3, 4].map(i => (
        <div key={i} className="col g2" style={{ padding: '10px 12px' }}><Skeleton w="70%" h={13} /><Skeleton w="40%" h={11} /></div>
      ))}
    </div>
  )
}

function SessionList({ hub, items, selected, onSelect, query, filters, clear, openSettings }: {
  hub: Hub; items: SessionItem[]; selected?: string; onSelect: (id: string) => void; query: string; filters: Filters
  clear: () => void; openSettings: () => void
}) {
  const [rescanning, setRescanning] = useState(false)
  const previous = useRef<string | undefined>(undefined), locate = useRef<string | undefined>(undefined)
  useLayoutEffect(() => {
    if (previous.current !== selected) { previous.current = selected; locate.current = selected }
    if (locate.current) document.getElementById(`s-${locate.current}`)?.scrollIntoView({ block: 'nearest' })
  }, [selected, items])
  const stopLocating = () => { locate.current = undefined }
  if (!hub.loaded) return <ListSkeleton />
  if (!items.length) {
    if (hub.scanning) return <Empty title={tr("正在读取会话")} why={tr("读到的会话会陆续出现在这里")} />
    if (query.trim()) return <Empty title={tr("没有找到“{0}”", [query.trim()])} why={tr("试试别的关键词")} />
    if (filters.project) {
      const what = [filters.project && tr("项目：{0}", [projectLabel(filters.project)])].filter(Boolean).join(' · ')
      return <Empty title={tr("没有符合条件的会话")} why={what} action={<Button small onClick={clear}>{tr("清除筛选")}</Button>} />
    }
    if (filters.scope !== 'all') return <Empty title={tr("暂无会话")} />
    return (
      <Empty title={tr("还没找到会话")} why={tr("这台电脑上还没有 Agent 的会话")}
        action={<div className="col g2" style={{ alignItems: 'center' }}>
          <Button small disabled={rescanning} onClick={() => { setRescanning(true); void api.scan().finally(() => { setRescanning(false); void hub.reload() }) }}>{rescanning ? tr("正在扫描…") : tr("重新扫描")}</Button>
          <button type="button" className="btn-link no-drag" onClick={openSettings}>{tr("设置 › Agent")}</button>
        </div>} />
    )
  }
  return (
    <div id="sessions" className="col scroll grow" role="listbox" aria-label={tr("会话")} onWheel={stopLocating} onPointerDown={stopLocating} onKeyDown={stopLocating} style={{ padding: '0 8px 8px', gap: 2 }}>
      {items.map(s => (
        <ListRow key={s.id} id={`s-${s.id}`} selected={s.id === selected} onClick={() => onSelect(s.id)}
          style={{ alignItems: 'flex-start', '--custom-contextmenu': 'session', '--custom-contextmenu-data': JSON.stringify(s.id), '--stack-bg': s.id === selected ? 'var(--bg-selected)' : 'var(--bg-list)' } as React.CSSProperties}>
          <span className="col grow g1" style={{ padding: '2px 0', minWidth: 0 }}>
            <span className="row g2">
              {filters.scope === 'all' && <AgentDisc harness={s.harness} size={16} />}<span className="grow ellipsis t-medium" title={s.title}>{s.title}</span>
              <span className="t-meta" style={{ flex: 'none' }}>{relativeTime(s.updated)}</span>
            </span>
            <span className="row g2">
              <span className="grow row g1 t-meta" style={{ minWidth: 0 }}>
                <span className="ellipsis">{s.project || '—'}</span>
                {s.desktop && filters.scope !== DESKTOP && <span className="tag" title={tr("在 Claude Desktop 的 Code 标签里开的会话，和 Claude Code 用的是同一批文件")}>Desktop</span>}
              </span>
            </span>
          </span>
        </ListRow>
      ))}
    </div>
  )
}

import { tr } from '../i18n/core.ts'
import { shortcut } from '../ui/shortcut'
// Sidebar selects a product workspace; Agent and project filters narrow session copying.

import { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import { api, onContextAction, onNavigate, onSettingsTab, shell, useOpeners } from '../api/client'
import { clockTime, originBinding, productName, productScopeName, projectName, relativeTime, tildePath } from '../api/model'
import { accountLabel, accountName, storageAccount, matchesIdentity, matchesProduct, productKey, UNKNOWN_ACCOUNT } from '../api/identity'
import { buildSessions, useConversation, useSessionSearch, type Hub, type SessionItem } from '../store'
import { AgentDisc, agentName, Button, Icon, ListRow, Menu, OperationError, SearchField, Skeleton, Toast } from '../ui'
import { ContinueSheet, type CopySource } from './Continue'
import { Detail } from './Detail'
import { Analysis } from './Analysis'
import { isSettingsTab, Settings, type SettingsTab } from './Settings'
import { SplitPane } from '../ui/SplitPane'
import { useAgentOrder } from '../ui/useAgentOrder'
import { destinationFilters, visibleSelection } from '../sessionNavigation'

type Mode = { kind: 'analysis' } | { kind: 'sessions' } | { kind: 'settings'; tab: SettingsTab }

/** scope: 'all' or a product key. Switching product clears its narrower filters. */
interface Filters { scope: string; project?: string; profile?: string; account?: string }
const NO_FILTERS: Filters = { scope: 'all' }
const FILTERS_KEY = 'koto.filters'
/** Claude Desktop's Code tab has a separate product scope even when transcripts are shared. */
const DESKTOP = 'claude-code/desktop'
const NO_PROJECT = '__no_project__'
/** Sessions group by their project folder; two folders with the same name stay apart. */
const projectOf = (s: SessionItem) => s.cwd || NO_PROJECT
const projectLabel = (key: string) => key === NO_PROJECT ? tr("其他") : projectName(key)

function useFilters(): [Filters, (f: Filters) => void] {
  const [f, setF] = useState<Filters>(() => {
    try {
      const saved = { ...NO_FILTERS, ...(JSON.parse(localStorage.getItem(FILTERS_KEY) ?? '{}') as Partial<Filters>) }
      return saved.scope === 'all' || saved.scope.includes('/') ? saved : NO_FILTERS
    } catch { return NO_FILTERS }
  })
  const set = (next: Filters) => { setF(next); try { localStorage.setItem(FILTERS_KEY, JSON.stringify(next)) } catch { /* not kept; fine */ } }
  return [f, set]
}

interface ToastState { text: string; sub?: string }

export function MainWindow({ hub }: { hub: Hub }) {
  const [mode, setMode] = useState<Mode>({ kind: 'sessions' })
  const [filters, setFilters] = useFilters()
  const [query, setQuery] = useState('')
  const [selected, setSelected] = useState<string>()
  const [destination, setDestination] = useState<SessionItem>()
  const navigation = useRef<AbortController | undefined>(undefined)
  useEffect(() => () => { navigation.current?.abort() }, [mode, filters, query, selected])
  const [sheet, setSheet] = useState<{ title: string; source: CopySource }>()
  const [toast, setToast] = useState<ToastState>()
  const [refreshing, setRefreshing] = useState(false)
  const search = useRef<HTMLInputElement>(null)
  const matches = useSessionSearch(mode.kind === 'sessions' ? query : '', filters.scope, filters.project, hub.version, mode.kind === 'sessions' ? filters.profile : undefined, mode.kind === 'sessions' ? filters.account : undefined)

  // Sessions in the picked scope, and the projects among them (for 项目 ▾).
  // A newly copied old conversation can be outside the recent-list page. Keep just its summary
  // while selected, instead of fetching every indexed session to make the destination reachable.
  const sessions = useMemo(() => destination && !hub.sessions.some(s => s.id === destination.id) ? [destination, ...hub.sessions] : hub.sessions, [destination, hub.sessions])
  const inScope = useMemo(() => sessions.filter(s => matchesProduct(s.bindings, filters.scope)), [sessions, filters.scope])
  useEffect(() => { if (destination && selected !== destination.id) setDestination(undefined) }, [selected, destination])
  const projects = useMemo(() => {
    const n = new Map<string, number>()
    for (const s of inScope) n.set(projectOf(s), (n.get(projectOf(s)) ?? 0) + 1)
    return [...n].sort((a, b) => b[1] - a[1] || projectLabel(a[0]).localeCompare(projectLabel(b[0])))
  }, [inScope])
  const openers = useOpeners()
  const profiles = hub.profiles.filter(p => filters.scope === 'all' || p.harness === filters.scope || productKey(p) === filters.scope)
  const currentProfile = profiles.find(p => p.id === filters.profile)
  const accounts = [...new Map(profiles.filter(p => !filters.profile || p.id === filters.profile).flatMap(p => p.accounts.filter(a => a.kind === 'account').map(a => ({ ...a, product: productKey(p) }))).map(a => [`${a.product}\0${a.id}`, a])).values()]
  // A project kept from an earlier run that has no sessions now would hide everything: drop it.
  useEffect(() => {
    if (hub.loaded && filters.project && !projects.some(([p]) => p === filters.project)) setFilters({ ...filters, project: undefined })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [hub.loaded, projects, filters.project])

  const visible = useMemo(() => {
    const q = query.trim().toLowerCase()
    const rows = matches?.sessions ?? inScope
    const serverMatches = matches?.sessions && new Set(matches.sessions.map(s => s.id))
    const candidates = destination && selected === destination.id && !rows.some(s => s.id === destination.id) ? [destination, ...rows] : rows
    return candidates.filter(s =>
      matchesProduct(s.bindings, filters.scope) && (!filters.project || projectOf(s) === filters.project) && matchesIdentity(s.bindings, filters.profile, filters.account)
      && (serverMatches?.has(s.id) || !q || s.id.toLowerCase().includes(q) || s.title.toLowerCase().includes(q) || s.project.toLowerCase().includes(q) || agentName(s.harness).toLowerCase().includes(q) || (s.desktop && 'claude desktop'.includes(q))
        || (!!s.cwd && (s.cwd.toLowerCase().includes(q) || tildePath(s.cwd, openers?.home).toLowerCase().includes(q)))))
  }, [inScope, filters, query, openers?.home, matches, destination, selected])
  // Keep a selection while the list changes.
  useEffect(() => {
    const next = visibleSelection(visible, selected)?.id
    if (selected !== next) setSelected(next)
  }, [visible, selected])

  const current = mode.kind === 'sessions' ? visibleSelection(visible, selected) : undefined
  const { conversation, error: detailError, loadEarlier, reload: reloadDetail } = useConversation(current?.id, current?.revision)

  const refresh = async () => {
    if (refreshing) return
    setRefreshing(true)
    try {
      const report = await api.scan()
      await hub.reload()
      setToast(report.errors.length ? { text: tr("部分会话未能刷新"), sub: report.errors.map(e => e.error).join('；') } : { text: tr("会话数据已刷新") })
    } catch (e) { setToast({ text: tr("刷新失败，请重试"), sub: (e as Error).message }) }
    finally { setRefreshing(false) }
  }

  const openSession = async (id: string) => {
    navigation.current?.abort()
    const request = new AbortController()
    navigation.current = request
    let d
    try { d = await api.threadPage(id, 1, undefined, request.signal) }
    catch (e) {
      if (request.signal.aborted) return
      if (navigation.current === request) navigation.current = undefined
      throw e
    }
    if (request.signal.aborted) return
    const item = buildSessions([{ id, bindings: d.bindings, events: d.total ?? d.events.length, updated: d.events.at(-1)?.time ?? null }])[0]
    if (!item) throw Error(tr("新会话暂时不可用，请刷新后重试"))
    navigation.current = undefined
    setDestination(item); setMode({ kind: 'sessions' }); setFilters(destinationFilters(originBinding(item.bindings), item.harness)); setQuery(''); setSelected(item.id)
  }

  const openSettings = (tab?: string) => setMode({ kind: 'settings', tab: tab && isSettingsTab(tab) ? tab : 'general' })

  // Shell → window: tray 「复制到…」, right-click menu, ⌘, and 设置 from the menu bar.
  useEffect(() => {
    const offNavigate = onNavigate(n => {
      if (n.thread) void openSession(n.thread).catch(e => setToast({ text: tr("读取失败"), sub: String(e) }))
    })
    const offSettings = onSettingsTab(openSettings)
    // Native menu/tray requests can be replayed only after both listeners exist.
    shell.mark('ui-ready')
    return () => { offNavigate(); offSettings() }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])
  useEffect(() => onContextAction(a => {
    if (a.action === 'copy-id') void shell.copy(a.thread)
  }), [])

  // ⌘K search · ↑↓ sessions · ⌘↵ 复制到… · ⌘, settings · Esc back
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (sheet || document.querySelector('.sheet, .menu')) return
      const mod = e.metaKey || e.ctrlKey
      if (mod && e.key === ',') { e.preventDefault(); openSettings() }
      else if (e.key === 'Escape' && mode.kind !== 'sessions') { e.preventDefault(); setMode({ kind: 'sessions' }) }
      else if (mode.kind === 'settings' || mode.kind === 'analysis') return
      else if (mod && e.key.toLowerCase() === 'r') { e.preventDefault(); document.getElementById('refresh-sessions')?.click() }
      else if (mod && e.key.toLowerCase() === 'k') { e.preventDefault(); search.current?.focus(); search.current?.select() }
      else if (mod && e.key === 'Enter' && current) { e.preventDefault(); document.getElementById('copy-main')?.click() }
      else if ((e.key === 'ArrowDown' || e.key === 'ArrowUp') && !(e.target instanceof HTMLElement && e.target.closest('[role="listbox"]:not(#sessions), [role="switch"], [role="menu"]'))) {
        e.preventDefault()
        const i = visible.findIndex(s => s.id === selected)
        const next = visible[Math.max(0, Math.min(visible.length - 1, i + (e.key === 'ArrowDown' ? 1 : -1)))]
        if (next) { setSelected(next.id); document.getElementById(`s-${next.id}`)?.scrollIntoView({ block: 'nearest' }) }
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [visible, selected, current, sheet, mode])

  const issue = issueOf(hub, { session: id => { setMode({ kind: 'sessions' }); setFilters(NO_FILTERS); setSelected(id) }, settings: openSettings })

  const scopeTitle = filters.scope === 'all' ? tr("全部会话") : tr("{0} 会话", [productScopeName(filters.scope)])
  const pickScope = (scope: string) => { setMode({ kind: 'sessions' }); setFilters(scope === filters.scope ? filters : { scope }) }

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
        openSettings={() => openSettings()} issue={issue} />}
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
          <SearchField ref={search} value={query} onChange={setQuery} placeholder={tr("搜索会话")} />
          <div className="row g1 list-filters">
            {profiles.length > 1 && <Menu trigger="ghost" align="under" pressed={!!filters.profile} label={tr('筛选会话目录')} searchable={tr('搜索目录')}
              items={[{ label: tr('全部会话目录'), checked: !filters.profile, onClick: () => setFilters({ ...filters, profile: undefined, account: undefined }) },
                ...profiles.map(p => ({ key: p.id, label: p.label || agentName(p.harness), sub: tildePath(p.root, openers?.home), checked: filters.profile === p.id, onClick: () => setFilters({ ...filters, profile: p.id, account: undefined }) }))]}>
              <span className="ellipsis">{currentProfile?.label || tr('会话目录')}</span> {Icon.chevron}
            </Menu>}
            {(accounts.length > 0 || filters.account) && <Menu trigger="ghost" align="under" pressed={!!filters.account} label={tr('筛选账号')} searchable={tr('搜索账号')}
              items={[{ label: tr('全部账号'), checked: !filters.account, onClick: () => setFilters({ ...filters, account: undefined }) },
                ...accounts.map(a => ({ key: `${a.product}:${a.id}`, label: accountName(a), sub: filters.scope === 'all' ? productScopeName(a.product) : a.scope === 'creator' ? tr('创建账号') : undefined, checked: filters.account === a.id, onClick: () => setFilters({ ...filters, scope: a.product, account: a.id }) })),
                { label: tr('账号未知'), checked: filters.account === UNKNOWN_ACCOUNT, onClick: () => setFilters({ ...filters, account: UNKNOWN_ACCOUNT }) }]}>
              <span className="ellipsis">{filters.account === UNKNOWN_ACCOUNT ? tr('账号未知') : accountName(accounts.find(a => a.id === filters.account) ?? { id: tr('账号'), kind: 'account', scope: 'storage', evidence: '' })}</span> {Icon.chevron}
            </Menu>}
            {projects.length > 1 && (
              <Menu trigger="ghost" align="under" pressed={!!filters.project} label={tr("只看一个项目里的会话")} searchable={tr("搜索项目")}
                items={[{ label: tr("全部"), checked: !filters.project, onClick: () => setFilters({ ...filters, project: undefined }) },
                  ...projects.map(([p]) => ({ key: p, label: projectLabel(p), sub: p === NO_PROJECT ? tr("没有记录项目目录") : tildePath(p, openers?.home), checked: filters.project === p, onClick: () => setFilters({ ...filters, project: p }) }))]}>
                <span className="ellipsis">{tr('项目目录：{0}', [filters.project ? projectLabel(filters.project) : tr('全部')])}</span> {Icon.chevron}
              </Menu>
            )}
          </div>
        </div>
        {hub.status.state !== 'error' && (hub.initializing || (refreshing && hub.scanning)) && (
          <div className="scanline" title={tr("第一次打开要把各 Agent 的会话读一遍；读到的会话已经可以点开")}>
            <span role="status">{hub.scanning ? <>{tr(" 正在读取 ")}{agentName(hub.scanning.harness)} {tr(" 的会话 · ")}{hub.scanning.done.toLocaleString()} / {hub.scanning.total.toLocaleString()}</> : tr("正在读取会话")}</span>
            <span className={`scanline-bar${hub.scanning?.total ? '' : ' indeterminate'}`} role="progressbar" aria-label={tr("正在读取会话")} aria-valuemin={0} aria-valuemax={hub.scanning?.total || undefined} aria-valuenow={hub.scanning?.total ? hub.scanning.done : undefined}><span style={hub.scanning?.total ? { width: `${Math.round(100 * hub.scanning.done / hub.scanning.total)}%` } : undefined} /></span>
          </div>
        )}
        {matches?.error && <OperationError message={tr("读取失败")} detail={matches.error} />}
        <SessionList hub={hub} items={visible} selected={current?.id} onSelect={setSelected} query={query} filters={filters}
              clear={() => { setFilters({ scope: filters.scope }); setQuery('') }} openSettings={() => openSettings('agents')} />
      </section>} second={
      <main className="col grow" style={{ background: 'var(--bg-window)', minWidth: 0, minHeight: 0 }}>
        {current ? (
          <Detail key={current.id} item={current} conversation={conversation} detailError={detailError} loadEarlier={loadEarlier} onRetry={reloadDetail}
            onCopy={source => {
              const binding = originBinding(current.bindings)
              setSheet({ title: current.title, source: { ...source, cwd: current.cwd, profile: binding?.connector, harness: binding?.harness, variant: binding?.meta.sourceProfile?.variant, account: storageAccount(binding) } })
            }} />
        ) : (
          <div className="drag col grow" style={{ alignItems: 'center', justifyContent: 'center', padding: 32 }}>
          </div>
        )}
      </main>} /> } />
      {sheet && (
        <ContinueSheet onClose={() => setSheet(undefined)} hub={hub} title={sheet.title}
          source={sheet.source} onDone={() => void hub.reload()} onOpen={async id => {
            await openSession(id); setSheet(undefined)
          }} />
      )}
      {toast && <Toast text={toast.text} sub={toast.sub} onDone={() => setToast(undefined)} />}
    </div>
  )
}

// ---------------------------------------------------------------- sidebar: where to look

function Sidebar({ hub, mode, scope, pickScope, openSettings, openAnalysis, openCopy, issue }: {
  hub: Hub; mode: Mode; scope: string; pickScope: (s: string) => void
  openSettings: () => void; openAnalysis: () => void; openCopy: () => void; issue?: Issue
}) {
  const scroll = useRef<HTMLDivElement>(null)
  const products = new Map(hub.connectors.map(c => [productKey(c), c]))
  const ordering = useAgentOrder([...products.keys()], [...new Set(hub.connectors.filter(c => c.enabled).map(productKey))], productScopeName, scroll)
  const at = (s: string) => mode.kind === 'sessions' && scope === s
  const [expanded, setExpanded] = useRemembered('koto.sidebar.allOpen', true)
  const nav = useRef<HTMLElement>(null)
  useEffect(() => { if (mode.kind === 'sessions') setExpanded(true) }, [scope, mode.kind])
  useEffect(() => { const frame = requestAnimationFrame(() => nav.current?.querySelector('[aria-selected="true"]')?.scrollIntoView({ block: 'nearest' })); return () => cancelAnimationFrame(frame) }, [scope, expanded])
  return (
    <nav ref={nav} className="col grow drag app-sidebar" style={{ background: 'var(--bg-sidebar)' }}>
      <div style={{ height: 52, flex: 'none' }} />
      <div ref={scroll} className="col no-drag scroll grow" style={{ padding: '0 8px', gap: 1 }} role="listbox" aria-label={tr("范围")}>
        <ListRow compact selected={mode.kind === 'sessions' && !expanded} aria-expanded={expanded && mode.kind === 'sessions'}
          onClick={() => { if (mode.kind === 'sessions') setExpanded(!expanded); else { openCopy(); setExpanded(true) } }}
          onKeyDown={e => { if (e.key === 'ArrowLeft' && expanded) { e.preventDefault(); setExpanded(false) } if (e.key === 'ArrowRight' && !expanded) { e.preventDefault(); setExpanded(true) } }}>
          <span className="row" style={{ width: 16, justifyContent: 'center', color: 'var(--ghost-ink)' }}>{Icon.copy}</span>
          <span className="grow ellipsis">{tr("会话复制")}</span>
          <span className="side-caret" data-open={expanded && mode.kind === 'sessions' || undefined} aria-hidden="true">{Icon.disclosure}</span>
        </ListRow>
        {mode.kind === 'sessions' && expanded && <div className="side-children col">
        <SideItem icon={Icon.all} label={tr("全部会话")} selected={at('all')} onClick={() => pickScope('all')} />
        {ordering.visible.map(key => {
          const product = products.get(key)!
          const label = productName(product.harness, product.variant)
          const title = key === 'claude-code/desktop' ? tr("在 Claude Desktop 的 Code 标签里开的会话，和 Claude Code 用的是同一批文件") : label
          return <div key={key} className="side-agent-group col" {...ordering.group(key)} data-agent={product.harness} data-product={key} data-dragging={ordering.dragged === key || undefined} data-drop={ordering.drop?.id === key && ordering.dragged !== key ? (ordering.drop.after ? 'after' : 'before') : undefined}>
            <SideItem icon={<AgentDisc harness={product.harness} size={16} title="" />} label={label} selected={at(key)} onClick={() => pickScope(key)} title={title} reorder={ordering.row(key)} />
          </div>
        })}
        </div>}
        <SideItem icon={Icon.branch} label={tr("会话对比")} selected={mode.kind === 'analysis'} onClick={openAnalysis} />
      </div>
      <span className="sr-only" id="agent-order-help">{tr('拖动调整顺序，或按 Option / Alt + 上下方向键')}</span>
      <span className="sr-only" role="status" aria-live="polite">{ordering.announcement}</span>
      {ordering.error && <Toast text={ordering.error} onDone={ordering.clearError} />}
      {issue && (
        <button type="button" className={`issue no-drag ${issue.tone === 'bad' ? 't-red' : issue.tone === 'warn' ? 't-amber' : 't-3'}`} onClick={issue.run} disabled={!issue.run} title={issue.title}>
          <span className="grow ellipsis">{issue.text}</span>{issue.action && <span className="t-medium">{issue.action}</span>}
        </button>
      )}
      <div className="col no-drag" style={{ padding: '6px 8px 8px', gap: 1, borderTop: issue ? undefined : '1px solid var(--divider)' }}>
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
function SideItem({ icon, label, selected, onClick, title, indent, reorder }: {
  icon: React.ReactNode; label: string; selected: boolean; onClick: () => void; title?: string; indent?: boolean | 2; reorder?: React.ButtonHTMLAttributes<HTMLButtonElement>
}) {
  return (
    <ListRow compact selected={selected} onClick={onClick} title={title} {...reorder} style={{ paddingLeft: indent === 2 ? 58 : indent ? 34 : undefined }}>
      <span className="row" style={{ width: 16, justifyContent: 'center', color: 'var(--ghost-ink)' }}>{icon}</span>
      <span className="grow ellipsis">{label}</span>
    </ListRow>
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
  if (!hub.loaded) return hub.error || hub.status.state === 'error'
    ? <Empty title={tr("读取失败")} why={hub.error || hub.status.error} />
    : <ListSkeleton />
  if (!items.length) {
    if (hub.initializing && !hub.error) return <Empty title={tr("正在读取会话")} why={tr("读到的会话会陆续出现在这里")} />
    if (query.trim()) return <Empty title={tr("没有找到“{0}”", [query.trim()])} why={tr("试试别的关键词")} />
    if (filters.project || filters.profile || filters.account) {
      const what = [filters.project && tr('项目目录：{0}', [projectLabel(filters.project)]), filters.profile && tr('会话目录'), filters.account && tr('账号')].filter(Boolean).join(' · ')
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
                <span className="session-identity ellipsis" title={[accountLabel(originBinding(s.bindings)), originBinding(s.bindings)?.meta.sourceProfile?.root].filter(Boolean).join('\n')}>{accountLabel(originBinding(s.bindings))}</span>
              </span>
            </span>
          </span>
        </ListRow>
      ))}
    </div>
  )
}

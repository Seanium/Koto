import { getLocale, tr } from '../i18n/core.ts'
import { useEffect, useMemo, useRef, useState } from 'react'
import { api } from '../api/client'
import type { AnalysisCollection, AnalysisResult } from '../api/types'
import { align } from '../analysis/sequence'
import { analysisDisplayLines } from '../analysis/display.ts'
import { edgePath, GRAPH_ROW, laneX, layoutGraph } from '../analysis/graph'
import { useSessionSearch, type Hub } from '../store'
import { AgentDisc, agentName, Button, Icon, Menu, OperationError, Segmented } from '../ui'

import { SplitPane } from '../ui/SplitPane'
import { DiffColumns } from './DiffColumns'

const blank = (): AnalysisCollection => ({ id: '', name: '', sessions: [] })
const letter = (i: number) => i < 26 ? String.fromCharCode(65 + i) : `A${i - 25}`

/** Only collection names/references persist. Native content and derived comparisons are read-only. */
export function Analysis({ hub }: { hub: Hub }) {
  const locale = getLocale()
  const labels = { equal: tr("相同"), changed: tr("修改"), added: tr("右侧新增"), removed: tr("左侧独有") }
  const [collections, setCollections] = useState<AnalysisCollection[]>([])
  const [collection, setCollection] = useState<AnalysisCollection>()
  const [harness, setHarness] = useState(hub.sessions[0]?.harness ?? '')
  const [query, setQuery] = useState('')
  const [data, setData] = useState<AnalysisResult>()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()
  const [left, setLeft] = useState(0), [right, setRight] = useState(1)
  const [focus, setFocus] = useState(0)
  const [context, setContext] = useState(false)
  const [view, setView] = useState<'tree' | 'sequence'>('tree')
  const [nodeID, setNodeID] = useState('')
  const [visibleCount, setVisibleCount] = useState(200)
  const navigation = useRef<HTMLDivElement>(null)
  const chosen = collection?.sessions ?? []
  const agents = [...new Set(hub.sessions.map(s => s.harness))]
  const matches = useSessionSearch(query, harness, undefined, hub.version)
  const sessions = matches?.sessions ?? hub.sessions.filter(s => s.harness === harness && `${s.id} ${s.title} ${s.cwd ?? ''}`.toLowerCase().includes(query.trim().toLowerCase()))
  const missing = chosen.filter(id => !hub.sessions.some(s => s.id === id) && !matches?.sessions?.some(s => s.id === id))
  useEffect(() => { let live = true; api.analyses().then(x => { if (live) setCollections(x) }, e => { if (live) setError(e.message) }); return () => { live = false } }, [])
  const edit = (c: AnalysisCollection) => { setCollection(c); setHarness(c.sessions[0]?.split(':')[0] ?? agents[0] ?? ''); setData(undefined); setError(undefined); setQuery('') }
  const run = async (c: AnalysisCollection, save: boolean) => {
    setBusy(true); setError(undefined); setCollection(c)
    try {
      if (save) { c = await api.saveAnalysis({ ...c, name: c.name.trim() || tr("会话对比") }); setCollection(c); setCollections(await api.analyses()) }
      const d = await api.analyze(c.sessions); setData(d); setLeft(0); setRight(1); setFocus(0); setContext(false); setVisibleCount(200); setView('tree'); setNodeID(layoutGraph(d).rows[0]?.node.id ?? d.nodes[0]?.id ?? '')
    } catch (e) { setError((e as Error).message) } finally { setBusy(false) }
  }
  const remove = async (id: string) => { try { await api.deleteAnalysis(id); setCollections(xs => xs.filter(x => x.id !== id)) } catch (e) { setError((e as Error).message) } }
  const byID = useMemo(() => new Map(data?.nodes.map(n => [n.id, n])), [data])
  const sequence = (i: number) => data?.sessions[i]?.nodes.map(id => byID.get(id)!).filter(n => n.turn > 0) ?? []
  const pair = useMemo(() => {
    if (view !== 'sequence') return []
    const a = sequence(left), b = sequence(right)
    return align(a.map(n => n.hash), b.map(n => n.hash)).map(r => ({ ...r, a: r.left === undefined ? undefined : a[r.left], b: r.right === undefined ? undefined : b[r.right] }))
  }, [data, left, right, view]) // eslint-disable-line react-hooks/exhaustive-deps
  const graph = useMemo(() => layoutGraph(data ?? { harness: '', sessions: [], nodes: [] }), [data])
  const selectedNode = byID.get(nodeID)
  const differences = pair.flatMap((r, i) => r.kind === 'equal' ? [] : [i])
  const a = context ? data?.nodes.find(n => n.turn === 0 && n.sessions.includes(left)) : pair[focus]?.a
  const b = context ? data?.nodes.find(n => n.turn === 0 && n.sessions.includes(right)) : pair[focus]?.b
  const lines = useMemo(() => {
    if (view !== 'sequence') return []
    const al = a?.text.split('\n') ?? [], bl = b?.text.split('\n') ?? []
    const shownA = analysisDisplayLines(a), shownB = analysisDisplayLines(b)
    return align(al, bl).map(r => ({ ...r, a: r.left === undefined ? undefined : shownA[r.left], b: r.right === undefined ? undefined : shownB[r.right] }))
  }, [a, b, view, locale])
  useEffect(() => {
    const index = view === 'sequence' ? focus : graph.rows.findIndex(r => r.node.id === nodeID)
    if (index >= visibleCount) { setVisibleCount(index + 200); return }
    navigation.current?.querySelector('[aria-pressed="true"]')?.scrollIntoView({ block: 'nearest' })
  }, [focus, view, nodeID, context, visibleCount, graph])
  const visibleGraph = graph.rows.slice(0, visibleCount)
  const navigationLength = view === 'tree' ? graph.rows.length : pair.length
  const graphWidth = Math.max(72, laneX(Math.max(0, (data?.sessions.length ?? 1) - 1)) + 24)
  const jump = (direction: number) => { const i = direction > 0 ? differences.find(i => i > focus) : [...differences].reverse().find(i => i < focus); if (i !== undefined) { setFocus(i); setContext(false) } }
  return <section className="col grow analysis" style={{ minWidth: 0 }}>
    <header className="detail-header drag">
      <div className="row g3">{(data || collection) && <Button variant="ghost" disabled={busy} onClick={() => { setData(undefined); setCollection(undefined); setError(undefined) }}>{Icon.left} {tr(" 返回")}</Button>}<h1 className="t-title grow ellipsis" style={{ margin: 0 }}>{data ? collection?.name : tr("会话对比")}</h1>
        {data && <Button small disabled={busy} onClick={() => edit(collection!)}>{tr("编辑会话")}</Button>}
      </div>
      {data && <div className="analysis-views no-drag"><Segmented label={tr("对比视图")} value={view} onChange={setView} options={[{ value: 'tree', label: tr("会话树") }, { value: 'sequence', label: tr("差异对比") }]} /></div>}
    </header>
    {!data ? <div className={`col g3 grow ${collection ? 'analysis-editor' : 'scroll'}`} style={{ padding: '0 24px 24px' }}>
      {!collection ? <>
        <div className="row g3"><span className="t-meta grow">{tr("选择同一个 Agent 的会话，查看分叉和内容差异。")}</span><Button variant="primary" onClick={() => edit(blank())}>{tr("新建对比")}</Button></div>
        <div className="group">{collections.map(c => <div key={c.id} className="group-row row g3 analysis-collection-row">
          <button className="analysis-collection grow row g3" disabled={busy} onClick={() => void run(c, false)}><AgentDisc harness={c.sessions[0]?.split(':')[0]} size={22} /><span className="col g1 grow"><span className="t-medium ellipsis">{c.name}</span><span className="t-meta">{c.sessions.length} {tr(" 个会话")}</span></span></button>
          <Menu label={tr("对比集合选项")} items={[{ label: tr("编辑…"), onClick: () => edit(c) }, { label: tr("删除对比集合"), sub: tr("原会话保留"), onClick: () => void remove(c.id) }]}>{Icon.more}</Menu>
        </div>)}</div>
        {!collections.length && <div className="t-meta" style={{ padding: '36px 0' }}>{tr("还没有保存的对比")}</div>}
      </> : <>
        <input aria-label={tr("对比名称")} className="field" placeholder={tr("对比名称")} value={collection.name} maxLength={120} disabled={busy} onChange={e => setCollection({ ...collection, name: e.target.value })} />
        <div className="row g2"><Menu trigger="secondary" align="under" width={240} label={tr("选择 Agent")} items={agents.map(h => ({ icon: <AgentDisc harness={h} size={18} />, label: agentName(h), checked: h === harness, onClick: () => { setHarness(h); setCollection({ ...collection, sessions: [] }) } }))}><AgentDisc harness={harness} size={18} />{agentName(harness)} {Icon.chevron}</Menu>
          <input aria-label={tr("搜索对比会话")} className="field grow" placeholder={tr("搜索会话")} value={query} onChange={e => setQuery(e.target.value)} />
        </div>
        <div className="group grow analysis-session-choices">{[...sessions.map(s => ({ id: s.id, title: s.title, sub: s.cwd || s.id })), ...missing.map(id => ({ id, title: tr("会话暂不可用"), sub: id }))].map(s => <label key={s.id} className="group-row row g3">
          <input type="checkbox" checked={chosen.includes(s.id)} disabled={busy || chosen.length >= 32 && !chosen.includes(s.id)} onChange={e => setCollection({ ...collection, sessions: e.target.checked ? [...chosen, s.id] : chosen.filter(x => x !== s.id) })} />
          <span className="col grow" style={{ minWidth: 0 }}><span className="ellipsis">{s.title}</span><span className="t-meta ellipsis">{s.sub}</span></span>
        </label>)}</div>
        {matches?.error && <OperationError message={tr("读取失败")} detail={matches.error} />}
        <div className="row g2 analysis-editor-footer"><span className="t-meta grow">{tr("已选 ")}{chosen.length} {tr(" 个（2–32 个）")}</span><Button disabled={busy} onClick={() => { setCollection(undefined); setError(undefined) }}>{tr("取消")}</Button><Button variant="primary" disabled={chosen.length < 2 || busy} onClick={() => void run(collection, true)}>{busy ? tr("正在对比…") : tr("保存并对比")}</Button></div>
      </>}
      {error && <OperationError message={tr("对比失败")} detail={error} />}
    </div> : <>
      {view === 'tree' ? <>
        <div className="analysis-legend" aria-label={tr("会话分支")}>
          {data.sessions.map((session, i) => <button key={session.selector} className="branch-label" title={session.title} onClick={() => setNodeID(session.nodes.at(-1) ?? '')}>
            <span className="branch-dot" style={{ background: graph.color(graph.sessionLane(i)) }} /><span className="t-medium">{letter(i)}</span><span className="ellipsis">{session.title}</span>
          </button>)}
        </div>
        <SplitPane name="comparison-tree" label={tr("会话树与内容宽度")} ratio={.6} min={260} minRest={280} minRatio={.25} maxRatio={.75} className="analysis-workspace analysis-tree-view" first={
          <div ref={navigation} className="analysis-graph scroll">
            <div className="analysis-graph-rows" style={{ minWidth: graphWidth + 270 }}>
              <svg width={graphWidth} height={visibleGraph.length * GRAPH_ROW} className="branch-graph" aria-label={tr("按轮次排列的会话分支")}>
                {visibleGraph.map((r, i) => <path key={r.node.id} d={edgePath(r, i, graph.rows)} fill="none" stroke={graph.color(r.lane)} strokeWidth={2} strokeLinecap="round" />)}
                {visibleGraph.map((r, i) => <g key={r.node.id}>
                  {r.node.id === nodeID && <circle cx={laneX(r.lane)} cy={i * GRAPH_ROW + GRAPH_ROW / 2} r={10} fill="none" stroke="var(--ink)" strokeWidth={1.5} />}
                  <circle cx={laneX(r.lane)} cy={i * GRAPH_ROW + GRAPH_ROW / 2} r={5} fill="var(--bg-window)" stroke={graph.color(r.lane)} strokeWidth={2.5} />
                </g>)}
              </svg>
              {visibleGraph.map(r => <button key={r.node.id} className="analysis-node analysis-graph-node row no-drag" data-turn={r.node.turn} data-node-id={r.node.id} style={{ paddingLeft: graphWidth }} aria-pressed={r.node.id === nodeID} onClick={() => setNodeID(r.node.id)}>
                <span className="t-meta graph-turn">{tr("第 ")}{r.node.turn} {tr(" 轮")}</span>
                <span className="col grow" style={{ minWidth: 0, gap: 3 }}><span className="ellipsis" title={r.node.preview}>{r.node.preview || tr("工具与回复")}</span>
                  <span className="row g1 graph-members" title={r.node.sessions.map(i => letter(i) + ' · ' + data.sessions[i].title).join('\n')}>
                    {r.node.sessions.map(i => <span key={i} className="branch-member" style={{ color: graph.color(graph.sessionLane(i)) }}>{letter(i)}</span>)}
                  </span>
                </span>
              </button>)}
            </div>
            {navigationLength > visibleCount && <Button variant="ghost" onClick={() => setVisibleCount(n => n + 200)}>{tr("显示更多（剩余 ")}{navigationLength - visibleCount} {tr(" 轮）")}</Button>}
            {!graph.rows.length && <div className="t-meta" style={{ padding: 20 }}>{tr("这些会话没有对话内容")}</div>}
          </div>
          } second={<aside className="analysis-inspector col grow">
            <div className="analysis-inspector-head col g2">
              <span className="t-head">{selectedNode?.turn ? tr("第 ") + selectedNode.turn + tr(" 轮") : tr("系统与环境上下文")}</span>
              <span className="row g1 graph-members">{selectedNode?.sessions.map(i => <span key={i} className="branch-member" style={{ color: graph.color(graph.sessionLane(i)) }} title={data.sessions[i].title}>{letter(i)}</span>)}</span>
            </div>
            <div className="grow scroll selectable analysis-node-content">{analysisDisplayLines(selectedNode).join("\n") || tr("选择节点查看内容")}</div>
            {data.nodes.some(n => n.turn === 0) && <Menu trigger="ghost" label={tr("查看系统与环境上下文")} items={data.sessions.map((s, i) => ({ label: letter(i) + ' · ' + s.title, disabled: !s.nodes.some(id => byID.get(id)?.turn === 0), onClick: () => setNodeID(s.nodes.find(id => byID.get(id)?.turn === 0) ?? '') }))}>{tr("系统与环境上下文 ")}{Icon.chevron}</Menu>}
          </aside>} />
      </> : <>
        <div className="analysis-pair row g2">
          {[left, right].map((s, side) => <Menu trigger="secondary" align="under" key={side} label={side ? tr("右侧会话") : tr("左侧会话")} items={data.sessions.map((session, i) => ({ label: letter(i) + ' · ' + session.title, sub: session.selector, checked: s === i, disabled: i === (side ? left : right), onClick: () => { if (side) setRight(i); else setLeft(i); setFocus(0); setContext(false) } }))}><span className="branch-dot" style={{ background: graph.color(graph.sessionLane(s)) }} /><span className="ellipsis">{letter(s)}：{data.sessions[s]?.title}</span>{Icon.chevron}</Menu>)}
          <span className="grow" /><span className="t-meta" style={{ flex: 'none' }}>{differences.length ? differences.length + tr(" 处差异") : tr("内容相同")}</span>
          <Button small disabled={!differences.some(i => i < focus)} onClick={() => jump(-1)} aria-label={tr("上一处差异")}>↑</Button>
          <Button small disabled={!differences.some(i => i > focus)} onClick={() => jump(1)} aria-label={tr("下一处差异")}>↓</Button>
        </div>
        <SplitPane name="comparison-sequence" label={tr("对比轮次与内容宽度")} initial={240} min={180} max={340} minRest={480} className="analysis-workspace" first={
          <div ref={navigation} className="analysis-sequence col scroll grow">
            {data.nodes.some(n => n.turn === 0) && <Button variant="ghost" aria-pressed={context} onClick={() => setContext(true)}>{tr("系统与环境上下文")}</Button>}
            {pair.slice(0, visibleCount).map((r, i) => <button key={i} className="analysis-node col" aria-pressed={!context && i === focus} onClick={() => { setFocus(i); setContext(false) }}>
              <span className="row g2"><span className="t-meta">{r.a ? tr("左 ") + r.a.turn : tr("左 —")} / {r.b ? tr("右 ") + r.b.turn : tr("右 —")}</span><span className="t-meta">{labels[r.kind]}</span></span>
              <span className="ellipsis">{r.a?.preview || r.b?.preview || tr("工具与回复")}</span>
            </button>)}
            {navigationLength > visibleCount && <Button variant="ghost" onClick={() => setVisibleCount(n => n + 200)}>{tr("显示更多（剩余 ")}{navigationLength - visibleCount} {tr(" 轮）")}</Button>}
          </div>
          } second={<div className="col grow" style={{ minWidth: 0, minHeight: 0 }}>
            <div className="row g2" style={{ padding: '10px 16px', borderBottom: '1px solid var(--divider)' }}><span className="t-medium grow">{context ? tr("系统与环境上下文") : tr("左 ") + (a ? tr("第 ") + a.turn + tr(" 轮") : tr("无")) + tr(" · 右 ") + (b ? tr("第 ") + b.turn + tr(" 轮") : tr("无"))}</span><span className="t-meta">{a?.hash === b?.hash ? tr("相同") : context ? tr("有差异") : labels[pair[focus]?.kind ?? 'changed']}</span></div>
            {(a?.truncated || b?.truncated) && <div className="t-meta" style={{ padding: '8px 16px' }}>{tr("按完整内容比较；长内容仅显示前 24,000 字符。")}</div>}
            {lines.length ? <DiffColumns key={left + ":" + right + ":" + focus + ":" + context} lines={lines} /> : <div className="t-meta" style={{ padding: 16 }}>{tr("没有可显示的内容")}</div>}
          </div>} />
      </>}
    </>}
  </section>
}

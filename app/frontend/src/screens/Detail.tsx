import { tr } from '../i18n/core.ts'
import { shortcut } from '../ui/shortcut'
// One native session: a linear turn list or the full conversation, with explicit copy selection.
import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react'
import { api, shell, useOpeners } from '../api/client'
import { openerName, projectName, revealLabel } from '../api/model'
import type { Conversation, SessionItem } from '../store'
import { Banner, Button, Icon, Menu, OverflowLabel, Popover, Segmented, Toast } from '../ui'
import { ImageSource, Timeline, TimelineSkeleton, TurnPreview, type TimelineHandle } from './Timeline'
import { SelectionCheck, SelectionContext, useContentSelection } from './ContentSelection'
import type { CopySource } from './Continue'
import { ResumeActions } from './ResumeActions'
import { SplitPane } from '../ui/SplitPane'
import { SelectionSurface } from './SelectionSurface'
import { captureScrollAnchor } from '../scrollAnchor'

export function Detail({ item, detailError, conversation: c, loadEarlier, onRetry, onCopy, onTrash, askTrash }: {
  item: SessionItem; detailError?: string; conversation?: Conversation; loadEarlier: (untilTurn?: number) => Promise<void>; onRetry: () => void
  onCopy: (source: CopySource) => void
  onTrash: () => Promise<string | undefined>; askTrash: number
}) {
  const scroller = useRef<HTMLDivElement>(null)
  const turnList = useRef<HTMLDivElement>(null)
  const previewScroller = useRef<HTMLDivElement>(null)
  const timeline = useRef<TimelineHandle>(null)
  const [view, setView] = useState<'list' | 'chat'>('list')
  const selection = useContentSelection(c)
  const custom = selection.source
  const rangeLabel = selection.invalid ? tr("会话已变更，请重新选择") : custom?.label ?? tr("未选择内容")
  const [preview, setPreview] = useState<number>()
  const [loadingEarlier, setLoadingEarlier] = useState(false)
  const earlierBusy = useRef(false)
  const earlierFrame = useRef(0)
  const listInteracted = useRef(false)
  const alive = useRef(false)
  useEffect(() => { alive.current = true; return () => { alive.current = false; cancelAnimationFrame(earlierFrame.current) } }, [])
  const turn = c?.turns.find(t => t.index === preview) ?? c?.turns.at(-1)
  const binding = item.bindings[0]
  const imageSource = useCallback((sha: string, signal?: AbortSignal) => api.image(item.id, sha, signal), [item.id])
  const copy = () => { if (custom) onCopy(custom) }
  const loaded = !!c
  const loadMore = useCallback(async () => {
    if (earlierBusy.current || !c?.hasEarlier) return
    listInteracted.current = false
    earlierBusy.current = true; setLoadingEarlier(true)
    const list = turnList.current
    const restoreAnchor = captureScrollAnchor(list, '.turn-list-row')
    try { await loadEarlier() } finally {
      if (alive.current) earlierFrame.current = requestAnimationFrame(() => {
        restoreAnchor()
        earlierBusy.current = false; setLoadingEarlier(false)
      })
      else restoreAnchor()
    }
  }, [c?.hasEarlier, loadEarlier])
  useLayoutEffect(() => {
    if (view !== 'list' || !loaded) return
    const list = turnList.current
    if (!list) return
    let interrupted = false
    const position = () => {
      if (interrupted) return
      if (preview !== undefined) list.querySelector(`[data-turn="${preview}"]`)?.scrollIntoView({ block: 'nearest' })
      else list.scrollTop = list.scrollHeight
      if (preview === undefined && previewScroller.current) previewScroller.current.scrollTop = previewScroller.current.scrollHeight
    }
    const frame = requestAnimationFrame(position), observer = new ResizeObserver(position)
    observer.observe(list)
    if (previewScroller.current) observer.observe(previewScroller.current)
    const stop = () => { interrupted = true }
    const timer = setTimeout(() => observer.disconnect(), 1000)
    list.addEventListener('wheel', stop, { passive: true }); list.addEventListener('pointerdown', stop); list.addEventListener('keydown', stop)
    return () => { cancelAnimationFrame(frame); clearTimeout(timer); observer.disconnect(); list.removeEventListener('wheel', stop); list.removeEventListener('pointerdown', stop); list.removeEventListener('keydown', stop) }
    // Opening the view sets its position; refreshes and selections preserve the reader's place.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [view, loaded])
  useEffect(() => {
    if (!selection.active) return
    const escape = (e: KeyboardEvent) => { if (e.key === 'Escape' && !document.querySelector('.sheet,.menu,.attachment-overlay,.img-overlay')) { e.preventDefault(); selection.exit() } }
    window.addEventListener('keydown', escape)
    return () => window.removeEventListener('keydown', escape)
  }, [selection.active])
  return <div className="col grow" style={{ minHeight: 0 }}>
    <header className="drag detail-header session-header">
      <div className="session-heading">
        <h1 className="t-title no-drag"><OverflowLabel text={item.title} /></h1>
        <div className="session-project no-drag">{item.cwd && <ProjectButton cwd={item.cwd} />}</div>
        <TrashConfirm onTrash={onTrash} ask={askTrash} />
      </div>
      <div className="detail-toolbar no-drag">
        <Segmented label={tr("会话视图")} value={view} onChange={setView} options={[{ value: 'list', label: tr("列表视图") }, { value: 'chat', label: tr("对话视图") }]} />
        <span className="copy-control">
          {selection.active ? <>
            <Menu trigger="ghost" label={tr("选择操作")} title={rangeLabel} width={180} items={[{ label: tr("全选"), onClick: selection.all }, { label: tr("清空"), onClick: selection.clear }]}>
              <span className="copy-range t-meta">{rangeLabel}</span>{Icon.chevron}
            </Menu>
            <Button variant="ghost" icon aria-label={tr("取消选择")} title={tr("取消选择")} onClick={selection.exit}>{Icon.close}</Button>
            <Button id="copy-main" variant="primary" disabled={!custom} onClick={copy}>{tr("复制到…")}</Button>
          </> : <>
            {binding.resume && <ResumeActions command={binding.resume.shell} instructions={binding.resume.instructions && binding.harness === 'dsh' ? tr(/win/i.test(navigator.platform) ? "输入下一条消息，按 Enter 提交。" : "输入下一条消息后，在新行按 Ctrl+D 提交。") : binding.resume.instructions} />}
            <Button id="copy-main" variant="primary" disabled={!selection.canStart} onClick={selection.start}>{tr("选择复制范围")}</Button>
          </>}
        </span>
      </div>
    </header>
    {detailError && <Banner action={<Button small onClick={onRetry}>{tr("重试")}</Button>}>{detailError}</Banner>}
    {!c ? !detailError && <div className="conversation-loading" aria-busy="true">
      <div className="t-meta" role="status">{tr("正在读取会话内容…")}</div>
      <span className="loading-bar indeterminate" role="progressbar" aria-label={tr("正在读取会话内容…")}><span /></span>
      <TimelineSkeleton />
    </div> : <SelectionContext.Provider value={selection.selection}><ImageSource.Provider value={imageSource}><SelectionSurface onFocusTurn={setPreview}>
      {view === 'list' ? <SplitPane name="turns" label={tr("轮次与内容宽度")} ratio={.55} min={220} minRest={280} minRatio={.25} maxRatio={.75} className="conversation-list" first={
        <div ref={turnList} className="col grow scroll turn-list" role="listbox" aria-label={tr("轮次")} aria-multiselectable={selection.active || undefined}
          onWheel={() => { listInteracted.current = true }} onTouchStart={() => { listInteracted.current = true }} onPointerDown={() => { listInteracted.current = true }} onKeyDown={() => { listInteracted.current = true }}
          onScroll={e => { if (listInteracted.current && e.currentTarget.scrollTop < 80) void loadMore() }}>
          {c.hasEarlier && <Button variant="ghost" disabled={loadingEarlier} onClick={() => void loadMore()}>{loadingEarlier ? tr("加载中…") : tr("加载更早的轮次")}</Button>}
          {c.turns.map(t => <div key={t.index} role="option" tabIndex={0} data-turn={t.index} data-selection-unit={selection.active && !t.partial && t.index <= selection.selection!.state.through ? 'turn' : undefined} data-selection-events={selection.selection?.events(t.eventIds).join(' ')} aria-selected={selection.active ? t.index <= selection.selection!.state.through && t.eventIds.some(id => selection.selection?.has(id)) : turn?.index === t.index} className="turn-list-row no-drag" onClick={() => setPreview(t.index)} onKeyDown={e => { if (e.target === e.currentTarget && (e.key === 'Enter' || e.key === ' ')) { e.preventDefault(); setPreview(t.index); if (selection.active && !t.partial && t.index <= selection.selection!.state.through) selection.selection?.toggle(t.eventIds) } }}>
            {selection.active ? <SelectionCheck scope="turn" events={t.eventIds} label={tr("第 {0} 轮", [t.index])} disabled={t.partial || t.index > selection.selection!.state.through} /> : <><span className="turn-list-point" /><span className="t-meta">{tr("第 ")}{t.index} {tr(" 轮")}</span></>}
            <span className="ellipsis">{t.user || (t.promptId ? tr("图片或附件") : tr("仅回复片段"))}</span>
            {t.index > c.completeTurns && <span className="t-meta">{tr("进行中")}</span>}
          </div>)}
        </div>} second={turn && <aside key={turn.index} data-turn={turn.index} className="turn-preview col grow">
          <div className="row g2" style={{ padding: 16, borderBottom: '1px solid var(--divider)' }}><span className="t-head grow">{tr("第 ")}{turn.index} {tr(" 轮")}</span><Button small onClick={() => { setView('chat'); setTimeout(() => timeline.current?.jumpToTurn(turn.index), 100) }}>{tr("查看对话")}</Button></div>
          <div ref={previewScroller} className="col grow scroll" style={{ padding: 16 }}><TurnPreview turn={turn} /></div>
        </aside>} /> : <div className="col grow" style={{ position: 'relative', minHeight: 0 }}>
        <div ref={scroller} className="col scroll grow" style={{ padding: '0 24px 24px', position: 'relative' }}>
          <Timeline ref={timeline} turns={c.turns} scroller={scroller} resetKey={item.id} totalTurns={c.totalTurns} completeTurns={c.completeTurns}
            hasEarlier={c.hasEarlier} loadEarlier={loadEarlier} />
        </div><div className="outline-host" />
      </div>}
    </SelectionSurface></ImageSource.Provider></SelectionContext.Provider>}
  </div>
}

function TrashConfirm({ onTrash, ask }: { onTrash: () => Promise<string | undefined>; ask: number }) {
  const [open, setOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()
  const first = useRef(ask)
  useEffect(() => { if (ask !== first.current) setOpen(true) }, [ask])
  useEffect(() => { if (open) { setError(undefined); requestAnimationFrame(() => document.getElementById('trash-go')?.focus()) } }, [open])
  const go = async () => {
    setBusy(true); setError(undefined)
    const err = await onTrash()
    setBusy(false)
    if (err) setError(err)
    else setOpen(false)
  }
  return (
    <Popover open={open} setOpen={setOpen} width={300} align="right" label={tr("移到回收站")}
      trigger={p => <Button className="btn-square" aria-label={tr("移到回收站")} title={shortcut(tr("移到回收站（⌘⌫）"))} aria-pressed={open} {...p}>{Icon.trash}</Button>}>
      <div className="col g2" style={{ padding: '8px 10px 10px' }} onKeyDown={e => { if (e.key === 'Enter' && !busy) { e.preventDefault(); void go() } }}>
        <span className="t-medium">{tr("把这个会话移到回收站？")}</span>
        <span className="t-meta">{tr("只移动当前会话。之后可以在回收站里恢复。")}</span>
        {error && <span className="note">{error}</span>}
        <div className="row g2" style={{ justifyContent: 'flex-end', paddingTop: 4 }}>
          <Button small onClick={() => setOpen(false)}>{tr("取消")}</Button>
          <Button small id="trash-go" className="btn-danger" disabled={busy} onClick={() => void go()}>{busy ? tr("正在移走…") : error ? tr("再试一次") : tr("移到回收站")}</Button>
        </div>
      </div>
    </Popover>
  )
}

/** Project actions and the full path stay in the menu so long paths cannot move the toolbar. */
function ProjectButton({ cwd }: { cwd: string }) {
  const openers = useOpeners()
  const [exists, setExists] = useState(true)
  const [note, setNote] = useState<string>()
  useEffect(() => { void shell.pathExists(cwd).then(setExists, () => setExists(true)) }, [cwd])
  const name = projectName(cwd)
  const info = openers?.info
  const nameOf = (list: { id: string; name: string }[] | undefined, id?: string) => { const opener = list?.find(o => o.id === id); return opener && openerName(opener) }
  const terminal = info?.terminal ? (info.terminal === 'custom' ? tr("自定义") : nameOf(info.terminals, info.terminal)) : undefined
  const editor = info?.editor ? (info.editor === 'custom' ? tr("自定义") : nameOf(info.editors, info.editor)) : undefined
  const gone = exists ? undefined : tr("文件夹已不存在")
  const fail = (what: string) => (ok: unknown) => { if (!ok) setNote(tr("没能{0}", [what])) }
  const reveal = () => { void shell.revealPath(cwd).then(fail(tr("打开文件夹"))) }
  // ⌥⌘R reveals the current session's folder.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => { if (exists && e.metaKey && e.altKey && e.code === 'KeyR') { e.preventDefault(); reveal() } }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [cwd, exists])
  return (
    <>
        <Menu trigger="tint" align="under" label={tr("项目 {0}", [name])} title={tr("项目操作")}
          header={<><span className="t-medium ellipsis">{name}</span><span className="mono t-3 selectable" style={{ wordBreak: 'break-all' }}>{cwd}</span>{gone && <span className="t-amber">{gone}</span>}</>}
          items={[
            { label: revealLabel(info?.fileManager ?? 'Finder'), hint: shortcut('⌥⌘R'), hintMono: true, disabled: !exists, title: gone, onClick: reveal },
            ...(terminal ? [{ label: tr("在终端中打开"), hint: terminal, hintMono: true, disabled: !exists, title: gone, onClick: () => { void shell.openInTerminal('', cwd).then(fail(tr("打开终端"))) } }] : []),
            ...(editor ? [{ label: tr("在编辑器中打开"), hint: editor, hintMono: true, disabled: !exists, title: gone, onClick: () => { void shell.openInEditor(cwd).then(fail(tr("打开编辑器"))) } }] : []),
            { separator: true },
            { label: tr("复制路径"), onClick: () => { void shell.copy(cwd) } },
          ]}>
          {Icon.folder}<span className="ellipsis">{name}</span>{Icon.chevron}
        </Menu>
      {note && <Toast text={note} onDone={() => setNote(undefined)} />}
    </>
  )
}


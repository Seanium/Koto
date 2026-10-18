import { tr } from '../i18n/core.ts'
import { shortcut } from '../ui/shortcut'
// One native session: a linear turn list or the full conversation, with explicit copy selection.
import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react'
import { api, shell, useOpeners } from '../api/client'
import { openerName, originBinding, projectName, revealLabel } from '../api/model'
import type { Conversation, SessionItem } from '../store'
import { Banner, Button, Icon, Menu, OverflowLabel, Segmented, Toast } from '../ui'
import { ImageSource, Timeline, TimelineSkeleton, TurnPreview, type TimelineHandle } from './Timeline'
import { SelectionCheck, SelectionContext, useContentSelection } from './ContentSelection'
import type { CopySource } from './Continue'
import { ResumeActions } from './ResumeActions'
import { SplitPane } from '../ui/SplitPane'
import { SelectionSurface } from './SelectionSurface'
import { captureScrollAnchor } from '../scrollAnchor'

export function Detail({ item, detailError, conversation: c, loadEarlier, onRetry, onCopy }: {
  item: SessionItem; detailError?: string; conversation?: Conversation; loadEarlier: (untilTurn?: number) => Promise<void>; onRetry: () => void
  onCopy: (source: CopySource) => void
}) {
  const scroller = useRef<HTMLDivElement>(null)
  const turnList = useRef<HTMLDivElement>(null)
  const previewScroller = useRef<HTMLDivElement>(null)
  const timeline = useRef<TimelineHandle>(null)
  const copyButton = useRef<HTMLButtonElement>(null)
  const [view, setView] = useState<'list' | 'chat'>('list')
  const selection = useContentSelection(c)
  const exitSelection = () => { selection.exit(); copyButton.current?.focus({ preventScroll: true }) }
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
  const sessionPath = originBinding(item.bindings)?.path
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
    const escape = (e: KeyboardEvent) => { if (e.key === 'Escape' && !document.querySelector('.sheet,.menu,.attachment-overlay,.img-overlay')) { e.preventDefault(); exitSelection() } }
    window.addEventListener('keydown', escape)
    return () => window.removeEventListener('keydown', escape)
  }, [selection.active])
  return <div className="col grow" style={{ minHeight: 0 }}>
    <header className="drag detail-header session-header">
      <div className="session-heading">
        <h1 className="t-title no-drag"><OverflowLabel text={item.title} /></h1>
        <div className="session-project no-drag">{item.cwd && <ProjectButton cwd={item.cwd} />}</div>
        <div className="row g2 no-drag">
          {sessionPath && <SessionFileButton path={sessionPath} />}
        </div>
      </div>
      <div className="detail-toolbar no-drag">
        <Segmented label={tr("会话视图")} value={view} onChange={setView} options={[{ value: 'list', label: tr("列表视图") }, { value: 'chat', label: tr("对话视图") }]} />
        <span className="copy-control">
          {selection.active ? <>
            <Menu key="selection-range" trigger="ghost" label={tr("选择操作")} title={rangeLabel} width={180} items={[{ label: tr("全选"), onClick: selection.all }, { label: tr("清空"), onClick: selection.clear }]}>
              <span className="copy-range t-meta">{rangeLabel}</span>{Icon.chevron}
            </Menu>
            <Button key="selection-cancel" variant="ghost" onClick={exitSelection}>{tr("取消")}</Button>
            <Button key="copy-action" ref={copyButton} id="copy-main" variant="primary" disabled={!custom} onClick={copy}>{tr("复制到…")}</Button>
          </> : <>
            {binding.resume && <ResumeActions key="resume" command={binding.resume.shell} instructions={binding.resume.instructions && binding.harness === 'dsh' ? tr(/win/i.test(navigator.platform) ? "输入下一条消息，按 Enter 提交。" : "输入下一条消息后，在新行按 Ctrl+D 提交。") : binding.resume.instructions} />}
            <Button key="copy-action" ref={copyButton} id="copy-main" variant="primary" disabled={!selection.canStart} onClick={selection.start}>{tr("选择复制范围")}</Button>
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
            <span className="turn-list-choice">{selection.active ? <SelectionCheck scope="turn" events={t.eventIds} label={tr("第 {0} 轮", [t.index])} disabled={t.partial || t.index > selection.selection!.state.through} /> : <span className="turn-list-point" />}</span><span className="t-meta">{tr("第 ")}{t.index} {tr(" 轮")}</span>
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

// Clipboard feedback stays beside its control. Only the latest successful
// request may show it; changing the path or leaving the view cancels its timer.
function usePathCopy(value: string) {
  const [feedback, setFeedback] = useState<'success' | 'error'>()
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  const request = useRef(0)
  useEffect(() => {
    setFeedback(undefined)
    return () => { request.current++; clearTimeout(timer.current) }
  }, [value])
  const copy = async () => {
    const id = ++request.current
    clearTimeout(timer.current); setFeedback(undefined)
    const ok = await shell.copy(value).catch(() => false)
    if (id !== request.current) return
    setFeedback(ok ? 'success' : 'error')
    timer.current = setTimeout(() => setFeedback(undefined), 1800)
  }
  return { copy, feedback }
}

function PathCopyFeedback({ state, error }: { state?: 'success' | 'error'; error: string }) {
  const bubble = useRef<HTMLSpanElement>(null)
  const text = state === 'success' ? tr('复制成功') : error
  useLayoutEffect(() => {
    if (!state) return
    const fit = () => {
      const el = bubble.current
      if (!el) return
      el.style.translate = ''
      const rect = el.getBoundingClientRect(), width = document.documentElement.clientWidth
      const offset = rect.right > width - 8 ? width - 8 - rect.right : rect.left < 8 ? 8 - rect.left : 0
      if (offset) el.style.translate = `${Math.round(offset)}px 0`
    }
    fit(); window.addEventListener('resize', fit)
    return () => window.removeEventListener('resize', fit)
  }, [state, text])
  return state && <span ref={bubble} className="path-copy-feedback" role="status" aria-live="polite" aria-atomic="true">{text}</span>
}

/** Native file paths can point at a log or a database shared by several sessions. */
function SessionFileButton({ path }: { path: string }) {
  const { copy, feedback } = usePathCopy(path)
  const [note, setNote] = useState<string>()
  const reveal = async () => {
    try { if (!await shell.revealPath(path)) setNote(tr('无法打开会话文件所在目录，文件可能已移动或删除。')) }
    catch { setNote(tr('无法打开会话文件所在目录，文件可能已移动或删除。')) }
  }
  return <span className="path-copy-anchor">
    <Menu trigger="secondary" square label={tr('会话文件')} width={320}
      header={<><span className="t-medium">{tr('会话文件')}</span><span className="mono t-3 selectable" style={{ overflowWrap: 'anywhere' }}>{path}</span></>}
      items={[
        { label: tr('打开会话文件所在目录'), icon: Icon.folder, onClick: () => void reveal() },
        { label: tr('复制会话文件路径'), icon: Icon.copy, onClick: () => void copy() },
      ]}>{Icon.file}</Menu>
    <PathCopyFeedback state={feedback} error={tr('未能复制会话文件路径，请重试。')} />
    {note && <Toast text={note} onDone={() => setNote(undefined)} />}
  </span>
}

/** Project actions use cwd; the separate session-file menu uses the native log or database. */
function ProjectButton({ cwd }: { cwd: string }) {
  const { copy, feedback } = usePathCopy(cwd)
  const openers = useOpeners()
  const [exists, setExists] = useState(true)
  const [note, setNote] = useState<string>()
  useEffect(() => { void shell.pathExists(cwd).then(setExists, () => setExists(true)) }, [cwd])
  const name = projectName(cwd)
  const info = openers?.info
  const nameOf = (list: { id: string; name: string }[] | undefined, id?: string) => { const opener = list?.find(o => o.id === id); return opener && openerName(opener) }
  const terminal = info?.terminal ? (info.terminal === 'custom' ? tr("自定义") : nameOf(info.terminals, info.terminal)) : undefined
  const editor = info?.editor ? (info.editor === 'custom' ? tr("自定义") : nameOf(info.editors, info.editor)) : undefined
  const gone = exists ? undefined : tr("目录已不存在")
  const fail = (what: string) => (ok: unknown) => { if (!ok) setNote(tr("没能{0}", [what])) }
  const reveal = () => { void shell.revealPath(cwd).then(fail(tr("打开目录"))) }
  // ⌥⌘R reveals the current session's folder.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => { if (exists && e.metaKey && e.altKey && e.code === 'KeyR') { e.preventDefault(); reveal() } }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [cwd, exists])
  return (
    <span className="path-copy-anchor">
        <Menu trigger="tint" align="under" label={tr("项目 {0}", [name])} title={tr("项目操作")}
          header={<><span className="t-medium ellipsis">{name}</span><span className="mono t-3 selectable" style={{ wordBreak: 'break-all' }}>{cwd}</span>{gone && <span className="t-amber">{gone}</span>}</>}
          items={[
            { label: revealLabel(info?.fileManager ?? 'Finder'), hint: shortcut('⌥⌘R'), hintMono: true, disabled: !exists, title: gone, onClick: reveal },
            ...(terminal ? [{ label: tr("在终端中打开"), hint: terminal, hintMono: true, disabled: !exists, title: gone, onClick: () => { void shell.openInTerminal('', cwd).then(fail(tr("打开终端"))) } }] : []),
            ...(editor ? [{ label: tr("在编辑器中打开"), hint: editor, hintMono: true, disabled: !exists, title: gone, onClick: () => { void shell.openInEditor(cwd).then(fail(tr("打开编辑器"))) } }] : []),
            { separator: true },
            { label: tr("复制路径"), onClick: () => void copy() },
          ]}>
          {Icon.folder}<span className="ellipsis">{name}</span>{Icon.chevron}
        </Menu>
      <PathCopyFeedback state={feedback} error={tr('未能复制项目路径，请重试。')} />
      {note && <Toast text={note} onDone={() => setNote(undefined)} />}
    </span>
  )
}


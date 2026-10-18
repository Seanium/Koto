import { tr } from '../i18n/core.ts'
import { shortcut } from '../ui/shortcut'
// Paginated native conversation with inline content selection and quick turn navigation.

import { createPortal } from 'react-dom'
import { createContext, forwardRef, useCallback, useContext, useEffect, useImperativeHandle, useLayoutEffect, useMemo, useRef, useState, type ReactNode, type RefObject } from 'react'
import { captureScrollAnchor } from '../scrollAnchor'
import { shell } from '../api/client'
import { clockTime, oneLine, type ToolStep, type Turn, type TurnImage, type TurnFile } from '../api/model'
import { Button, Icon, Skeleton } from '../ui'
import { SelectionContext, SelectionCheck, replyIDs } from './ContentSelection'
import { acquireMedia } from '../media'

/** The sticky bar's height: a turn jumped to sits right under it. */
const BAR = 46

export interface TimelineHandle {
  /** Scroll to a turn (1-based) and flash it. */
  jumpToTurn: (turn: number) => void
}

export const Timeline = forwardRef<TimelineHandle, {
  turns: Turn[]; scroller: RefObject<HTMLDivElement | null>
  /** Changes when another session is shown: open at its latest turn again. */
  resetKey: string
  /** All turns of the conversation; `turns` holds the loaded ones (the last pages), from `firstTurn`. */
  totalTurns: number; hasEarlier: boolean; loadEarlier: (untilTurn?: number) => Promise<void>
  /** Finished turns eligible for explicit copy selection. */
  completeTurns: number
}>(function Timeline({ turns, scroller, resetKey, totalTurns, hasEarlier, loadEarlier, completeTurns }, ref) {
  const [flash, setFlash] = useState<number>()
  const [visible, setVisible] = useState<{ first: number; last: number }>({ first: totalTurns, last: totalTurns })
  const [atBottom, setAtBottom] = useState(true)
  const [unseen, setUnseen] = useState(0)
  const [search, setSearch] = useState<{ q: string; i: number } | undefined>()
  const [earlier, setEarlier] = useState(false)
  const lastTurn = useRef(turns.at(-1)?.index ?? 0)
  const pendingJump = useRef<number | undefined>(undefined)
  const loadingEarlier = useRef(false)
  const allowAutomaticPage = useRef(true)
  // Just opened: the page is still settling at its latest turn; do not page in earlier turns yet.
  const settling = useRef(0)
  const box = useRef<HTMLDivElement>(null)
  const flashTimer = useRef<ReturnType<typeof setTimeout>>(undefined)
  const generation = useRef(0)
  const frames = useRef(new Set<number>())
  const nextFrame = useCallback((action: () => void) => {
    const frame = requestAnimationFrame(() => { frames.current.delete(frame); action() })
    frames.current.add(frame)
  }, [])
  useEffect(() => {
    generation.current++; pendingJump.current = undefined; loadingEarlier.current = false; allowAutomaticPage.current = true; setEarlier(false)
    return () => { generation.current++; clearTimeout(flashTimer.current); for (const frame of frames.current) cancelAnimationFrame(frame); frames.current.clear() }
  }, [resetKey])

  // Another session: from its latest turn.
  useLayoutEffect(() => {
    setSearch(undefined); setUnseen(0); lastTurn.current = turns.at(-1)?.index ?? 0
    // On a first mount the pane's own ref is attached after this runs: find it from inside.
    const s = scroller.current ?? box.current?.closest<HTMLDivElement>('.scroll') ?? null
    if (!s) return
    // What sits above the timeline (the Agent rows, folded blocks) settles after this; stay at the
    // bottom while it does, until the user scrolls or a moment has passed.
    let user = false
    settling.current = Date.now() + 1500
    const toEnd = () => { if (!user) s.scrollTop = s.scrollHeight }
    const stop = () => { user = true; settling.current = 0 }
    const frame = requestAnimationFrame(toEnd)
    const ro = new ResizeObserver(toEnd)
    for (const c of Array.from(s.children)) ro.observe(c)
    s.addEventListener('wheel', stop, { passive: true }); s.addEventListener('keydown', stop); s.addEventListener('pointerdown', stop)
    const t = setTimeout(() => ro.disconnect(), 1500)
    return () => { cancelAnimationFrame(frame); clearTimeout(t); ro.disconnect(); s.removeEventListener('wheel', stop); s.removeEventListener('keydown', stop); s.removeEventListener('pointerdown', stop) }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [resetKey])

  // New turns at the end: follow them when at the bottom, else count them for 「↓ N 条新消息」.
  useEffect(() => {
    const end = turns.at(-1)?.index ?? 0
    const added = end - lastTurn.current
    lastTurn.current = end
    if (added <= 0) return
    if (atBottom) nextFrame(() => { const s = scroller.current; if (s) s.scrollTo({ top: s.scrollHeight, behavior: 'smooth' }) })
    else setUnseen(n => n + added)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [turns.at(-1)?.index])

  /** Put a turn at the top of the view (under the bar) and flash it; earlier pages load first when needed. */
  const scrollToTurn = useCallback((turn: number) => {
    const el = document.getElementById(`turn-${turn}`)
    if (!el) return false
    el.scrollIntoView({ block: 'start' })
    setFlash(turn)
    clearTimeout(flashTimer.current)
    flashTimer.current = setTimeout(() => setFlash(f => f === turn ? undefined : f), 1500)
    return true
  }, [])
  const jumpToTurn = useCallback((turn: number) => {
    if (turn < 1 || turn > totalTurns) return
    if (scrollToTurn(turn)) return
    const currentGeneration = generation.current
    pendingJump.current = turn
    allowAutomaticPage.current = false
    setEarlier(true)
    void loadEarlier(turn).catch(() => {}).finally(() => {
      if (generation.current !== currentGeneration) return
      nextFrame(() => { if (pendingJump.current === turn) { pendingJump.current = undefined; scrollToTurn(turn) }; setEarlier(false) })
    })
  }, [totalTurns, scrollToTurn, loadEarlier, nextFrame])
  // A jump waiting for its page: scroll once the turn is rendered.
  useEffect(() => {
    const t = pendingJump.current
    if (t !== undefined && document.getElementById(`turn-${t}`)) { pendingJump.current = undefined; nextFrame(() => scrollToTurn(t)) }
  }, [turns, scrollToTurn, nextFrame])
  useImperativeHandle(ref, () => ({ jumpToTurn }), [jumpToTurn])

  /** The turn at the top of the view, and whether its start is above the bar (partly scrolled past). */
  const current = useCallback((): { turn: number; cut: boolean } | undefined => {
    const s = scroller.current
    if (!s) return undefined
    const top = s.getBoundingClientRect().top + BAR
    for (const el of box.current?.querySelectorAll<HTMLElement>('[data-turn]') ?? []) {
      const r = el.getBoundingClientRect()
      if (r.bottom > top + 1) return { turn: Number(el.dataset.turn), cut: r.top < top - 2 }
    }
    return undefined
  }, [scroller])
  const prev = useCallback(() => { const c = current(); if (c) jumpToTurn(c.cut ? c.turn : c.turn - 1) }, [current, jumpToTurn])
  const next = useCallback(() => { const c = current(); if (c) jumpToTurn(c.turn + 1) }, [current, jumpToTurn])

  // Where we are: the turns in view, whether at the bottom, and loading earlier turns near the top.
  useEffect(() => {
    const s = scroller.current
    if (!s) return
    let raf = 0
    const measure = () => {
      raf = 0
      const top = s.getBoundingClientRect().top + BAR
      let first = 0, last = 0
      for (const el of box.current?.querySelectorAll<HTMLElement>('[data-turn]') ?? []) {
        const r = el.getBoundingClientRect()
        if (r.bottom > top && r.top < top - BAR + s.clientHeight) { const n = Number(el.dataset.turn); if (!first) first = n; last = n }
      }
      if (first) setVisible({ first, last })
      const bottom = s.scrollHeight - s.scrollTop - s.clientHeight < 80
      setAtBottom(bottom)
      if (bottom) setUnseen(0)
      if (hasEarlier && allowAutomaticPage.current && Date.now() > settling.current && !loadingEarlier.current && pendingJump.current === undefined && box.current && s.scrollTop < box.current.offsetTop + 300) {
        allowAutomaticPage.current = false
        loadingEarlier.current = true
        setEarlier(true)
        const restoreAnchor = captureScrollAnchor(s, '[id^="turn-"]'), currentGeneration = generation.current
        void loadEarlier().catch(() => {}).finally(() => {
          if (generation.current !== currentGeneration) { restoreAnchor(); return }
          nextFrame(() => {
            restoreAnchor()
            loadingEarlier.current = false; setEarlier(false)
          })
        })
      }
    }
    const onScroll = () => { if (!raf) raf = requestAnimationFrame(measure) }
    const onIntent = () => { allowAutomaticPage.current = true }
    const inputs = ['wheel', 'touchstart', 'pointerdown', 'keydown'] as const
    inputs.forEach(event => s.addEventListener(event, onIntent, { passive: true }))
    s.addEventListener('scroll', onScroll, { passive: true })
    measure()
    return () => { s.removeEventListener('scroll', onScroll); inputs.forEach(event => s.removeEventListener(event, onIntent)); cancelAnimationFrame(raf) }
  }, [scroller, hasEarlier, loadEarlier, turns])

  // ⌘F search · ⌘↑ / ⌘↓ your previous / next message.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (document.querySelector('.sheet')) return
      const mod = e.metaKey || e.ctrlKey
      if (mod && e.key.toLowerCase() === 'f') { e.preventDefault(); setSearch(s => s ?? { q: '', i: 0 }); nextFrame(() => document.getElementById('tl-search')?.focus()) }
      else if (mod && e.key === 'ArrowUp' && totalTurns > 1) { e.preventDefault(); prev() }
      else if (mod && e.key === 'ArrowDown' && totalTurns > 1) { e.preventDefault(); next() }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [totalTurns, prev, next])

  const q = search?.q.trim().toLowerCase() ?? ''
  const hits = useMemo(() => q ? turns.filter(t => turnText(t).toLowerCase().includes(q)).map(t => t.index) : [], [turns, q])
  useEffect(() => { if (hits.length && search) jumpToTurn(hits[Math.min(search.i, hits.length - 1)]) }, [q, search?.i]) // eslint-disable-line react-hooks/exhaustive-deps
  const step = (d: number) => setSearch(s => s && hits.length ? { ...s, i: (s.i + d + hits.length) % hits.length } : s)

  if (!turns.length) return <div className="t-meta">{tr("没有内容")}</div>
  const long = totalTurns > 1
  return (
    <div className="col" style={{ position: 'relative' }}>
      {long && (
        <div className="tl-top no-drag">
          {search ? (
            <div className="row g2 grow">
              {Icon.search}
              <input id="tl-search" className="tl-search" value={search.q} placeholder={hasEarlier ? tr("在已加载的第 {0}–{1} 轮里搜索", [turns[0].index, turns.at(-1)!.index]) : tr("在这个会话里搜索")} spellCheck={false}
                onChange={e => setSearch({ q: e.target.value, i: 0 })}
                onKeyDown={e => { if (e.key === 'Enter') { e.preventDefault(); step(e.shiftKey ? -1 : 1) } if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); setSearch(undefined) } }} />
              <span className="t-meta" style={{ minWidth: 48, textAlign: 'right' }}>{q ? (hits.length ? `${Math.min(search.i, hits.length - 1) + 1} / ${hits.length}` : '0 / 0') : ''}</span>
              <Button variant="ghost" icon aria-label={tr("上一个")} disabled={!hits.length} onClick={() => step(-1)}>{Icon.left}</Button>
              <Button variant="ghost" icon aria-label={tr("下一个")} disabled={!hits.length} onClick={() => step(1)}>{Icon.right}</Button>
              <Button variant="ghost" icon aria-label={tr("关闭搜索")} onClick={() => setSearch(undefined)}>{Icon.close}</Button>
            </div>
          ) : (
            <>
              <span className="t-meta grow">{tr("第 ")}{atBottom ? visible.last : visible.first} / {totalTurns} {tr(" 轮")}</span>
              <Button variant="ghost" disabled={visible.first <= 1 && !hasEarlier} onClick={prev} title={shortcut(tr("上一条你的消息（⌘↑）"))}>{Icon.up}{tr("上一条")}</Button>
              <Button variant="ghost" disabled={atBottom} onClick={next} title={shortcut(tr("下一条你的消息（⌘↓）"))}>{Icon.down}{tr("下一条")}</Button>
              <Button variant="ghost" onClick={() => { setSearch({ q: '', i: 0 }); nextFrame(() => document.getElementById('tl-search')?.focus()) }} title={shortcut(tr("在这个会话里搜索（⌘F）"))}>{Icon.search}{tr("搜索")}</Button>
            </>
          )}
        </div>
      )}
      <div ref={box} className="col g5 selectable" style={{ paddingTop: 4 }}>
        {hasEarlier && (
          <div className="row g2" style={{ alignSelf: 'center', minHeight: 26 }}>
            {earlier ? <span className="t-meta">{tr("正在加载更早的轮次…")}</span>
              : <Button variant="ghost" onClick={() => {
                if (loadingEarlier.current) return
                allowAutomaticPage.current = false
                const g = generation.current; loadingEarlier.current = true; setEarlier(true)
                const restoreAnchor = captureScrollAnchor(scroller.current, '[id^="turn-"]')
                void loadEarlier().catch(() => {}).finally(() => {
                  if (g !== generation.current) { restoreAnchor(); return }
                  nextFrame(() => { restoreAnchor(); loadingEarlier.current = false; setEarlier(false) })
                })
              }}>{turns[0].partial ? tr("显示第 {0} 轮的前半段", [turns[0].index]) : tr("显示更早的轮次（还有 {0} 轮）", [turns[0].index - 1])}</Button>}
          </div>
        )}
        {turns.map(t => (
          <TurnView key={t.index} t={t} flash={flash === t.index} done={t.index <= completeTurns}
            hit={q && hits.includes(t.index) ? q : ''} />
        ))}
      </div>
      {totalTurns >= 12 && <TurnBar total={totalTurns} turns={turns} visible={visible} onJump={jumpToTurn} scroller={scroller} />}
      {!atBottom && (
        <button type="button" className="to-latest no-drag" onClick={() => { const s = scroller.current; if (s) s.scrollTo({ top: s.scrollHeight, behavior: 'smooth' }); setUnseen(0) }}>
          {Icon.down}{unseen ? tr("{0} 条新消息", [unseen]) : tr("回到最新")}
        </button>
      )}
    </div>
  )
})

const turnText = (t: Turn) => [t.user, ...t.steps.map(s => s.kind === 'tool' ? `${s.tool.call} ${s.tool.result ?? ''}` : s.kind === 'image' ? '' : s.kind === 'file' ? (s.file.name ?? s.file.mediaType) : s.text)].join('\n')

function TurnView({ t, flash, done, hit }: {
  t: Turn; flash: boolean; done: boolean; hit: string
}) {
  const selection = useContext(SelectionContext)
  const eligible = !t.partial && done && (!selection || t.index <= selection.state.through)
  return (
    <div id={`turn-${t.index}`} data-turn={t.index} className={`col g4 turn ${flash ? 'turn-flash' : ''}`}
      data-selection-unit={selection && eligible ? 'turn' : undefined} data-selection-events={selection?.events(t.eventIds).join(' ')} data-selecting={!!selection || undefined} title={done ? undefined : tr("这一轮还没结束")}>
      {!t.partial && <div className="turn-label mono"><SelectionCheck scope="turn" events={t.eventIds} label={tr("第 {0} 轮 · 整轮", [t.index])} disabled={!eligible} />{tr("第 ")}{t.index} {tr(" 轮")}{!done && <span className="turn-label-hint"> {tr(" · 这一轮还没结束")}</span>}</div>}
      {t.partial ? <div className="switch-line">{tr("第 ")}{t.index} {tr(" 轮很长，这里是它的后半段")}</div> : t.promptId ? <div className="msg-you" data-content-selected={eligible && selection?.has(t.promptId) || undefined} title={t.time ? clockTime(t.time) : undefined}>
        <SelectionCheck events={[t.promptId]} label={tr("提问")} disabled={!eligible} />
        {t.imagesFirst && t.images.length > 0 && <Images images={t.images} />}
        {t.user && <Folded text={t.user} lines={8} keep={6} hit={hit} />}
        {!t.imagesFirst && t.images.length > 0 && <Images images={t.images} />}
        {t.files.map((f, i) => <FileCard key={i} file={f} />)}
      </div> : <span className="t-meta">{tr("仅回复片段")}</span>}
      {t.steps.length > 0 && (
        <div className="msg-reply" data-content-selected={eligible && replyIDs(t).some(id => selection?.has(id)) || undefined}>
          <div className="body col g2"><SelectionCheck events={replyIDs(t)} label={tr("回复（含工具记录）")} disabled={!eligible} />{groupSteps(t.steps).map((g, i) => <StepView key={i} g={g} hit={hit} />)}</div>
        </div>
      )}
    </div>
  )
}

type Group = { kind: 'text'; text: string } | { kind: 'reasoning'; text: string } | { kind: 'tools'; tools: ToolStep[] } | { kind: 'images'; images: TurnImage[] } | { kind: 'file'; file: TurnFile }

/** Consecutive tool calls become one group. */
function groupSteps(steps: Turn['steps']): Group[] {
  const out: Group[] = []
  for (const s of steps) {
    const last = out.at(-1)
    if (s.kind === 'tool') {
      if (last?.kind === 'tools') last.tools.push(s.tool)
      else out.push({ kind: 'tools', tools: [s.tool] })
    } else if (s.kind === 'image') {
      if (last?.kind === 'images') last.images.push(s.image)
      else out.push({ kind: 'images', images: [s.image] })
    } else out.push(s)
  }
  return out
}

function StepView({ g, hit }: { g: Group; hit: string }) {
  const [open, setOpen] = useState(false)
  if (g.kind === 'text') return <RichText text={g.text} hit={hit} />
  if (g.kind === 'images') return <Images images={g.images} />
  if (g.kind === 'file') return <FileCard file={g.file} />
  if (g.kind === 'reasoning') {
    return (
      <div className="col g1">
        <button type="button" className="fold-head no-drag" aria-expanded={open} onClick={() => setOpen(o => !o)}><span className="disclose" data-open={open || undefined}>{Icon.disclosure}</span>{tr("思考过程")}</button>
        {open && <div className="t-3" style={{ whiteSpace: 'pre-wrap', paddingLeft: 16 }}>{g.text}</div>}
      </div>
    )
  }
  if (g.tools.length === 1) return <ToolLine tool={g.tools[0]} />
  const names = [...new Set(g.tools.map(t => t.name))]
  return (
    <div className="col g1">
      <button type="button" className="fold-head no-drag" aria-expanded={open} onClick={() => setOpen(o => !o)}>
        <span className="disclose" data-open={open || undefined}>{Icon.disclosure}</span>
        {tr(" 运行了 ")}{g.tools.length} {tr(" 个工具 · ")}<span className="ellipsis">{names.slice(0, 4).join(', ')}{names.length > 4 ? ' …' : ''}</span>
        {g.tools.some(t => t.outcome === 'error') && <span className="t-red">{tr("有失败")}</span>}
      </button>
      {open && <div className="col g1" style={{ paddingLeft: 16 }}>{g.tools.map(t => <ToolLine key={t.callId} tool={t} />)}</div>}
    </div>
  )
}

// ---------------------------------------------------------------- pictures

/** Where the timeline's pictures come from: their blob by sha256, as a data: URL (set by the pane). */
export const ImageSource = createContext<((sha: string, signal?: AbortSignal) => Promise<string>) | undefined>(undefined)

/** Thumbnails in a row; a click opens the picture full size (Esc or a click closes it). */
function Images({ images }: { images: TurnImage[] }) {
  return <div className={`msg-images${images.length > 4 ? ' msg-images-many' : ''}`}>{images.map((im, i) => <Thumb key={`${im.sha}${i}`} image={im} />)}</div>
}

function FileCard({ file }: { file: TurnFile }) {
  const source = useContext(ImageSource)
  const [src, setSrc] = useState<string>()
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [open, setOpen] = useState(false)
  const [text, setText] = useState('')
  const lease = useRef<ReturnType<typeof acquireMedia>>(undefined)
  const mounted = useRef(true)
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; lease.current?.release() } }, [])
  const close = () => { setOpen(false); setSrc(undefined); setText(''); lease.current?.release(); lease.current = undefined }
  const canPreview = /^(text\/|image\/|audio\/|video\/|application\/(pdf|json|xml))/.test(file.mediaType)
  const preview = async () => {
    if (!file.sha || !source) return
    setBusy(true); setError('')
    lease.current?.release()
    const resource = acquireMedia(source, file.sha); lease.current = resource
    try {
      const url = await resource.promise
      if (!mounted.current || lease.current !== resource) return
      const content = /^(text\/|application\/(json|xml))/.test(file.mediaType) ? await (await fetch(url)).text() : ''
      if (!mounted.current || lease.current !== resource) return
      setText(content); setSrc(url); setOpen(true)
    } catch (e) { resource.release(); if (mounted.current) setError((e as Error).message) } finally { if (mounted.current) setBusy(false) }
  }
  useEffect(() => { if (!open) return; const key = (e: KeyboardEvent) => { if (e.key === 'Escape') { e.stopPropagation(); close() } }; window.addEventListener('keydown', key, true); return () => window.removeEventListener('keydown', key, true) }, [open])
  return <div className="attachment no-drag" onClick={e => e.stopPropagation()}>
    <span className="attachment-icon">{Icon.file}</span><span className="grow min0"><span className="ellipsis block">{file.name || tr("附件")}</span><span className="t-meta">{file.mediaType || tr("文件")}{!file.sha ? file.uri ? tr(" · 远程引用") : tr(" · 内容不可用") : ''}</span></span>
    {canPreview && file.sha && <Button small disabled={busy} onClick={() => void preview()}>{busy ? tr("加载中…") : tr("预览")}</Button>}
    {error && <span className="t-meta">{error}</span>}
    {open && src && createPortal(<div className="attachment-overlay no-drag" role="dialog" aria-label={file.name || tr("附件预览")} onClick={close}><div className="attachment-preview" onClick={e => e.stopPropagation()}><div className="row g2"><span className="grow ellipsis">{file.name || tr("附件")}</span><Button small onClick={close}>{tr("关闭")}</Button></div>
      {file.mediaType.startsWith('image/') ? <img src={src} alt={file.name} /> : file.mediaType.startsWith('audio/') ? <audio src={src} controls /> : file.mediaType.startsWith('video/') ? <video src={src} controls /> : file.mediaType === 'application/pdf' ? <iframe src={src} title={file.name || 'PDF'} /> : <pre>{text}</pre>}
    </div></div>, document.body)}
  </div>
}

function Thumb({ image }: { image: TurnImage }) {
  const source = useContext(ImageSource)
  const [src, setSrc] = useState<string>()
  const [failed, setFailed] = useState(false)
  const [big, setBig] = useState(false)
  const [near, setNear] = useState(false)
  const [size, setSize] = useState<{ width: number; height: number }>()
  const host = useRef<HTMLSpanElement>(null)
  useEffect(() => {
    const observer = new IntersectionObserver(entries => setNear(entries[0].isIntersecting), { rootMargin: '200px' })
    if (host.current) observer.observe(host.current)
    return () => observer.disconnect()
  }, [])
  const close = () => { setBig(false); host.current?.querySelector<HTMLElement>('[role="button"]')?.focus({ preventScroll: true }) }
  const needed = near || big
  useEffect(() => {
    if (!source || !needed) { setSrc(undefined); return }
    let alive = true
    const resource = acquireMedia(source, image.sha)
    resource.promise.then(u => { if (alive) { setSrc(u); setFailed(false) } }, () => alive && setFailed(true))
    return () => { alive = false; resource.release() }
  }, [image.sha, source, needed])
  useEffect(() => {
    if (!big) return
    const esc = (e: KeyboardEvent) => { if (e.key === 'Escape') { e.stopPropagation(); close() } }
    window.addEventListener('keydown', esc, true)
    return () => window.removeEventListener('keydown', esc, true)
  }, [big])
  return (
    <span ref={host} className="msg-image-slot">
      {failed ? <span className="msg-img msg-img-missing" title={image.name}>{tr("图片读不到了")}</span> : src ? <img className="msg-img no-drag" role="button" tabIndex={0} src={src} alt={image.name ?? tr("图片")} title={image.name} onLoad={e => { const r = e.currentTarget.getBoundingClientRect(); setSize({ width: r.width, height: r.height }) }} onClick={() => setBig(true)} onKeyDown={e => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); setBig(true) } }} /> : <span className="msg-img msg-img-loading" style={size} aria-label={tr("图片")} />}
      {big && createPortal(<div className="img-overlay no-drag" role="dialog" aria-label={tr("图片")} onClick={close}><img src={src} alt={image.name ?? tr("图片")} /></div>, document.body)}
    </span>
  )
}

function ToolLine({ tool }: { tool: ToolStep }) {
  const [open, setOpen] = useState(false)
  return (
    <div className={`code no-drag${open ? ' code-open' : ''}`} role="button" tabIndex={0} title={open ? undefined : tool.result} onClick={() => setOpen(o => !o)} onKeyDown={e => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); setOpen(o => !o) } }} style={{ cursor: 'pointer' }}>
      <span className={open ? '' : 'ellipsis'} style={{ maxWidth: open ? '100%' : '60%', minWidth: 0, color: 'var(--ink)', overflowWrap: 'anywhere' }}>{tool.call}</span>
      {tool.result && <span className={open ? '' : 'ellipsis grow'} style={{ minWidth: 0, flexBasis: open ? '100%' : undefined, overflowWrap: 'anywhere' }}>→ {tool.result}</span>}
      {tool.outcome === 'error' && <span className="t-red">{tr("失败")}</span>}
    </div>
  )
}

/** Text with ``` code blocks: prose folds past 30 lines (12 kept); code scrolls inside 320px. */
function RichText({ text, hit }: { text: string; hit: string }) {
  const parts = useMemo(() => splitFences(text), [text])
  return <>{parts.map((p, i) => p.code !== undefined ? <CodeBlock key={i} lang={p.lang} code={p.code} hit={hit} /> : <Folded key={i} text={p.text} lines={30} keep={12} hit={hit} />)}</>
}

function splitFences(text: string): { text: string; code?: string; lang?: string }[] {
  const out: { text: string; code?: string; lang?: string }[] = []
  const re = /```([\w+-]*)\n([\s\S]*?)```/g
  let at = 0
  for (let m; (m = re.exec(text));) {
    if (m.index > at) out.push({ text: text.slice(at, m.index).replace(/\n+$/, '') })
    out.push({ text: '', code: m[2].replace(/\n$/, ''), lang: m[1] })
    at = m.index + m[0].length
  }
  if (at < text.length) out.push({ text: text.slice(at).replace(/^\n+/, '') })
  return out.filter(p => p.code !== undefined || p.text.trim())
}

function CodeBlock({ lang, code, hit }: { lang?: string; code: string; hit: string }) {
  const [copied, setCopied] = useState(false)
  const lines = code.split('\n').length
  const label = lang === 'diff' || /^(diff --git|@@ )/m.test(code) ? tr("diff · {0} 行", [lines]) : lang || tr("{0} 行", [lines])
  return (
    <div className="codeblock">
      <div className="codeblock-head row"><span className="grow">{label}</span>
        <button type="button" className="btn-link no-drag" onClick={() => { void shell.copy(code).then(ok => { if (ok) { setCopied(true); setTimeout(() => setCopied(false), 1500) } }) }}>{copied ? tr("已复制") : tr("复制")}</button>
      </div>
      <pre className="codeblock-body">{mark(code, hit)}</pre>
    </div>
  )
}

/** Long text: the first `keep` lines, faded, and 展开全文（还有 N 行）; 收起 at the end once open. */
function Folded({ text, lines, keep, hit }: { text: string; lines: number; keep: number; hit: string }) {
  const [open, setOpen] = useState(false)
  const all = text.split('\n')
  const folds = all.length > lines
  useEffect(() => { if (hit && folds && text.toLowerCase().includes(hit)) setOpen(true) }, [hit]) // eslint-disable-line react-hooks/exhaustive-deps
  if (!folds) return <div style={{ whiteSpace: 'pre-wrap' }}>{mark(text, hit)}</div>
  return (
    <div className="col g1">
      <div className={open ? '' : 'fade-out'} style={{ whiteSpace: 'pre-wrap' }}>{mark(open ? text : all.slice(0, keep).join('\n'), hit)}</div>
      <button type="button" className="btn-link no-drag" style={{ alignSelf: 'flex-start' }} onClick={() => setOpen(o => !o)}>{open ? tr("收起") : tr("展开全文（还有 {0} 行）", [all.length - keep])}</button>
    </div>
  )
}

function mark(text: string, hit: string): ReactNode {
  if (!hit) return text
  const low = text.toLowerCase()
  const out: ReactNode[] = []
  let at = 0
  for (let i = low.indexOf(hit); i >= 0; i = low.indexOf(hit, i + hit.length)) {
    out.push(text.slice(at, i), <mark key={i}>{text.slice(i, i + hit.length)}</mark>)
    at = i + hit.length
  }
  out.push(text.slice(at))
  return out
}

/**
 * The outline of a long conversation, floating at the left edge of the conversation (like Codex): one
 * dash per turn, the turns in view darker,  Hovering opens the list of prompts; a click
 * jumps there. It sits at the left so it never meets the scroll bar on the right.
 */
function TurnBar({ total, turns, visible, onJump, scroller }: { total: number; turns: Turn[]; visible: { first: number; last: number }; onJump: (t: number) => void; scroller: RefObject<HTMLDivElement | null> }) {
  const [open, setOpen] = useState(false)
  const [host, setHost] = useState<HTMLElement | null>(null)
  const [height, setHeight] = useState(0)
  const list = useRef<HTMLDivElement>(null)
  const close = useRef<ReturnType<typeof setTimeout>>(undefined)
  useEffect(() => () => clearTimeout(close.current), [])
  // Drawn in the conversation pane's overlay (beside the scrolling area, so it stays put). After
  // commit, when the pane's ref is attached.
  useEffect(() => {
    const h = scroller.current?.parentElement?.querySelector<HTMLElement>(':scope > .outline-host') ?? null
    setHost(h)
    if (!h) return
    const ro = new ResizeObserver(() => setHeight(h.clientHeight))
    ro.observe(h)
    setHeight(h.clientHeight)
    return () => ro.disconnect()
  }, [scroller])
  useEffect(() => {
    if (open) list.current?.querySelector<HTMLElement>('[data-here]')?.scrollIntoView({ block: 'center' })
  }, [open])
  if (!host || !height) return null
  const box = { height: Math.max(1, height - 8) }
  // At most ~120 dashes: a long session samples them; each still jumps to its own turn.
  const maxHeight = Math.min(360, box.height * .65)
  const every = Math.max(1, Math.ceil(total / Math.max(1, Math.min(120, Math.floor(maxHeight / 4)))))
  const dashes = Array.from({ length: Math.ceil(total / every) }, (_, i) => i * every + 1)
  const gap = Math.min(10, maxHeight / Math.max(1, dashes.length))
  const byIndex = new Map(turns.map(t => [t.index, t]))
  const here = (n: number) => n >= visible.first && n <= visible.last
  const show = () => { clearTimeout(close.current); setOpen(true) }
  const hide = () => { clearTimeout(close.current); close.current = setTimeout(() => setOpen(false), 180) }
  return (
    createPortal(<div className="outline no-drag" style={{ maxHeight: box.height }} onMouseEnter={show} onMouseLeave={hide}>
      <div className="outline-dashes" aria-hidden style={{ height: dashes.length * gap }}>
        {dashes.map((n, i) => (
          <span key={n} className="outline-dash" data-here={here(n) || undefined} style={{ top: i * gap }} />
        ))}
      </div>
      {open && (
        <div ref={list} className="outline-list menu" role="menu" aria-label={tr("轮次目录")} style={{ maxHeight: box.height }}>
          {Array.from({ length: total }, (_, i) => i + 1).map(n => {
            const t = byIndex.get(n)
            return (
              <button key={n} type="button" role="menuitem" className="outline-item row g2" data-here={here(n) || undefined} onClick={() => { onJump(n); setOpen(false) }}>
                <span className="mono t-meta outline-n">{n}</span>
                <span className="grow ellipsis">{t ? oneLine(t.user, 60) || tr("（没有文字）") : <span className="t-meta" title={tr("还没加载，点一下跳过去")}>{tr("第 ")}{n} {tr(" 轮")}</span>}</span>
              </button>
            )
          })}
        </div>
      )}
    </div>, host)
  )
}

export function TimelineSkeleton() {
  return (
    <div className="col g5">
      {[0, 1].map(i => (
        <div key={i} className="col g4">
          <div style={{ alignSelf: 'flex-end' }}><Skeleton w={220} h={32} /></div>
          <div className="row g3" style={{ alignItems: 'flex-start' }}>
            <Skeleton w={18} h={18} />
            <div className="col g2 grow"><Skeleton w="80%" /><Skeleton w="55%" /></div>
          </div>
        </div>
      ))}
    </div>
  )
}


/** Loaded turn content, shared by the list inspector and read-only session comparison. */
export function TurnPreview({ turn: t }: { turn: Turn }) {
  const selection = useContext(SelectionContext)
  const eligible = !t.partial && (!selection || t.index <= selection.state.through)
  return <div className="col g3">
    {t.promptId ? <div className="msg-you" data-content-selected={eligible && selection?.has(t.promptId) || undefined}><SelectionCheck events={[t.promptId]} label={tr("提问")} disabled={!eligible} /><Folded text={t.user} lines={12} keep={10} hit="" />{t.images.length > 0 && <Images images={t.images} />}{t.files.map((f,i) => <FileCard key={i} file={f} />)}</div> : <span className="t-meta">{tr("仅回复片段")}</span>}
    <div className="preview-reply col g3" data-content-selected={eligible && replyIDs(t).some(id => selection?.has(id)) || undefined}><SelectionCheck events={replyIDs(t)} label={tr("回复（含工具记录）")} disabled={!eligible} />{groupSteps(t.steps).map((g, i) => <StepView key={i} g={g} hit="" />)}</div>
  </div>
}

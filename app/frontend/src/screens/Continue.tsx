import { tr } from '../i18n/core.ts'
// Copies an explicit, frozen selection to a new native session.
import { useEffect, useRef, useState } from 'react'
import { api, copyFailure, shell } from '../api/client'
import { lossSentences, writable } from '../api/model'
import type { CarryResult } from '../api/types'
import type { Hub } from '../store'
import { AgentDisc, agentName, Button, Kbd, ListRow, OperationError, Sheet } from '../ui'

export interface CopySource { selector: string; eventIds?: string[]; excludeEventIds?: string[]; label: string }
export function ContinueSheet({ onClose, hub, title, source, onDone, onOpen }: {
  onClose: () => void; hub: Hub; title: string; source: CopySource; onDone: () => void; onOpen: (id: string) => Promise<void>
}) {
  const [frozen] = useState(source)
  const [pick, setPick] = useState(() => hub.connectors.find(c => c.enabled && writable(c))?.id ?? '')
  const [cwd, setCwd] = useState<string>()
  const [preview, setPreview] = useState<{ key: string; plan: CarryResult }>()
  const [result, setResult] = useState<CarryResult>()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()
  const [retry, setRetry] = useState(0)
  const previewKey = JSON.stringify([pick, cwd, retry])
  const plan = preview?.key === previewKey ? preview.plan : undefined
  const request = useRef('')
  const targets = hub.connectors.filter(c => c.enabled && writable(c))
  const target = targets.find(c => c.id === pick)
  const range = frozen.label
  useEffect(() => {
    if (!pick) return
    let alive = true
    const id = crypto.randomUUID()
    request.current = id
    setError(undefined)
    api.carry({ from: frozen.selector, eventIds: frozen.eventIds, excludeEventIds: frozen.excludeEventIds, to: pick, cwd, title: `${title} · ${range}`, requestId: request.current, dryRun: true }).then(r => {
      if (alive) setPreview({ key: previewKey, plan: r }); else void api.forgetCopy(id)
    }, e => { if (alive) setError((e as Error).message) })
    return () => { alive = false; void api.forgetCopy(id) }
  }, [frozen, pick, cwd, retry]) // eslint-disable-line react-hooks/exhaustive-deps
  const go = async () => {
    if (!frozen || !target || busy || result || !plan || plan.plan.mode === 'reject') return
    setBusy(true); setError(undefined)
    try {
      const r = await api.carry({ from: frozen.selector, eventIds: frozen.eventIds, excludeEventIds: frozen.excludeEventIds, to: pick, cwd, title: `${title} · ${range}`, requestId: request.current })
      if (!r.applied) throw Error(r.plan.notes.join('; ') || tr("目标未写入，请重试"))
      setResult(r); onDone()
    } catch (e) { setError((e as Error).message) } finally { setBusy(false) }
  }
  useEffect(() => {
    const key = (e: KeyboardEvent) => { if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) { e.preventDefault(); void go() } }
    window.addEventListener('keydown', key); return () => window.removeEventListener('keydown', key)
  })
  const chooseFolder = async () => { const dir = await shell.pickDirectory(tr("选择新会话的项目文件夹")); if (dir) setCwd(dir) }
  const openResult = async () => {
    if (!result || busy) return
    setBusy(true); setError(undefined)
    const id = typeof result.receipt?.session === 'string' ? result.receipt.session : result.plan.target.session
    try { await onOpen(`${result.plan.harness}:${id}`) } catch (e) { setError((e as Error).message) } finally { setBusy(false) }
  }
  return <Sheet open onClose={() => { if (!busy) onClose() }} label={result ? tr("复制完成") : tr("复制{0}到…", [range])}>
    <div className="sheet-head"><div className="t-head">{result ? tr("已复制到 {0}", [agentName(target?.harness ?? '')]) : tr("复制{0}到…", [range])}</div>
      <div className="t-meta">{result ? tr("{0}已保存为新会话，可继续对话。", [range]) : tr("在目标 Agent 中新建会话，原会话保持不变。")}</div>
    </div>
    <div className="sheet-body col g3">
      {result ? <>
        <span className="t-medium">{title} · {range}</span>
        {lossSentences(result.plan.losses).length > 0 && <span className="t-meta">{lossSentences(result.plan.losses).join('；')}</span>}
      </> : <>
        <div role="listbox" aria-label={tr("选择目标 Agent")} className="col scroll" style={{ maxHeight: 320 }}>
          {targets.map(t => <ListRow key={t.id} data-id={t.id} selected={pick === t.id} disabled={busy} onClick={() => setPick(t.id)}>
            <AgentDisc harness={t.harness} size={24} /><span className="grow">{agentName(t.harness)}</span>
            {t.removable && <span className="t-meta ellipsis" title={t.root}>{t.root}</span>}
          </ListRow>)}
        </div>
        {!targets.length && <span className="t-meta">{tr("请在设置中启用可复制的 Agent。")}</span>}
        <div className="copy-plan-status t-meta scroll" role="status" aria-live="polite" aria-busy={!plan && !error}>
          {plan ? <>{lossSentences(plan.plan.losses).join('；') || tr("将复制所选范围内的完整对话。")} {tr(" 系统提示词由目标 Agent 重新加载。")}</> : error ? null : tr("正在检查复制内容…")}
        </div>
        {plan?.plan.mode === 'reject' && <OperationError message={tr("暂时无法复制")} detail={plan.plan.notes.join('; ')} />}
        <div className="row g2"><span className="grow t-meta ellipsis" title={cwd}>{tr("项目文件夹：")}{cwd ?? tr("沿用来源")}</span><Button small disabled={busy} onClick={() => void chooseFolder()}>{tr("更改…")}</Button></div>
      </>}
      {error && <OperationError message={result ? tr("复制已完成，但暂时无法打开新会话") : copyFailure(error)} detail={error} />}
      {error && !plan && <Button small onClick={() => setRetry(n => n + 1)}>{tr("重试预览")}</Button>}
    </div>
    <div className="sheet-foot">{result ? <><Button disabled={busy} onClick={onClose}>{tr("完成")}</Button><Button variant="primary" disabled={busy} onClick={() => void openResult()}>{busy ? tr("正在打开…") : tr("查看新会话")}</Button></> : <><Button disabled={busy} onClick={onClose}>{tr("取消")}</Button>
      <Button variant="primary" disabled={busy || !plan || plan.plan.mode === 'reject' || !frozen} onClick={() => void go()}>{busy ? tr("正在复制…") : error ? tr("重试复制") : tr("复制")} <Kbd>⌘↵</Kbd></Button></>}
    </div>
  </Sheet>
}

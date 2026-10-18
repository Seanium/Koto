import { tr } from '../i18n/core.ts'
// Copies an explicit, frozen selection to a new native session.
import { useEffect, useRef, useState } from 'react'
import { api, copyFailure, shell } from '../api/client'
import { copyBlocker } from '../api/copyErrors'
import { lossSentences, productName, productScopeName, tildePath } from '../api/model'
import { accountName, preferredCopyTarget, productKey } from '../api/identity'
import type { CarryResult, Loss } from '../api/types'
import type { Hub } from '../store'
import { AgentDisc, agentName, Button, Icon, Kbd, ListRow, Menu, OperationError, Popover, Sheet, type MenuItem } from '../ui'
import { orderAgentProducts } from '../ui/agentOrder'
import { useAgentPreference } from '../ui/agentPreferences'

export interface CopySource { selector: string; eventIds?: string[]; excludeEventIds?: string[]; label: string; profile?: string; harness?: string; variant?: string; account?: string; cwd?: string }
export function ContinueSheet({ onClose, hub, title, source, onDone, onOpen }: {
  onClose: () => void; hub: Hub; title: string; source: CopySource; onDone: () => void; onOpen: (id: string) => Promise<void>
}) {
  const [frozen] = useState(source)
  const ordering = useAgentPreference()
  const [destination, setDestination] = useState(() => preferredCopyTarget(hub.profiles, source))
  const [product, setProduct] = useState(() => {
    const profiles = hub.profiles.filter(p => p.writable && p.enabled)
    const selected = profiles.find(p => p.id === source.profile) ?? profiles.find(p => p.harness === source.harness && p.variant === source.variant) ?? profiles[0]
    return selected ? productKey(selected) : ''
  })
  const pick = destination.profile
  const [cwd, setCwd] = useState<string>()
  const [preview, setPreview] = useState<{ key: string; plan: CarryResult }>()
  const [result, setResult] = useState<CarryResult>()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()
  const [retry, setRetry] = useState(0)
  const previewKey = JSON.stringify([pick, destination.account, cwd, retry])
  const plan = preview?.key === previewKey ? preview.plan : undefined
  const request = useRef('')
  const targets = hub.profiles.filter(p => p.writable && hub.connectors.some(c => c.id === p.id && c.enabled))
  const target = targets.find(c => c.id === pick)
  const products = ordering.ready ? orderAgentProducts([...new Map(targets.map(p => [productKey(p), p])).values()], ordering.saved, productScopeName) : []
  const selectedProduct = target ? productKey(target) : product
  const productInfo = products.find(p => productKey(p) === selectedProduct)
  const targetProfiles = targets.filter(p => productKey(p) === selectedProduct)
  const targetAccounts = target?.targetAccounts.filter(a => a.kind === 'account') ?? []
  const accountsError = target?.targetAccountsError
  const needsAccount = !accountsError && !!target?.accountSelectable && !targetAccounts.some(a => a.id === destination.account)
  const checking = !!target && !accountsError && !needsAccount && !plan && !error
  const project = cwd ?? plan?.plan.target.cwd ?? frozen.cwd
  const losses = (result ?? plan)?.plan.losses ?? []
  const lossLines = lossSentences(losses)
  const range = frozen.label
  useEffect(() => {
    if (!target || needsAccount || accountsError) return
    let alive = true
    const controller = new AbortController()
    const id = crypto.randomUUID()
    request.current = id
    setError(undefined)
    api.carry({ from: frozen.selector, eventIds: frozen.eventIds, excludeEventIds: frozen.excludeEventIds, to: target.harness, targetProfile: pick, targetAccount: destination.account, cwd, title: `${title} · ${range}`, requestId: request.current, dryRun: true }, controller.signal).then(r => {
      if (alive) setPreview({ key: previewKey, plan: r }); else void api.forgetCopy(id)
    }, e => { if (alive) setError((e as Error).message) })
    return () => { alive = false; controller.abort(); void api.forgetCopy(id) }
  }, [frozen, pick, destination.account, cwd, retry, needsAccount, accountsError, !!target]) // eslint-disable-line react-hooks/exhaustive-deps
  const go = async () => {
    if (!frozen || !target || needsAccount || accountsError || busy || result || !plan || plan.plan.mode === 'reject') return
    setBusy(true); setError(undefined)
    try {
      const r = await api.carry({ from: frozen.selector, eventIds: frozen.eventIds, excludeEventIds: frozen.excludeEventIds, to: target.harness, targetProfile: pick, targetAccount: destination.account, cwd, title: `${title} · ${range}`, requestId: request.current })
      if (!r.applied) throw Error(r.plan.notes.join('; ') || tr("目标未写入，请重试"))
      setResult(r); onDone()
    } catch (e) { setError((e as Error).message) } finally { setBusy(false) }
  }
  useEffect(() => {
    const key = (e: KeyboardEvent) => { if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) { e.preventDefault(); void go() } }
    window.addEventListener('keydown', key); return () => window.removeEventListener('keydown', key)
  })
  const chooseFolder = async () => {
    try { const dir = await shell.pickDirectory(tr("选择新会话的项目目录")); if (dir) { setCwd(dir); setError(undefined) } }
    catch (e) { setError((e as Error).message) }
  }
  const chooseProfile = (id: string) => {
    const profile = targets.find(p => p.id === id)
    if (profile) { setDestination(preferredCopyTarget([profile], frozen)); setError(undefined) }
  }
  const addProfile = async () => {
    if (!productInfo || busy) return
    setError(undefined)
    try {
      const dir = await shell.pickDirectory(tr('选择 Agent 的会话目录'))
      if (!dir) return
      setBusy(true)
      const added = await api.addConnectorRoot(productInfo.harness, dir, productInfo.variant)
      await hub.reload()
      const profiles = await api.profiles()
      const profile = profiles.find(p => p.id === added.id)
      if (!profile) throw Error(tr('目录暂时不可用，请刷新后重试'))
      setDestination(preferredCopyTarget([profile], frozen))
    } catch (e) { setError((e as Error).message) } finally { setBusy(false) }
  }
  const openResult = async () => {
    if (!result || busy) return
    setBusy(true); setError(undefined)
    const id = typeof result.receipt?.session === 'string' ? result.receipt.session : result.plan.target.session
    try { await onOpen(`@${result.plan.target.profile}:${id}`) } catch (e) { setError((e as Error).message) } finally { setBusy(false) }
  }
  const profileItems: MenuItem[] = targetProfiles.map(p => ({ key: p.id, label: p.label || agentName(p.harness), sub: p.root, checked: p.id === pick, onClick: () => chooseProfile(p.id) }))
  if (productInfo && !(productInfo.harness === 'claude-code' && productInfo.variant !== 'cli')) {
    profileItems.push({ separator: true }, { label: tr('添加目录…'), onClick: () => void addProfile() })
  }
  return <Sheet open className="copy-sheet" onClose={() => { if (!busy) onClose() }} label={result ? tr("复制完成") : tr("复制{0}到…", [range])}>
    <div className="sheet-head"><div className="t-head">{result ? tr("已复制到 {0}", [target ? productName(target.harness, target.variant) : '']) : tr("复制{0}到…", [range])}</div>
      {result && <div className="t-meta">{tr("{0}已保存为新会话，可继续对话。", [range])}</div>}
    </div>
    <div className="sheet-body col g3">
      {result ? <>
        <span className="t-medium">{title} · {range}</span>
      </> : <>
        <div role="listbox" aria-label={tr("选择目标 Agent")} className="copy-targets col scroll" data-feedback={needsAccount || !!accountsError || !!error || plan?.plan.mode === 'reject' || undefined}>
          {products.map(p => <ListRow key={productKey(p)} data-id={productKey(p)} data-autofocus={selectedProduct === productKey(p) || undefined} selected={selectedProduct === productKey(p)} disabled={busy} onClick={() => { setProduct(productKey(p)); setDestination(preferredCopyTarget(targets.filter(x => productKey(x) === productKey(p)), frozen)); setError(undefined) }}>
            <AgentDisc harness={p.harness} size={24} title="" /><span className="grow">{productName(p.harness, p.variant)}</span>
          </ListRow>)}
        </div>
        {!targets.length && <span className="t-meta">{tr("请在设置中启用可复制的 Agent。")}</span>}
        {productInfo && <div className="copy-destination col g2">
          <div className="copy-field"><span className="t-meta copy-field-label">{tr('会话目录')}</span>
            <Menu trigger="secondary" align="under" disabled={busy} label={tr('选择目标会话目录')} items={profileItems}>
              <span className="copy-field-text">{target ? tildePath(target.root) : tr('请选择目录')}</span> {Icon.chevron}
            </Menu>
          </div>
          {target?.accountSelectable && targetAccounts.length > 0 && <div className="copy-field"><span className="t-meta copy-field-label">{tr('目标账号')}</span>
            <Menu trigger="secondary" align="under" disabled={busy || !targetAccounts.length} label={tr('选择目标账号')} items={targetAccounts.map(a => ({ key: a.id, label: accountName(a), checked: a.id === destination.account, onClick: () => { setDestination({ profile: pick, account: a.id }); setError(undefined) } }))}>
              <span className="copy-field-text">{targetAccounts.find(a => a.id === destination.account) ? accountName(targetAccounts.find(a => a.id === destination.account)!) : tr('请选择账号')}</span> {Icon.chevron}
            </Menu>
          </div>}
          <div className="copy-field"><span className="t-meta copy-field-label">{tr('项目目录')}</span>
            <Menu trigger="secondary" align="under" disabled={busy} label={tr('选择新会话的项目目录')} items={[
              { label: tr('沿用来源'), sub: frozen.cwd, checked: cwd === undefined, onClick: () => { setCwd(undefined); setError(undefined) } },
              { separator: true }, { label: tr('选择目录…'), onClick: () => void chooseFolder() },
            ]}>
              <span className="copy-field-text">{project ? tildePath(project) : tr('沿用来源')}</span> {Icon.chevron}
            </Menu>
          </div>
        </div>}
      </>}
    </div>
    {((!result && (needsAccount || accountsError || plan?.plan.mode === 'reject')) || error) && <div className="copy-feedback col g2">
      {!result && accountsError && <OperationError message={tr("无法读取目标账号")} detail={accountsError} />}
      {!result && needsAccount && <div className="copy-warning t-meta" role="status" aria-live="polite">
        {targetAccounts.length ? tr('请选择此目录中的目标账号。') : tr('此目录尚无可用账号，请先在目标 App 中创建会话。')}
      </div>}
      {!result && plan?.plan.mode === 'reject' && <OperationError {...copyBlocker(plan.plan.notes)} />}
      {error && <OperationError message={result ? tr("复制已完成，但暂时无法打开新会话") : copyFailure(error)} detail={error} />}
      {error && !plan && <Button small onClick={() => setRetry(n => n + 1)}>{tr("重试预览")}</Button>}
    </div>}
    <div className="sheet-foot">{lossLines.length > 0 && <CopyNotes key={`${result ? "result" : "preview"}:${(result ?? plan)?.plan.id ?? previewKey}`} losses={losses} lines={lossLines} />}{result ? <><Button disabled={busy} onClick={onClose}>{tr("完成")}</Button><Button variant="primary" disabled={busy} onClick={() => void openResult()}>{busy ? tr("正在打开…") : tr("查看新会话")}</Button></> : <><Button disabled={busy} onClick={onClose}>{tr("取消")}</Button>
      <Button className="copy-submit" variant="primary" disabled={busy || needsAccount || !!accountsError || !plan || plan.plan.mode === 'reject' || !frozen} aria-busy={checking || busy} onClick={() => void go()}>{busy ? tr("正在复制…") : checking ? tr('正在检查…') : error ? tr("重试复制") : tr("复制")} <Kbd>⌘↵</Kbd></Button></>}
    </div>
  </Sheet>
}

function CopyNotes({ losses, lines }: { losses: Loss[]; lines: string[] }) {
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)
  const material = losses.filter(l => !(l.field === 'reasoning' && l.note === 'empty reasoning')).some(l => l.kind === 'dropped' || l.kind === 'truncated' || (l.kind === 'approximated' && l.field !== 'role'))
  return <div ref={ref} className="copy-note" onMouseEnter={() => setOpen(true)} onMouseLeave={() => { if (!ref.current?.contains(document.activeElement)) setOpen(false) }}
    onFocusCapture={() => setOpen(true)} onBlurCapture={e => { if (!e.currentTarget.contains(e.relatedTarget)) setOpen(false) }}>
    <Popover open={open} setOpen={setOpen} width={320} label={tr('复制说明')} trigger={p => <Button {...p} variant="ghost" icon className="copy-note-trigger" data-tone={material ? 'warn' : undefined} aria-label={tr('复制说明')} onClick={() => setOpen(true)}>{Icon.info}</Button>}>
      <div className="copy-note-content t-meta col g2">{lines.map((line, i) => <div key={i}>{line}</div>)}</div>
    </Popover>
  </div>
}

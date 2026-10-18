import { languages, setLanguage, tr } from '../i18n/core.ts'
import { setAppearance } from '../appearance'
// Settings (⌘,): a view inside the main window, beside the sidebar (Esc goes back) — 通用 · Agent.

import { useEffect, useState } from 'react'
import { api, onUpdate, refreshOpeners, shell, useOpeners, type AppInfo, type Opener, type Prefs, type UpdateState } from '../api/client'
import { AGENTS, openerName, productName } from '../api/model'
import type { Connector } from '../api/types'
import type { Hub } from '../store'
import { AgentDisc, agentName, Button, Icon, ListRow, Menu, Segmented, Sheet, Skeleton, Switch } from '../ui'

export type SettingsTab = 'general' | 'agents'
export const isSettingsTab = (t: string): t is SettingsTab => t === 'general' || t === 'agents'
const PRODUCTS = [...AGENTS.map(a => ({ harness: a.id, variant: a.id === 'workbuddy' || a.id === 'workbuddy-ai' ? 'desktop' : a.id === 'zcode' ? 'shared' : 'cli' })),
  { harness: 'cursor', variant: 'ide' }]

export function Settings({ hub, tab, setTab }: { hub: Hub; tab: SettingsTab; setTab: (t: SettingsTab) => void }) {
  return (
    <div className="col grow" style={{ height: '100%', minWidth: 0, background: 'var(--bg-window)' }}>
      <div className="drag row" style={{ height: 52, justifyContent: 'center', borderBottom: '1px solid var(--divider)', flex: 'none' }}>
        <div className="row g1 no-drag" role="tablist" aria-label={tr("设置分区")}>
          {([['general', tr("通用")], ['agents', 'Agent']] as const).map(([v, label]) => (
            <Button key={v} variant="ghost" role="tab" aria-selected={tab === v} aria-pressed={tab === v} onClick={() => setTab(v)} style={{ height: 28, borderRadius: 7, padding: '0 12px', fontSize: 13 }}>{label}</Button>
          ))}
        </div>
      </div>
      <div className="col scroll grow" style={{ padding: '24px 32px 32px', width: '100%', maxWidth: 760, alignSelf: 'center' }}>
        {tab === 'general' && <General />}
        {tab === 'agents' && <Agents hub={hub} />}
      </div>
    </div>
  )
}

// ---------------------------------------------------------------- 通用

function General() {
  const [prefs, setPrefs] = useState<Prefs>()
  const [error, setError] = useState<string>()
  const [info, setInfo] = useState<AppInfo>()
  const [infoError, setInfoError] = useState<string>()
  const [noticesOpen, setNoticesOpen] = useState(false)
  useEffect(() => { void shell.prefs().then(setPrefs) }, [])
  useEffect(() => { void shell.appInfo().then(setInfo).catch(e => setInfoError(String(e))) }, [])
  const save = async (p: Prefs) => {
    const before = prefs
    setPrefs(p); setError(undefined)
    try { await shell.setPrefs(p); setLanguage(p.language); setAppearance(p.appearance); void refreshOpeners() } catch (e) { setPrefs(before); setError((e as Error).message) }
  }
  const openers = useOpeners()
  return (
    <div className="col g5">
      <div className="group">
        <Line title={tr("语言")} hint={tr("切换后立即生效")}><Menu trigger="secondary" align="under" width={220} label={tr("语言")} items={languages.map(l => ({ label: l.id === 'system' ? tr(l.name) : l.name, checked: (prefs?.language || 'system') === l.id, disabled: !prefs, onClick: () => { if (prefs) void save({ ...prefs, language: l.id }) } }))}>{prefs?.language && prefs.language !== 'system' ? languages.find(l => l.id === prefs.language)?.name : tr("跟随系统")} {Icon.chevron}</Menu></Line>
        <Line title={tr("外观")} hint={tr("浅色、深色，或跟着系统切换")}>
          {prefs ? <Segmented label={tr("外观")} value={prefs.appearance || 'system'} onChange={v => void save({ ...prefs, appearance: v === 'system' ? '' : v })}
            options={[{ value: 'system', label: tr("跟随系统") }, { value: 'light', label: tr("浅色") }, { value: 'dark', label: tr("深色") }]} /> : <Skeleton w={220} h={24} />}
        </Line>
        {prefs && openers && <>
          <OpenerLine title={tr("在终端中打开")} hint={tr("「在终端中打开」用哪个终端运行接着聊的命令")} list={openers.info.terminals} current={openers.info.terminal}
            custom={prefs.terminalCustom} placeholder={tr("例如 kitty --directory {cwd} sh -c {cmd}")} help={tr("{cmd} 是要运行的命令（已含 cd），{cwd} 是项目文件夹")}
            onPick={id => void save({ ...prefs, terminal: id })} onCustom={t => void save({ ...prefs, terminal: 'custom', terminalCustom: t })} />
          <OpenerLine title={tr("在编辑器中打开")} hint={tr("项目菜单里「在编辑器中打开」用哪个编辑器")} list={openers.info.editors} current={openers.info.editor}
            custom={prefs.editorCustom} placeholder={tr("例如 subl -n {path}")} help={tr("{path} 是项目文件夹")}
            onPick={id => void save({ ...prefs, editor: id })} onCustom={t => void save({ ...prefs, editor: 'custom', editorCustom: t })} />
        </>}
      </div>
      {error && <div className="note" title={error}>{tr("没能保存")}</div>}
      <div className="group">
        <Line title={tr("开源许可")}><Button variant="ghost" onClick={() => setNoticesOpen(true)}>{tr("查看许可")}</Button></Line>
        <UpdateLine version={info?.version} />
        <Line title={tr("项目地址")}>
          <Button variant="ghost" disabled={!info} title={info?.projectURL} onClick={() => {
            if (info) void shell.openExternal(info.projectURL).then(() => setInfoError(undefined)).catch(e => setInfoError(String(e)))
          }}>github.com/Seanium/koto</Button>
        </Line>
      </div>
      {noticesOpen && <LicenseSheet onClose={() => setNoticesOpen(false)} />}
      {infoError && <div className="note" title={infoError}>{tr(info ? "无法打开链接" : "无法读取应用信息")}</div>}
    </div>
  )
}

function UpdateLine({ version }: { version?: string }) {
  const [state, setState] = useState<UpdateState>()
  const [error, setError] = useState('')
  useEffect(() => {
    let active = true
    const accept = (next: UpdateState) => {
      if (active) setState(previous => !previous || next.revision >= previous.revision ? next : previous)
    }
    const off = onUpdate(accept)
    void shell.updateState().then(accept).catch(e => { if (active) setError(String(e)) })
    return () => { active = false; off() }
  }, [])
  const check = () => {
    setError('')
    void shell.checkUpdates().then(next => setState(previous => !previous || next.revision >= previous.revision ? next : previous)).catch(e => setError(String(e)))
  }
  const install = () => { setError(''); void shell.installUpdate().catch(e => setError(String(e))) }
  const phase = state?.phase
  const busy = phase === 'checking' || phase === 'downloading' || phase === 'restarting'
  const percent = state?.total ? Math.min(100, Math.floor(state.downloaded * 100 / state.total)) : 0
  return <div className="col">
    <Line title={tr("版本")}>
      <span className="t-3 selectable">{version ?? '—'}</span>
      {phase === 'available' ? <Button onClick={install}>{tr("更新并重启")}</Button> :
        <Button variant="ghost" disabled={busy || !version} onClick={check}>{phase === 'checking' ? tr("正在检查…") : phase === 'downloading' ? tr("正在下载 {0}%", [percent]) : phase === 'restarting' ? tr("正在重启…") : tr("检查更新")}</Button>}
    </Line>
    {(phase === 'current' || phase === 'available') && <span className="t-meta" style={{ padding: '0 16px 12px' }} role="status">
      {phase === 'current' ? tr("已是最新版本") : tr("新版本 {0}", [state?.latest ?? ''])}
    </span>}
    {(error || state?.error) && <div className="note selectable" style={{ padding: '0 16px 12px' }} role="alert">{tr("更新失败：{0}", [error || state?.error || ''])}</div>}
  </div>
}

function LicenseSheet({ onClose }: { onClose: () => void }) {
  const [text, setText] = useState<string>()
  const [error, setError] = useState(false)
  useEffect(() => {
    let active = true
    void shell.licenseNotices().then(text => { if (active) setText(text) }).catch(() => { if (active) setError(true) })
    return () => { active = false }
  }, [])
  return <Sheet open onClose={onClose} label={tr("开源许可")}>
    <div className="sheet-head"><div className="t-head">{tr("开源许可")}</div></div>
    <div className="sheet-body scroll selectable" tabIndex={0} style={{ height: 'min(55vh, 480px)', minHeight: 120 }}>
      {error ? <div className="note">{tr("无法读取许可信息")}</div> : text === undefined ? <span className="t-3">{tr("加载中…")}</span> :
        <pre style={{ margin: 0, whiteSpace: 'pre-wrap', overflowWrap: 'anywhere', fontFamily: 'inherit', fontSize: 12, lineHeight: 1.6 }}>{text}</pre>}
    </div>
    <div className="sheet-foot"><Button onClick={onClose}>{tr("关闭")}</Button></div>
  </Sheet>
}

/** A dropdown of the installed presets plus 「自定义命令…」, which reveals a template field. */
function OpenerLine({ title, hint, list, current, custom, placeholder, help, onPick, onCustom }: {
  title: string; hint: string; list: Opener[]; current: string; custom: string; placeholder: string; help: string
  onPick: (id: string) => void; onCustom: (template: string) => void
}) {
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState(custom)
  useEffect(() => setDraft(custom), [custom])
  const installed = list.filter(o => o.installed && o.id !== 'custom')
  const showField = editing || current === 'custom'
  const selected = list.find(o => o.id === current)
  const label = current === 'custom' ? tr("自定义命令") : selected ? openerName(selected) : tr("未找到")
  const commit = () => { const t = draft.trim(); if (t) onCustom(t); else if (current !== 'custom') setEditing(false) }
  return (
    <div className="col">
      <Line title={title} hint={hint}>
        <Menu trigger="secondary" label={title} items={[
          ...installed.map(o => ({ key: o.id, label: openerName(o), checked: o.id === current, onClick: () => { setEditing(false); onPick(o.id) } })),
          ...(installed.length ? [{ separator: true as const }] : []),
          { key: 'custom', label: tr("自定义命令…"), checked: current === 'custom', onClick: () => setEditing(true) },
        ]}>{label}{Icon.chevron}</Menu>
      </Line>
      {showField && (
        <div className="col g1" style={{ padding: '0 16px 12px' }}>
          <input className="field mono" value={draft} placeholder={placeholder} spellCheck={false} aria-label={tr("{0}：自定义命令", [title])}
            autoFocus={editing && current !== 'custom'} onChange={e => setDraft(e.target.value)} onBlur={commit}
            onKeyDown={e => { if (e.key === 'Enter') (e.target as HTMLInputElement).blur(); if (e.key === 'Escape') { setDraft(custom); setEditing(false) } }} />
          <span className="t-meta">{help}</span>
        </div>
      )}
    </div>
  )
}

function Line({ title, hint, children }: { title: string; hint?: string; children: React.ReactNode }) {
  return (
    <div className="group-row">
      <span className="t-medium grow" title={hint}>{title}</span>
      {children}
    </div>
  )
}

// ---------------------------------------------------------------- Agent

function Agents({ hub }: { hub: Hub }) {
  const [adding, setAdding] = useState(false)
  const [errors, setErrors] = useState<Record<string, string>>({})
  const [local, setLocal] = useState<Record<string, boolean>>({})

  const toggle = async (c: Connector, v: boolean) => {
    setLocal(l => ({ ...l, [c.id]: v })); setErrors(e => ({ ...e, [c.id]: '' }))
    try { await api.setConnectorEnabled(c.id, v); await hub.reload() }
    catch { setErrors(x => ({ ...x, [c.id]: tr(v ? "没能启用" : "没能停用") })) }
    finally { setLocal(l => { const n = { ...l }; delete n[c.id]; return n }) }
  }
  const remove = async (c: Connector) => {
    try { await api.removeConnector(c.id); await hub.reload() }
    catch (e) { setErrors(x => ({ ...x, [c.id]: connectorError(tr("没能移除"), e) })) }
  }

  return (
    <div className="col g3">
      {!hub.loaded ? <div className="group">{[0, 1, 2].map(i => <div key={i} className="group-row"><Skeleton w="60%" /></div>)}</div> : (
        <div className="group">
          {grouped(hub.connectors).map(c => {
            const enabled = local[c.id] ?? c.enabled
            return (
              <div key={c.id} className="col">
                <div className="group-row">
                  <AgentDisc harness={c.harness} size={28} title={productName(c.harness, c.variant)} state={enabled ? 'present' : 'absent'} />
                  <div className="col grow g1" style={{ opacity: enabled ? 1 : 0.55 }}>
                    <span className="row g2"><span className="t-medium">{productName(c.harness, c.variant)}</span>{c.status === 'failed' && <span className="t-meta t-red" title={c.error}>{tr("读取失败")}</span>}{!c.sink && <span className="tag" title={tr("{0} 只能作为来源", [productName(c.harness, c.variant)])}>{tr("只读")}</span>}</span>
                    <span className="row g2" style={{ minWidth: 0 }}>
                      {sourceTag(c.id) && <span className="tag" title={sourceTag(c.id) === tr("网关") ? tr("Claude Desktop 网关模式（Claude-3p）里的 Cowork 会话") : tr("Claude Desktop 账号模式里的 Cowork 会话")}>{sourceTag(c.id)}</span>}
                      <span className="mono t-3 ellipsis selectable" title={c.root}>{c.root ?? '—'}</span>
                    </span>
                  </div>
                  {c.removable && <Button variant="ghost" onClick={() => void remove(c)}>{tr("移除")}</Button>}
                  <Switch label={tr("启用 {0}", [agentName(c.harness)])} checked={enabled} onChange={v => void toggle(c, v)} />
                </div>
                {errors[c.id] && <div className="note" style={{ padding: '0 16px 12px 56px' }}>{errors[c.id]}</div>}
              </div>
            )
          })}
        </div>
      )}
      <div><Button small onClick={() => setAdding(true)}>{tr("添加位置…")}</Button></div>
      <AddLocation open={adding} onClose={() => setAdding(false)} hub={hub} />
    </div>
  )
}

function AddLocation({ open, onClose, hub }: { open: boolean; onClose: () => void; hub: Hub }) {
  const [product, setProduct] = useState(PRODUCTS[0])
  const [error, setError] = useState<string>()
  const [busy, setBusy] = useState(false)
  useEffect(() => { if (open) setError(undefined) }, [open])
  const choose = async () => {
    setError(undefined)
    const dir = await shell.pickDirectory(tr("选择 {0} 的数据目录", [productName(product.harness, product.variant)])).catch(() => '')
    if (!dir) return
    setBusy(true)
    try { await api.addConnectorRoot(product.harness, dir, product.variant); await hub.reload(); onClose() }
    catch (e) { setError((e as Error).message) }
    finally { setBusy(false) }
  }
  return (
    <Sheet open={open} onClose={onClose} label={tr("添加位置")}>
      <div className="sheet-head">
        <div className="t-head">{tr("添加位置")}</div>
      </div>
      <div className="sheet-body col" role="listbox" aria-label={tr("这个目录属于哪个 Agent")}>
        {PRODUCTS.map(p => (
          <ListRow key={`${p.harness}/${p.variant}`} selected={product === p} onClick={() => setProduct(p)}>
            <AgentDisc harness={p.harness} size={24} title="" />
            <span className="grow t-medium">{productName(p.harness, p.variant)}</span>
            <span style={{ width: 16 }}>{product === p && Icon.check}</span>
          </ListRow>
        ))}
        {error && <div className="note" style={{ padding: '8px 12px 0' }} title={error}>{connectorError(tr("没能添加这个目录"), error)}</div>}
      </div>
      <div className="sheet-foot">
        <Button onClick={onClose}>{tr("取消")}</Button>
        <Button variant="primary" disabled={busy} onClick={choose}>{busy ? tr("正在添加…") : tr("选择目录…")}</Button>
      </div>
    </Sheet>
  )
}

// Claude Desktop's Cowork sources are Claude Code locations: account mode (claude-code@cowork:…) or
// gateway mode (claude-code@cowork-3p:…).
function sourceTag(id: string): string | undefined {
  if (id.includes('@cowork-3p:')) return tr("网关")
  if (id.includes('@cowork:')) return tr("账号")
  return undefined
}

/** Locations in their Agent's order, each Agent's extra locations (Cowork, added folders) right under it. */
function grouped(cs: Connector[]): Connector[] {
  const order = [...new Set(cs.map(c => c.harness))]
  return [...cs].sort((a, b) => order.indexOf(a.harness) - order.indexOf(b.harness))
}

// One short sentence for the core's 400s on adding or removing a location (the raw message stays in
// the tooltip).
function connectorError(fallback: string, e: unknown): string {
  const m = e instanceof Error ? e.message : String(e)
  if (/is already registered as/.test(m)) return tr("这个目录已经添加过了")
  if (/overlaps the root of/.test(m)) return tr("这个目录和已添加的位置重叠")
  if (/is not a directory/.test(m)) return tr("这不是一个文件夹")
  if (/waiting for repair|needs repair/.test(m)) return tr("有写到一半的内容，修复后才能移除")
  if (/is built in/.test(m)) return tr("内置位置不能移除，可以停用")
  return fallback
}

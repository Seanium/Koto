import { tr } from '../i18n/core.ts'
import { projectFolderError } from './copyErrors'
// Typed wrappers over the core, which runs inside the app: every call goes straight to Go
// (AppService); the core's events arrive as Wails events.

import { useEffect, useState } from 'react'
import { Browser, Events, type CancellablePromise } from '@wailsio/runtime'
import { AppService } from '../../bindings/koto'
import type {
  AgentProfile, AnalysisCollection, AnalysisResult, CarryRequest, CarryResult, Connector, CoreStatus, ScanReport, ScanStatus, ServerEvent, ThreadDetail, ThreadSummary } from './types'

/** A failed call: the core's message, with its `code` and the rest of what it said in `body`. */
export class ApiError extends Error {
  constructor(message: string, readonly code?: string, readonly body: Record<string, unknown> = {}) { super(message) }
}

/** Actionable summaries; the original error remains visible in the error details. */
export function copyFailure(reason: string): string {
  const folder = projectFolderError(reason)
  if (folder) return folder
  if (/already exists with different content/.test(reason)) return tr("目标已有同名会话，未覆盖原文件。请重新复制。")
  if (/new Codex session could not be registered|Codex did not accept/.test(reason)) return tr("新会话已写入，但 Codex 尚未确认。请重试完成验证。")
  if (/cannot start|executable file not found/.test(reason)) return tr("无法启动目标 Agent，请检查是否已安装、命令是否可用。")
  if (/database is locked/.test(reason)) return tr("koto 数据目录正忙，请稍后重试。")
  if (/ENOSPC|no space left/i.test(reason)) return tr("磁盘空间不足，请腾出空间后重试。")
  if (/permission|EACCES/i.test(reason)) return tr("没有目标目录的写入权限。")
  if (/no longer|nothing matches|NOT_FOUND/.test(reason)) return tr("会话已经变化或找不到了，请刷新后重新选择。")
  return tr("复制失败：{0}", [reason])
}

/** A core call: results are the core's JSON; failures carry its {status, code, error, detail}. */
async function call<T>(p: PromiseLike<unknown>): Promise<T> {
  try {
    return (await p) as T
  } catch (e) {
    // Go errors arrive as { message } objects or strings
    const msg = typeof e === 'string' ? e : (e as { message?: string })?.message ?? String(e)
    try {
      const body = JSON.parse(msg) as Record<string, unknown>
      if (typeof body.error === 'string') throw new ApiError(body.error, typeof body.code === 'string' ? body.code : undefined, { ...body, ...(body.detail as object ?? {}) })
    } catch (x) { if (x instanceof ApiError) throw x }
    throw new ApiError(msg)
  }
}

/** Forward view disposal to Wails and its Go request context, not just to React. */
function read<T>(p: CancellablePromise<unknown>, signal?: AbortSignal): Promise<T> {
  return call<T>(signal ? p.cancelOn(signal) : p)
}

export const api = {
  connectors: () => call<Connector[]>(AppService.Connectors()),
  profiles: () => call<AgentProfile[]>(AppService.Profiles()),
  setConnectorEnabled: (id: string, enabled: boolean) => call<Connector>(AppService.SetConnectorEnabled(id, enabled)),
  addConnectorRoot: (harness: string, root: string, variant = '') => call<Connector>(AppService.AddConnectorRoot(harness, root, variant)),
  removeConnector: (id: string) => call<boolean>(AppService.RemoveConnector(id)),

  threads: (limit = 500) => call<ThreadSummary[]>(AppService.Threads(limit)),
  searchThreads: (query: string, scope: string, project = '', profile = '', account = '') => call<ThreadSummary[]>(AppService.SearchThreads(query, scope, project, profile, account)),
  /** A page of a native session's path from the end: the last `turns` turns, or the `turns` before node `before`. */
  threadPage: (selector: string, turns: number, before?: string, signal?: AbortSignal) => read<ThreadDetail>(AppService.ThreadPage(selector, turns, before ?? ''), signal),
  analyze: (selectors: string[]) => call<AnalysisResult>(AppService.Analyze(selectors)),
  scan: () => call<ScanReport>(AppService.Scan()),
  scanStatus: () => call<ScanStatus>(AppService.ScanStatus()),

  carry: (req: CarryRequest, signal?: AbortSignal) => read<CarryResult>(AppService.Carry(req as never), signal),

  /** A picture from a conversation as a data: URL (selector: the conversation or a node in it). */
  image: (selector: string, sha: string, signal?: AbortSignal) => read<string>(AppService.Image(selector, sha), signal),
  forgetCopy: (id: string) => AppService.ForgetCopy(id),
  analyses: () => call<AnalysisCollection[]>(AppService.Analyses()),
  saveAnalysis: (c: AnalysisCollection) => call<AnalysisCollection>(AppService.SaveAnalysis(c as never)),
  deleteAnalysis: (id: string) => call<boolean>(AppService.DeleteAnalysis(id)),


}

// ---------------------------------------------------------------- shell

export const shell = {
  licenseNotices: () => AppService.LicenseNotices(),
  appInfo: () => AppService.AppInfo() as Promise<AppInfo>,
  updateState: () => AppService.UpdateState() as Promise<UpdateState>,
  checkUpdates: () => AppService.CheckUpdates() as Promise<UpdateState>,
  installUpdate: () => AppService.InstallUpdate(),
  /** Wails validates the URL and opens the system browser, keeping the app view in place. */
  openExternal: (url: string) => Browser.OpenURL(url),
  status: () => AppService.Status() as Promise<CoreStatus>,
  copy: (text: string) => AppService.Copy(text),
  openMain: (nav?: Record<string, string>) => AppService.OpenMain(nav ? JSON.stringify(nav) : ''),
  /** Native folder chooser; resolves to '' when cancelled. */
  pickDirectory: (title: string) => AppService.PickDirectory(title),
  mark: (name: string) => { void AppService.Mark(name).catch(() => {}) },
  openSettings: (tab = '') => AppService.OpenSettings(tab),
  /** Runs a shell line (or just opens at cwd) in the chosen terminal: 'ok', 'copied' (that terminal
   * cannot run it; the line is on the clipboard), or '' when it failed. */
  openInTerminal: (command: string, cwd = '') => AppService.OpenInTerminal(command, cwd) as Promise<'ok' | 'copied' | ''>,
  openInEditor: (path: string) => AppService.OpenInEditor(path),
  revealPath: (path: string) => AppService.RevealPath(path),
  pathExists: (path: string) => AppService.PathExists(path),
  openers: () => AppService.Openers() as Promise<OpenersInfo>,
  homeDir: () => AppService.HomeDir(),
  setUILocale: (locale: string) => AppService.SetUILocale(locale),
  prefs: () => AppService.GetPrefs() as Promise<Prefs>,
  setPrefs: (p: Prefs) => AppService.SetPrefs(p),
  setAgentOrder: (ids: string[]) => AppService.SetAgentOrder(ids),
  mcpConfig: () => AppService.McpConfig(),
  restart: () => AppService.Restart(),
}

export interface AppInfo { version: string; projectURL: string }
export interface UpdateState {
  revision: number; phase: 'idle' | 'checking' | 'unpublished' | 'ahead' | 'current' | 'available' | 'downloading' | 'restarting' | 'error'
  current: string; latest: string; prerelease: boolean; url: string; error: string; downloaded: number; total: number
  errorCode: string; errorStage: string; retryAt: number
}
export function onUpdate(cb: (state: UpdateState) => void): () => void {
  return Events.On('koto:update', event => cb(unwrap(event.data) as UpdateState))
}

export interface Prefs {
  agentOrder?: string[]
  /** Opener ids (see OpenersInfo); '' = the first installed one. *Custom: the template for 'custom'. */
  terminal: string; terminalCustom: string; editor: string; editorCustom: string
  /** '' = follow the system. */
  appearance: '' | 'light' | 'dark'
  language: 'system' | 'zh' | 'zh-TW' | 'en' | 'ja' | 'de'
}

export interface Opener { id: string; name: string; installed: boolean }
/** Terminals and editors (installed ones marked, then 'custom'), the effective choices ('' = none), and what the file manager is called. */
export interface OpenersInfo { terminals: Opener[]; editors: Opener[]; terminal: string; editor: string; fileManager: string }

// The openers and the home folder, fetched once and shared; Settings refreshes them after a change.
let openersCache: { info: OpenersInfo; home: string } | undefined
const openersListeners = new Set<(o: { info: OpenersInfo; home: string }) => void>()
export async function refreshOpeners() {
  try {
    const [info, home] = await Promise.all([shell.openers(), shell.homeDir()])
    openersCache = { info, home }
    openersListeners.forEach(l => l(openersCache!))
  } catch { /* not in the app shell (browser preview): no openers */ }
}
export function useOpeners(): { info: OpenersInfo; home: string } | undefined {
  const [v, setV] = useState(openersCache)
  useEffect(() => {
    openersListeners.add(setV)
    if (!openersCache) void refreshOpeners()
    return () => { openersListeners.delete(setV) }
  }, [])
  return v
}


/** Right-click menu actions on a session row (native menu, handled in Go). */
export function onContextAction(cb: (a: { action: string; thread: string }) => void): () => void {
  return Events.On('app:context', ev => {
    try { cb(JSON.parse(unwrap(ev.data) as string)) } catch { /* ignore */ }
  })
}

export function onSettingsTab(cb: (tab: string) => void): () => void {
  return Events.On('settings:tab', ev => cb(unwrap(ev.data) as string))
}

const unwrap = (d: unknown) => (Array.isArray(d) && d.length === 1 ? d[0] : d)

/** The core's events: each index pass (cycle), its progress, a file clash. */
export function onServerEvent(cb: (e: ServerEvent) => void): () => void {
  return Events.On('koto:event', ev => {
    try { cb(JSON.parse(unwrap(ev.data) as string)) } catch { /* ignore malformed */ }
  })
}

export function onStatus(cb: (s: CoreStatus) => void): () => void {
  return Events.On('koto:status', ev => cb(unwrap(ev.data) as CoreStatus))
}

/** Navigation requests from the shell (tray popover → main window, deep links). */
export function onNavigate(cb: (nav: Record<string, string>) => void): () => void {
  return Events.On('app:navigate', ev => {
    try { cb(JSON.parse(unwrap(ev.data) as string)) } catch { /* ignore */ }
  })
}

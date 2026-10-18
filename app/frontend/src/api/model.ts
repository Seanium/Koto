import { getLocale, tr } from '../i18n/core.ts'
// Derived views over API data that any screen needs: a thread's title / cwd / origin, its turns,
// where it lives (per harness), loss summaries, relative times. Pure functions, no I/O.

import type { Binding, Connector, HubEvent, Loss, Part } from './types'

// ---------------------------------------------------------------- threads

/** A Claude Code session written from Claude Desktop's Code tab (same files as the CLI). */
export const fromDesktop = (b?: { harness: string; meta?: { frontend?: string } | null }) => b?.harness === 'claude-code' && b.meta?.frontend === 'claude-desktop'

export function originBinding(bindings: Binding[]): Binding | undefined {
  return bindings[0]
}

export function threadTitle(t: { bindings: Binding[]; events?: HubEvent[] | number }): string {
  for (const b of [originBinding(t.bindings), ...t.bindings]) {
    const title = b?.meta.title ?? b?.meta.aiTitle
    if (typeof title === 'string' && title.trim()) return title.trim()
  }
  const first = Array.isArray(t.events) && t.events.find(e => e.actor === 'user' && e.kind === 'message')
  const text = first && (typeof first.attrs?.displayText === 'string' ? userDisplayText(first) : partsText(first.parts))
  return text ? oneLine(text, 80) : tr("未命名会话")
}

export function threadCwd(t: { bindings: Binding[] }): string | undefined {
  const cwd = originBinding(t.bindings)?.meta.cwd
  return typeof cwd === 'string' ? cwd : undefined
}

// ---------------------------------------------------------------- turns

export interface ToolStep {
  callId: string
  name: string
  /** One-line rendering of the call: a command, a path, or the raw tool name. */
  call: string
  outcome?: 'success' | 'error' | 'cancelled' | 'unknown'
  /** First line(s) of the result. */
  result?: string
}

/** A picture in a turn: its blob (fetched on demand) and type. */
export interface TurnImage { sha: string; mediaType: string; name?: string }
export interface TurnFile { sha?: string; mediaType: string; name?: string; uri?: string }
const filesOf = (parts: Part[]): TurnFile[] => parts.flatMap(p => p.type === 'file' ? [{ sha: p.blob?.sha256, mediaType: p.mediaType, name: p.name, uri: p.uri }] : p.type === 'tool_result' ? filesOf(p.parts) : [])

const imagesOf = (parts: Part[]): TurnImage[] => parts.flatMap(p =>
  p.type === 'image' && p.blob?.sha256 ? [{ sha: p.blob.sha256, mediaType: p.mediaType, name: p.name }]
  : p.type === 'tool_result' ? imagesOf(p.parts) : [])

/** Native user text can differ from the model request's wrapper. It is display-only. */
export function userDisplayText(event: HubEvent): string {
  if (event.actor === 'user' && typeof event.attrs?.displayText === 'string') {
    const unavailable = partsText(event.parts.filter(p => p.type !== 'text' && p.type !== 'file'))
    return [event.attrs.displayText, unavailable].filter(Boolean).join('\n')
  }
  return partsText(event.parts.filter(p => p.type !== 'file'))
}

export interface Turn {
  /** 1-based. */
  index: number
  /** Harness the user turn was written in. */
  harness: string
  connector: string
  time?: number
  user: string
  /** Absent when the excerpt starts with a reply. */
  promptId?: string
  /** The question and any separately recorded native material belonging to it. */
  promptIds?: string[]
  /** Pictures sent with the user message; imagesFirst: they came before its text. */
  images: TurnImage[]
  files: TurnFile[]
  imagesFirst?: boolean
  steps: ({ kind: 'text'; text: string } | { kind: 'tool'; tool: ToolStep } | { kind: 'reasoning'; text: string } | { kind: 'image'; image: TurnImage } | { kind: 'file'; file: TurnFile })[]
  /** Event ids in this turn (to map a binding leaf or a delivery head to a turn). */
  eventIds: string[]
  /** Only its end is loaded (a page started mid-turn): the user message is in an earlier page. */
  partial?: boolean
}

/** Groups a path of events into turns, each starting at a user message. */
/** `midTurn`: the events are a page that may start inside a turn (its start is on an earlier page). */
export function turnsOf(events: HubEvent[], midTurn = false): Turn[] {
  const turns: Turn[] = []
  const tools = new Map<string, ToolStep>()
  const questions = new Map<string, Turn>()
  let cur: Turn | undefined
  for (const e of events) {
    // Native system/environment records can occur anywhere in a turn. They are neither
    // visible reply content nor selectable IDs (including trailing copy boundaries).
    if (e.kind === 'context') continue
    if (e.kind === 'message' && e.actor === 'user' && e.parts.some(p => p.type === 'text' || p.type === 'image' || p.type === 'file' || p.type === 'opaque')) {
      cur = { index: turns.length + 1, harness: e.provenance.source.harness, connector: e.provenance.source.connector, time: e.time, user: userDisplayText(e), promptId: e.id, images: imagesOf(e.parts), files: filesOf(e.parts), imagesFirst: e.parts.findIndex(p => p.type === 'image') < e.parts.findIndex(p => p.type === 'text'), steps: [], eventIds: [e.id] }
      turns.push(cur)
      questions.set(e.id, cur)
      continue
    }
    if (e.kind === 'attachment' && e.actor === 'user' && e.parents.length === 1 && !e.attrs?.sidechain) {
      const question = questions.get(e.parents[0])
      if (question) {
        question.promptIds = [...questionIDs(question), e.id]
        question.eventIds.push(e.id)
        question.images.push(...imagesOf(e.parts))
        question.files.push(...filesOf(e.parts))
        const note = partsText(e.parts.filter(p => p.type === 'text'))
        if (note) question.user = [question.user, note].filter(Boolean).join('\n')
        questions.set(e.id, question)
        continue
      }
    }
    if (!cur) {
      // A page that starts mid-turn: keep its end as a turn whose start is not loaded yet.
      if (e.kind === 'message' || e.kind === 'tool_result' || e.kind === 'compaction' || e.kind === 'notification' || e.kind === 'attachment') {
        cur = { index: 1, harness: e.provenance.source.harness, connector: e.provenance.source.connector, time: e.time, user: '', images: [], files: [], steps: [], eventIds: [], partial: midTurn }
        turns.push(cur)
      } else continue // context before the first user message
    }
    cur.eventIds.push(e.id)
    for (const p of e.parts) {
      if (p.type === 'text' && (e.actor === 'assistant' || e.kind === 'notification') && p.text.trim()) cur.steps.push({ kind: 'text', text: p.text })
      else if (p.type === 'image' && p.blob?.sha256 && (e.actor !== 'user' || e.kind === 'notification')) cur.steps.push({ kind: 'image', image: { sha: p.blob.sha256, mediaType: p.mediaType, name: p.name } })
      else if (p.type === 'file') cur.steps.push({ kind: 'file', file: { sha: p.blob?.sha256, mediaType: p.mediaType, name: p.name, uri: p.uri } })
      else if (p.type === 'reasoning' && p.text) cur.steps.push({ kind: 'reasoning', text: p.text })
      else if (p.type === 'tool_call') {
        const step: ToolStep = { callId: p.callId, name: p.rawName, call: toolCallLine(p) }
        tools.set(p.callId, step)
        cur.steps.push({ kind: 'tool', tool: step })
      } else if (p.type === 'tool_result') {
        const step = tools.get(p.callId)
        if (step) { step.outcome = p.outcome; step.result = oneLine(partsText(p.parts), 160) }
        for (const image of imagesOf(p.parts)) cur.steps.push({ kind: 'image', image })
        for (const file of filesOf(p.parts)) cur.steps.push({ kind: 'file', file })
      }
    }
  }
  return turns
}

export const questionIDs = (turn: Turn) => turn.promptIds ?? (turn.promptId ? [turn.promptId] : [])
export const replyEventIDs = (turn: Turn) => {
  const question = new Set(questionIDs(turn))
  return turn.eventIds.filter(id => !question.has(id))
}

/** 1-based turn number containing `eventId`; undefined when the id is not on this path. */
export function turnOfEvent(turns: Turn[], eventId: string | null | undefined): number | undefined {
  if (!eventId) return undefined
  return turns.find(t => t.eventIds.includes(eventId))?.index
}

function toolCallLine(p: Extract<Part, { type: 'tool_call' }>): string {
  const t = p.tool
  switch (t.op) {
    case 'shell': return t.command
    case 'read': return `${p.rawName} ${t.path}`
    case 'write': return `${p.rawName} ${t.path}`
    case 'edit': return `${p.rawName} ${t.path}`
    case 'search': return `${p.rawName} ${t.query}${t.scope ? ' ' + t.scope : ''}`
    case 'list': return `${p.rawName} ${t.path ?? t.pattern ?? ''}`.trim()
    case 'web': return `${p.rawName} ${t.url ?? t.query ?? ''}`.trim()
    case 'patch': return `${p.rawName} ${firstLine(t.patch.split('\n').find(l => /^\*\*\* (Update|Add|Delete) File:/.test(l)) ?? '')}`.trim()
    default: return `${p.rawName} ${oneLine(JSON.stringify(p.args ?? ''), 100)}`
  }
}

export function partsText(parts: Part[]): string {
  return parts.map(p => (p.type === 'text' ? p.text : p.type === 'file' ? tr("[附件：{0}{1}]", [p.name || p.mediaType || tr("文件"), !p.blob && !p.uri ? tr(" · 内容不可用") : '']) : p.type === 'image' && !p.blob ? tr("[图片：内容不可用或仅有远程引用]") : p.type === 'opaque' ? tr("[内容：{0}]", [p.semantic]) : p.type === 'tool_result' ? partsText(p.parts) : '')).filter(Boolean).join('\n')
}

// ---------------------------------------------------------------- carry / deliveries

export function writeModeLabel(c: Connector): string {
  switch (c.capabilities.write?.mode) {
    case 'native-file': return tr("原生写入")
    default: return tr("只读")
  }
}


// ---------------------------------------------------------------- formatting

export function oneLine(s: string, max: number): string {
  const t = s.replace(/\s+/g, ' ').trim()
  return t.length > max ? t.slice(0, max - 1) + '…' : t
}

export const firstLine = (s: string) => s.split('\n')[0]

/** App-owned labels are translated; installed application names are left as registered. */
export const openerName = (opener: { id: string; name: string }) => opener.id === 'system' ? tr('系统默认') : opener.name

let formatLocale = ''
let relative: Intl.RelativeTimeFormat, date: Intl.DateTimeFormat, time: Intl.DateTimeFormat
function formats() {
  const locale = getLocale()
  if (locale !== formatLocale) {
    formatLocale = locale
    relative = new Intl.RelativeTimeFormat(locale, { numeric: 'auto', style: 'short' })
    date = new Intl.DateTimeFormat(locale, { month: 'numeric', day: 'numeric' })
    time = new Intl.DateTimeFormat(locale, { hour: '2-digit', minute: '2-digit' })
  }
}
export function relativeTime(ms: number | null | undefined, now = Date.now()): string {
  if (!ms) return ''
  formats()
  const s = Math.round((now - ms) / 1000)
  if (s < 45) return tr("刚刚")
  const m = Math.round(s / 60)
  if (m < 60) return relative.format(-m, 'minute')
  const h = Math.round(m / 60)
  if (h < 24) return relative.format(-h, 'hour')
  const d = Math.round(h / 24)
  if (d < 7) return relative.format(-d, 'day')
  return date.format(ms)
}

export function clockTime(ms: number | null | undefined): string {
  if (!ms) return ''
  formats()
  return time.format(ms)
}

/** Shortens a path under the user's home to ~/… when the home is known (it is not, in the webview; left as a hook). */
export const displayPath = (p: string | undefined) => p ?? ''


// ---------------------------------------------------------------- plain-language helpers for the UI

/** Last path segment of the working directory: the "project" a session belongs to. */
export function projectName(cwd: string | undefined): string {
  if (!cwd) return ''
  const parts = cwd.split(/[\\/]+/).filter(Boolean)
  return parts.at(-1) ?? cwd
}

/** The line that continues this session in its Agent (from the core), if there is one. */
export const resumeLine = (b: Binding): string | undefined => b.resume?.shell

/** Losses of a planned delivery, as short phrases ("不含 3 张图片"). */
export function lossSentences(losses: Loss[]): string[] {
  const counts = new Map<string, number>()
  for (const l of losses) {
    if (l.field === 'reasoning' && l.kind === 'dropped' && l.note === 'empty reasoning') continue
    let text = l.note || tr("部分内容需要转换")
    if (l.kind === 'truncated') text = tr('部分内容被截断')
    else if (l.kind === 'referenced') text = l.note?.includes('Remote attachment') ? tr('远程附件以链接保留') : l.note?.includes('内嵌格式') ? tr('目标无法内嵌此类附件，已保留原文件供读取') : tr("附件保留为原文件或链接")
    else if (l.field === 'image') text = tr("有图片内容不可用")
    else if (l.field === 'file') text = l.kind === 'dropped' ? tr("有附件内容不可用") : tr("附件格式有转换")
    else if (l.field.startsWith('tool_result') && l.kind === 'approximated') text = tr('部分工具调用缺少结果')
    else if (l.field.startsWith('tool_call') || l.field.startsWith('tool_result')) text = l.kind === 'dropped' ? tr('部分工具记录未复制')
      : l.note?.includes('orphan result') ? tr('未找到对应调用，工具结果以文字保留') : tr('为兼容目标格式，工具调用和结果以文字保留')
    else if (l.field === 'reasoning') text = l.kind === 'dropped' ? (l.note?.startsWith("encrypted by ") ? tr("不含加密推理内容") : tr("部分推理内容未复制")) : tr("推理过程转为文字")
    else if (l.field === 'sidechain') text = tr("不含子任务记录")
    else if (l.field === 'history') text = tr("较早内容只含摘要")
    else if (l.field === 'message' && l.kind === 'approximated') text = tr('部分对话已压缩为摘要')
    else if (l.field === 'role' && l.kind === 'approximated') text = tr('为回复片段补充起始提问')
    else if (l.field.startsWith('opaque:')) text = tr("有无法识别的内容")
    else if (l.field === 'message' && l.kind === 'dropped') text = tr("不含未完成的回答")
    counts.set(text, (counts.get(text) ?? 0) + 1)
  }
  return [...counts.keys()]
}

/** One line on what an Agent can do with sessions ("可读取、可写入" / "只读"). */
export function capabilityLine(c: Connector): string {
  const w = c.capabilities.write
  if (!c.sink || !w || w.mode === 'none') return tr("只读")
  const parts = [tr("可读取、可写入")]
  if (w.reasoning === 'none' || w.reasoning === false) parts.push(tr("不保留推理"))
  if (!w.images) parts.push(tr("不带图片"))
  return parts.join('、')
}

/** Can sessions be continued in this connector (written into)? */
export const writable = (c: Connector) => c.enabled && c.status === 'ok' && c.sink && !!c.capabilities.write && c.capabilities.write.mode !== 'none'
export const AGENTS: { id: string; name: string }[] = [
  { id: 'claude-code', name: 'Claude Code' },
  { id: 'codex', name: 'Codex' },
  { id: 'pi', name: 'Pi' },
  { id: 'dsh', name: 'DeepSeek Harness' },
  { id: 'antigravity', name: 'Antigravity CLI' },
  { id: 'opencode', name: 'OpenCode' },
  { id: 'zcode', name: 'ZCode' },
  { id: 'workbuddy', name: 'WorkBuddy' },
  { id: 'workbuddy-ai', name: 'WorkBuddy AI' },
  { id: 'cursor', name: 'Cursor CLI' },
]
const AGENT_NAMES = new Map(AGENTS.map(a => [a.id, a.name]))

/** Display name for a harness id; unknown ids are shown as they are. */
export const agentName = (harness: string) => AGENT_NAMES.get(harness) ?? harness
export const productName = (harness: string, variant: string) => harness === 'cursor'
  ? variant === 'ide' ? 'Cursor Desktop' : 'Cursor CLI'
  : harness === 'claude-code' && variant === 'desktop' ? 'Claude Desktop'
  : harness === 'claude-code' && variant === 'cowork' ? 'Claude Cowork'
  : agentName(harness)
export const productScopeName = (key: string) => productName(...key.split('/') as [string, string])

/** A folder for display: under the home folder as ~/…. */
export function tildePath(path: string, home?: string): string {
  if (home && (path === home || path.startsWith(home + '/'))) return '~' + path.slice(home.length)
  return path
}

/** The project menu's reveal item, by what the platform calls its file manager. */
export const revealLabel = (fileManager: string) => fileManager === 'Finder' ? tr("在 Finder 中显示") : tr("在{0}中显示", [fileManager])

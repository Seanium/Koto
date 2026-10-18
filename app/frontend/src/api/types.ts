// Types returned by the Go AppService (docs/development.md). Mirrors the JSON representations of the
// core, but only the fields the app reads; everything is plain JSON.

export type Level = 'L0' | 'L1' | 'L2' | 'L3'
export type JsonObject = { [k: string]: unknown }

export interface IdentityClaim {
  kind: 'account' | 'organization' | 'api-configuration'
  id: string
  label?: string
  scope: 'creator' | 'storage' | 'session' | 'message'
  evidence: string
}
export interface SessionIdentity {
  status: 'known' | 'unknown'
  claims?: IdentityClaim[]
  providers?: { id: string; model?: string; baseUrl?: string; scope: 'session' | 'message' | 'current-config'; evidence: string }[]
}
export interface SourceProfile {
  id: string
  harness: string
  variant: string
  default: boolean
  root: string
  label: string
  kind: string
}
export interface AgentProfile extends SourceProfile {
  writable: boolean
  enabled: boolean
  accountSelectable: boolean
  accounts: IdentityClaim[]
  targetAccounts: IdentityClaim[]
  targetAccountsError?: string
}

// ---------------------------------------------------------------- connectors

export type WriteMode = 'native-file' | 'none'

export interface Capabilities {
  read: { images: boolean; reasoning: 'none' | 'plain' | 'sealed'; subagents: boolean; compaction: boolean }
  write?: {
    mode: WriteMode
    images: boolean
    reasoning: unknown
    tools: 'native' | 'canonical' | 'text'
  }
  formatVersions: string[]
}

export interface Connector {
  /** Stable native profile id, derived from its product variant and storage root. */
  id: string
  harness: string
  variant: string
  default: boolean
  version: string
  /** Storage root this instance reads/writes. */
  root?: string
  status: 'ok' | 'failed' | 'disabled'
  enabled: boolean
  error?: string
  source: boolean
  sink: boolean
  /** User-added directory (can be deleted; built-ins can only be disabled). */
  removable: boolean
  capabilities: Capabilities
}

// ---------------------------------------------------------------- threads & events

export interface Binding {
  harness: string
  session: string
  connector: string
  thread: string
  leaf: string | null
  path: string | null
  meta: JsonObject & { title?: string; aiTitle?: string; cwd?: string; frontend?: string; identity?: SessionIdentity; sourceProfile?: SourceProfile }
  /** How to continue this session in its harness; null where there is no command. */
  resume?: Resume | null
}

export interface Resume {
  command: string
  cwd: string | null
  /** Only when the storage root is not the harness default. */
  env: Record<string, string>
  /** cwd + env + command as one shell line. */
  shell: string
  instructions?: string
}

export interface ThreadSummary {
  id: string
  revision?: string
  events: number
  updated: number | null
  bindings: Binding[]
}


export type Actor = 'user' | 'assistant' | 'tool' | 'system' | 'harness'

export type CanonicalTool =
  | { op: 'read'; path: string; range?: [number, number] }
  | { op: 'write'; path: string; content?: string }
  | { op: 'edit'; path: string; edits: { old: string; new: string }[] }
  | { op: 'shell'; command: string; cwd?: string }
  | { op: 'search'; query: string; scope?: string }
  | { op: 'list'; path?: string; pattern?: string }
  | { op: 'web'; url?: string; query?: string }
  | { op: 'patch'; patch: string }
  | { op: 'raw' }

export type Part =
  | { type: 'text'; text: string }
  | { type: 'image' | 'file'; mediaType: string; name?: string; uri?: string; blob?: { sha256: string; size?: number } }
  | { type: 'tool_call'; callId: string; tool: CanonicalTool; rawName: string; args: unknown }
  | { type: 'tool_result'; callId: string; outcome: 'success' | 'error' | 'cancelled' | 'unknown'; parts: Part[] }
  | { type: 'reasoning'; text?: string }
  | { type: 'opaque'; semantic: string; reason: string }

export interface HubEvent {
  id: string
  parents: string[]
  thread: string
  kind: string
  actor: Actor
  parts: Part[]
  time?: number
  attrs?: JsonObject
  provenance: {
    source: { connector: string; harness: string; version: string; frontend?: string }
    native: { session: string; id: string; index?: number }
  }
}

export interface ThreadDetail {
  completeTurns: number
  thread: string
  head: string
  bindings: Binding[]
  /** Events on the path from the root to `head` (with ?turns: a page from the end, starting at `offset`). */
  events: HubEvent[]
  offset?: number
  total?: number
  /** With ?turns: total turns on this native session path. */
  turns?: number
}

// ---------------------------------------------------------------- carry

export interface CarryRequest {
  eventIds?: string[]
  excludeEventIds?: string[]
  requestId?: string
  /** Event id, th:… thread id, or @profile:session. Either this or `from`. */
  selector?: string
  /** Any node (full or short ev:… id): carries root → that node. */
  from?: string
  /** Target connector id. */
  to: string
  targetProfile?: string
  targetAccount?: string
  level?: Level
  dryRun?: boolean
  maxTokens?: number
  cwd?: string
  /** A new session's title (「原标题 · 第 1–k 轮」), where the Agent keeps one. */
  title?: string
}

export interface Loss {
  sink: string
  event: string
  field: string
  kind: 'referenced' | 'dropped' | 'textualized' | 'truncated' | 'approximated'
  note?: string
}

export interface DeliveryPlan {
  id: string
  sink: string
  harness: string
  thread: string
  head: string
  level: Level
  mode: 'create' | 'noop' | 'reject'
  target: { mode: string; session: string; path?: string; cwd?: string; profile: string; account?: string }
  records: { nativeId: string; eventId: string; sha256: string }[]
  losses: Loss[]
  notes: string[]
  extra?: JsonObject
}

export interface CarryResult {
  plan: DeliveryPlan
  applied: boolean
  receipt?: JsonObject
  /** Shell command to continue in the target harness (after apply). */
  resume?: string
}

// ---------------------------------------------------------------- misc

export interface ScanReport {
  sessions: number
  changed: number
  added: number
  deduped: number
  skipped: number
  errors: { connector: string; session?: string; error: string }[]
}

export interface HubStats {
  today: { bytes: number; events: number; txns: number }
  total: { bytes: number; events: number; txns: number }
  budget: number
  events: number
  threads: number
  bindings: number
  blobs: number
  blobBytes: number
}

/** One message returned by the event API. */
/** A long scan (the first one reads every session) reports as it goes. */
export interface ScanProgress { connector: string; harness: string; done: number; total: number; added: number }
export interface ScanStatus { scanning: boolean; progress?: ScanProgress; report?: ScanReport }
export type ServerEvent = { type: 'cycle'; report: ScanReport } | ({ type: 'progress' } & ScanProgress)

export interface CoreStatus {
  state: 'starting' | 'ready' | 'error'
  code?: string
  hubDir?: string
  error?: string
}

export interface AnalysisSession { selector: string; title: string; turns: number; nodes: string[] }
export interface AnalysisLabel { line: number; kind: string; value?: string }
export interface AnalysisNode { id: string; parent: string; turn: number; preview: string; sessions: number[]; text: string; labels?: AnalysisLabel[]; hash: string; truncated: boolean }
export interface AnalysisResult { harness: string; sessions: AnalysisSession[]; nodes: AnalysisNode[] }

export interface AnalysisCollection { id: string; name: string; sessions: string[] }

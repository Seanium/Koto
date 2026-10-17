// Oracle: validate a dsh session log with dsh's own verifier (the migration-verifier worker shipped
// in @deepseek-ai/dsh-session-persistence-jsonl, installed project-locally under .tools/dsh).
//   node app/internal/koto/testdata/dsh-verify.mjs <session.v4.jsonl[.zstd]> <session id> <event count>
// Prints {"ok":true} or {"ok":false,"error":…}. The caller (a Go test) decodes the log for the id and count.
import { Worker } from 'node:worker_threads'
import path from 'node:path'

const worker = path.resolve(import.meta.dirname, '../../../../.tools/dsh/node_modules/@deepseek-ai/dsh-session-persistence-jsonl/lib/worker.cjs')
const [file, id, count] = process.argv.slice(2)
const r = await new Promise(resolve => {
  const w = new Worker(worker, { workerData: { path: file, compression: file.endsWith('.zstd') ? 'zstd' : 'none', expectedId: id, expectedEventCount: Number(count) } })
  w.once('message', m => resolve(m.ok ? { ok: true } : { ok: false, error: m.message }))
  w.once('error', e => resolve({ ok: false, error: String(e) }))
})
console.log(JSON.stringify(r))
process.exitCode = r.ok ? 0 : 1

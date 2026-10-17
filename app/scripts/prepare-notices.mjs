#!/usr/bin/env node
// Refresh the checked-in resource after scripts/collect-licenses.py. No network or dependencies.
import { createHash } from 'node:crypto'
import { readFile, writeFile, rename, unlink } from 'node:fs/promises'
import { dirname, resolve, relative, isAbsolute } from 'node:path'
import { fileURLToPath } from 'node:url'
import { gzipSync } from 'node:zlib'

const root = resolve(dirname(fileURLToPath(import.meta.url)), '../..')
const output = resolve(root, 'app/internal/legal/notices.txt.gz')
if (process.argv.slice(2).some(arg => arg !== '--check')) throw new Error('Usage: node app/scripts/prepare-notices.mjs [--check]')
const index = JSON.parse(await readFile(resolve(root, 'licenses/INDEX.json'), 'utf8'))
const chunks = ['koto — licenses and third-party notices\n']
for (const name of ['LICENSE', 'THIRD_PARTY_NOTICES.md', ...Object.keys(index.files).sort().map(name => `licenses/${name}`)]) {
  const path = resolve(root, name)
  if (relative(root, path).startsWith('..') || isAbsolute(relative(root, path))) throw new Error(`Invalid notice path: ${name}`)
  const content = await readFile(path)
  if (name.startsWith('licenses/') && createHash('sha256').update(content).digest('hex') !== index.files[name.slice(9)]) throw new Error(`License index is stale: ${name}`)
  chunks.push(`\n\n${name}\n${'─'.repeat(24)}\n\n${content.toString('utf8').trimEnd()}\n`)
}
const data = gzipSync(chunks.join(''), { level: 9 })
data[9] = 255 // Deterministic gzip OS field on every build host.
const existing = await readFile(output).catch(error => { if (error.code === 'ENOENT') return Buffer.alloc(0); throw error })
if (process.argv.includes('--check')) {
  if (!existing.equals(data)) throw new Error('Embedded notices are stale. Run node app/scripts/prepare-notices.mjs')
  console.log('Embedded notices match the license sources.')
} else if (!existing.equals(data)) {
  const temporary = `${output}.${process.pid}.tmp`
  try { await writeFile(temporary, data); await rename(temporary, output) } finally { await unlink(temporary).catch(() => {}) }
  console.log(`Embedded ${Object.keys(index.files).length + 2} notices (${data.length} compressed bytes).`)
} else console.log('Embedded notices are current.')

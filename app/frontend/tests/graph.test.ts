import assert from 'node:assert/strict'
import { edgePath, layoutGraph } from '../src/analysis/graph.ts'
import type { AnalysisNode, AnalysisResult } from '../src/api/types.ts'

const node = (id: string, parent: string, turn: number, sessions: number[]): AnalysisNode => ({ id, parent, turn, sessions, hash: id, text: id, preview: id, truncated: false })
const data: AnalysisResult = {
  harness: 'test',
  sessions: ['c', 'a', 'b', 'd'].map(selector => ({ selector, title: selector, turns: 4, nodes: [] })),
  nodes: [node('a4', 'a3', 4, [1]), node('start', '', 1, [0, 1, 2]), node('other', '', 1, [3]), node('b3', 'common', 3, [2]), node('common', 'start', 2, [0, 1, 2]), node('a3', 'common', 3, [1]), node('c3', 'common', 3, [0])],
}
const graph = layoutGraph(data)
assert.deepEqual(graph.rows.map(r => r.node.turn), [1, 1, 2, 3, 3, 3, 4])
for (const [index, row] of graph.rows.entries()) {
  if (!row.node.parent) { assert.equal(row.parentRow, -1); continue }
  assert.ok(row.parentRow < index, 'parents always above their children')
  assert.equal(graph.rows[row.parentRow].node.id, row.node.parent)
  assert.ok(edgePath(row, index, graph.rows).includes('V'))
}
assert.equal(new Set(graph.rows.filter(r => r.node.turn === 3).map(r => r.lane)).size, 3)
assert.equal(graph.rows.find(r => r.node.id === 'start')!.lane, graph.rows.find(r => r.node.id === 'a4')!.lane)
const remapped = { ...data, sessions: [...data.sessions].reverse(), nodes: [...data.nodes].reverse().map(n => ({ ...n, sessions: n.sessions.map(i => data.sessions.length - 1 - i) })) }
const again = layoutGraph(remapped)
assert.deepEqual(again.rows.map(r => [r.node.id, r.lane, again.color(r.lane)]), graph.rows.map(r => [r.node.id, r.lane, graph.color(r.lane)]), 'reordering the input must not change branch colors or lanes')

const many: AnalysisResult = { harness: 'test', sessions: Array.from({ length: 32 }, (_, i) => ({ selector: String(i).padStart(2, '0'), title: String(i), turns: 2, nodes: [] })), nodes: [] }
many.nodes = [node('shared', '', 1, many.sessions.map((_, i) => i)), ...many.sessions.map((_, i) => node('fork' + i, 'shared', 2, [i]))]
const fan = layoutGraph(many)
assert.equal(new Set(fan.rows.slice(1).map(r => r.lane)).size, 32)
assert.equal(new Set(fan.rows.slice(1).map(r => fan.color(r.lane))).size, 32)
assert.ok(fan.rows.slice(1).every(r => r.parentRow === 0))
assert.equal(layoutGraph({ harness: 'test', sessions: [], nodes: [] }).rows.length, 0)
const linear = { harness: 'test', sessions: [data.sessions[0]], nodes: Array.from({ length: 15000 }, (_, i) => node(String(i), i ? String(i - 1) : '', i + 1, [0])) }
assert.equal(layoutGraph(linear).rows.at(-1)?.parentRow, 14998)
console.log('Branch graph: shared prefixes, forks, independent roots, stable colors, chronology, 32 branches and 15,000 turns passed')

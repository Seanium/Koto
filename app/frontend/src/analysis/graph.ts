import type { AnalysisNode, AnalysisResult } from '../api/types'

const colors = ['#4e9276', '#b18332', '#9b7296', '#5e88b1', '#c07759', '#6f8e48', '#ae657c', '#668e91', '#897cb4', '#a08c61', '#bb7671', '#6d8baf']
export interface GraphRow { node: AnalysisNode; lane: number; parentRow: number }

/** Session lanes are assigned by identity, never by selection or arrival order.
 * Shared prefixes occupy their first member's lane; divergent suffixes keep separate lanes.
 * Sorting by turn keeps every edge going down and all views in chronological order. */
export function layoutGraph(data: AnalysisResult) {
  const order = data.sessions.map((s, index) => ({ id: s.selector, index })).sort((a, b) => a.id < b.id ? -1 : a.id > b.id ? 1 : 0)
  const lanes = new Map(order.map((s, lane) => [s.index, lane]))
  const laneOf = (node: AnalysisNode) => Math.min(...node.sessions.map(i => lanes.get(i)!))
  const rows: GraphRow[] = data.nodes.filter(n => n.turn > 0).map(node => ({ node, lane: laneOf(node), parentRow: -1 }))
    .sort((a, b) => a.node.turn - b.node.turn || a.lane - b.lane || a.node.id.localeCompare(b.node.id))
  const rowByID = new Map(rows.map((r, i) => [r.node.id, i]))
  for (const row of rows) row.parentRow = rowByID.get(row.node.parent) ?? -1
  const color = (lane: number) => colors[lane] ?? `hsl(${(lane * 137.508) % 360} 34% 48%)`
  return { rows, sessionLane: (index: number) => lanes.get(index)!, color }
}

export const GRAPH_ROW = 56
export const laneX = (lane: number) => 24 + lane * 22
export function edgePath(row: GraphRow, index: number, rows: GraphRow[]) {
  if (row.parentRow < 0) return ''
  const x1 = laneX(rows[row.parentRow].lane), x2 = laneX(row.lane)
  const y1 = row.parentRow * GRAPH_ROW + GRAPH_ROW / 2, y2 = index * GRAPH_ROW + GRAPH_ROW / 2
  return x1 === x2 ? `M ${x1} ${y1} V ${y2}` : `M ${x1} ${y1} C ${x1} ${y1 + 28}, ${x2} ${y1 + 12}, ${x2} ${y1 + 40} V ${y2}`
}

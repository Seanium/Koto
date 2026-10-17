/** Content-based sequence alignment. Unique anchors use O(n log n); small repeated spans
 * use bounded LCS. Large ambiguous spans stay explicit replacements, never false matches. */
export interface Pair { left?: number; right?: number; kind: 'equal' | 'changed' | 'added' | 'removed' }
export function align(a: readonly string[], b: readonly string[]): Pair[] {
  const matches: [number, number][] = []
  const work: [number, number, number, number][] = [[0, a.length, 0, b.length]]
  while (work.length) {
    let [as, ae, bs, be] = work.pop()!
    while (as < ae && bs < be && a[as] === b[bs]) matches.push([as++, bs++])
    while (as < ae && bs < be && a[ae - 1] === b[be - 1]) matches.push([--ae, --be])
    if (as === ae || bs === be) continue
    const n = ae - as, m = be - bs
    if (n * m <= 250000) {
      const d = new Uint32Array((n + 1) * (m + 1)), w = m + 1
      for (let i = n - 1; i >= 0; i--) for (let j = m - 1; j >= 0; j--)
        d[i * w + j] = a[as + i] === b[bs + j] ? 1 + d[(i + 1) * w + j + 1] : Math.max(d[(i + 1) * w + j], d[i * w + j + 1])
      let i = 0, j = 0
      while (i < n && j < m) {
        if (a[as + i] === b[bs + j]) { matches.push([as + i++, bs + j++]) }
        else if (d[(i + 1) * w + j] >= d[i * w + j + 1]) i++
        else j++
      }
      continue
    }
    const unique = (xs: readonly string[], start: number, end: number) => {
      const map = new Map<string, number>()
      for (let i = start; i < end; i++) map.set(xs[i], map.has(xs[i]) ? -1 : i)
      return map
    }
    const am = unique(a, as, ae), bm = unique(b, bs, be), candidates: [number, number][] = []
    for (const [key, i] of am) { const j = bm.get(key); if (i >= 0 && j !== undefined && j >= 0) candidates.push([i, j]) }
    const tails: number[] = [], prev = new Int32Array(candidates.length).fill(-1)
    candidates.forEach((p, i) => {
      let lo = 0, hi = tails.length
      while (lo < hi) { const mid = (lo + hi) >>> 1; if (candidates[tails[mid]][1] < p[1]) lo = mid + 1; else hi = mid }
      if (lo) prev[i] = tails[lo - 1]
      tails[lo] = i
    })
    const anchors: [number, number][] = []
    for (let i = tails.at(-1) ?? -1; i >= 0; i = prev[i]) anchors.push(candidates[i])
    anchors.reverse()
    for (const [i, j] of anchors) { work.push([as, i, bs, j]); matches.push([i, j]); as = i + 1; bs = j + 1 }
    if (anchors.length) work.push([as, ae, bs, be])
  }
  matches.sort((x, y) => x[0] - y[0])
  const out: Pair[] = []
  let i = 0, j = 0
  for (const [x, y] of [...matches, [a.length, b.length]]) {
    while (i < x || j < y) {
      const left = i < x ? i++ : undefined, right = j < y ? j++ : undefined
      out.push({ left, right, kind: left === undefined ? 'added' : right === undefined ? 'removed' : 'changed' })
    }
    if (x < a.length) { out.push({ left: i++, right: j++, kind: 'equal' }) }
  }
  return out
}

import { tr } from '../i18n/core.ts'
import { useLayoutEffect, useRef } from 'react'
import { SplitPane } from '../ui/SplitPane'

type Line = { kind: string; a?: string; b?: string }

/** Each side owns a contiguous text selection; aligned rows keep sequence differences readable. */
export function DiffColumns({ lines }: { lines: Line[] }) {
  const host = useRef<HTMLDivElement>(null)
  useLayoutEffect(() => {
    const root = host.current!
    const columns = [...root.querySelectorAll<HTMLElement>('.diff-column-content')]
    const left = [...columns[0].children] as HTMLElement[], right = [...columns[1].children] as HTMLElement[]
    let frame = 0
    const measure = () => {
      cancelAnimationFrame(frame)
      frame = requestAnimationFrame(() => {
        const heights = left.map((row, i) => Math.ceil(Math.max(row.firstElementChild!.getBoundingClientRect().height, right[i].firstElementChild!.getBoundingClientRect().height)))
        for (let i = 0; i < heights.length; i++) {
          const height = `${heights[i]}px`
          if (left[i].style.minHeight !== height) left[i].style.minHeight = right[i].style.minHeight = height
        }
      })
    }
    const observer = new ResizeObserver(measure)
    columns.forEach(c => observer.observe(c)); measure()
    return () => { observer.disconnect(); cancelAnimationFrame(frame) }
  }, [lines])
  const column = (side: 'left' | 'right') => <div className="diff-column scroll grow selectable" data-side={side} tabIndex={0} aria-label={side === 'left' ? tr("左侧对话内容") : tr("右侧对话内容")}
    onKeyDown={e => { if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'a') { e.preventDefault(); const range = document.createRange(); range.selectNodeContents(e.currentTarget); const selection = window.getSelection(); selection?.removeAllRanges(); selection?.addRange(range) } }}
    onPointerDown={() => { if (host.current) host.current.dataset.selectionSide = side }} onFocus={() => { if (host.current) host.current.dataset.selectionSide = side }}>
    <div className="diff-column-content">{lines.map((line, i) => {
      const value = side === 'left' ? line.a : line.b
      return <div className="diff-line" key={i} data-change={line.kind === 'equal' ? undefined : value === undefined ? 'empty' : side === 'left' ? 'removed' : 'added'}><div className="diff-line-content">{value ?? '\u00a0'}</div></div>
    })}</div>
  </div>
  return <div ref={host} className="diff-columns col grow" style={{ minHeight: 0 }}><SplitPane name="comparison-text" label={tr("左右对话宽度")} ratio={.5} min={200} minRest={200} minRatio={.25} maxRatio={.75} first={column('left')} second={column('right')} /></div>
}

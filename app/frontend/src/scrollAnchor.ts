/** Preserve a visible element when earlier content is prepended. Tail appends must
 * not be counted as added content above the reader. A new scroll gesture wins. */
export function captureScrollAnchor(container: HTMLElement | null, selector: string): () => void {
  if (!container) return () => {}
  const top = container.getBoundingClientRect().top
  const anchor = [...container.querySelectorAll<HTMLElement>(selector)].find(element => element.getBoundingClientRect().bottom > top)
  const y = anchor?.getBoundingClientRect().top
  let moved = false
  const stop = () => { moved = true }
  const events = ['wheel', 'touchmove', 'keydown', 'pointerdown'] as const
  events.forEach(event => container.addEventListener(event, stop, { passive: true }))
  return () => {
    events.forEach(event => container.removeEventListener(event, stop))
    if (!moved && anchor && y !== undefined && container.contains(anchor)) {
      const delta = anchor.getBoundingClientRect().top - y
      if (Math.abs(delta) > 1) container.scrollTop += delta
    }
  }
}

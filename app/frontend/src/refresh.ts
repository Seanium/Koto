/** Share concurrent reads, but repeat a read requested after the first one started.
 * A mutation's caller must not receive a snapshot captured before that mutation. */
export function coalescedRefresh(read: () => Promise<void>): () => Promise<void> {
  let inflight: Promise<void> | undefined
  let queued = false
  return () => {
    if (inflight) { queued = true; return inflight }
    inflight = (async () => {
      try {
        do { queued = false; await read() } while (queued)
      } finally { inflight = undefined }
    })()
    return inflight
  }
}

import { useEffect, useSyncExternalStore } from 'react'
import { shell } from '../api/client'

// Sidebar and copy destinations share one preference snapshot, including optimistic moves.
let state: { saved: string[]; ready: boolean } = { saved: [], ready: false }
let loading: Promise<void> | undefined
const listeners = new Set<() => void>()
const snapshot = () => state
const subscribe = (listener: () => void) => { listeners.add(listener); return () => { listeners.delete(listener) } }
export function setAgentPreference(saved: readonly string[]) {
  state = { saved: [...saved], ready: true }
  listeners.forEach(listener => listener())
}
export function useAgentPreference() {
  const value = useSyncExternalStore(subscribe, snapshot)
  useEffect(() => {
    loading ??= shell.prefs().then(p => setAgentPreference(p.agentOrder ?? []), () => setAgentPreference([]))
  }, [])
  return value
}

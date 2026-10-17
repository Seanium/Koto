// The main window ("/"; settings is a view inside it).

import { StrictMode, useEffect, useState, useSyncExternalStore } from 'react'
import { createRoot } from 'react-dom/client'
import './theme.css'
import { observeSystemAppearance, setAppearance } from './appearance'
import './ui/ui.css'
import { useHub } from './store'
import { shell } from './api/client'
import { MainWindow } from './screens/Main'
import { getLanguage, getLocale, setLanguage, subscribeLanguage } from './i18n/core.ts'

// Uncaught errors go to the app's log (stderr), where they are found when a screen misbehaves.
window.addEventListener('error', e => shell.mark(`error: ${e.message}`))
window.addEventListener('unhandledrejection', e => shell.mark(`error: ${e.reason instanceof Error ? e.reason.message : String(e.reason)}`))

function App() {
  const hub = useHub()
  const locale = useSyncExternalStore(subscribeLanguage, getLocale)
  const [ready, setReady] = useState(false)
  useEffect(observeSystemAppearance, [])
  useEffect(() => { let live = true; void shell.prefs().then(p => { if (live) { setLanguage(p.language); setAppearance(p.appearance); setReady(true) } }, () => { if (live) setReady(true) }); const changed = () => setLanguage(getLanguage()); window.addEventListener('languagechange', changed); return () => { live = false; window.removeEventListener('languagechange', changed) } }, [])
  useEffect(() => { if (ready) void shell.setUILocale(locale) }, [ready, locale])
  return ready ? <MainWindow hub={hub} /> : null
}

createRoot(document.getElementById('root')!).render(<StrictMode><App /></StrictMode>)

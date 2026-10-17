import english from './en.json' with { type: 'json' }
import traditional from './zh-TW.json' with { type: 'json' }
import japanese from './ja.json' with { type: 'json' }
import german from './de.json' with { type: 'json' }

export type Locale = 'zh' | 'zh-TW' | 'en' | 'ja' | 'de'
export type Language = 'system' | Locale
export const languages: { id: Language; name: string }[] = [{ id: 'system', name: '跟随系统' }, { id: 'en', name: 'English' }, { id: 'zh', name: '简体中文' }, { id: 'zh-TW', name: '繁體中文' }, { id: 'ja', name: '日本語' }, { id: 'de', name: 'Deutsch' }]
const isLocale = (value: string): value is Locale => ['en', 'zh', 'zh-TW', 'ja', 'de'].includes(value)
export function resolveLocale(preference: string, system: string): Locale {
  if (isLocale(preference)) return preference
  const tag = system.toLowerCase().replaceAll('_', '-')
  if (/^zh(?:-|$)/.test(tag)) return /(?:^|-)(?:tw|hk|mo|hant)(?:-|$)/.test(tag) ? 'zh-TW' : 'zh'
  if (/^ja(?:-|$)/.test(tag)) return 'ja'
  if (/^de(?:-|$)/.test(tag)) return 'de'
  return 'en'
}
const system = () => typeof navigator === 'undefined' ? 'en' : navigator.language
let preference: Language = 'system', locale = resolveLocale(preference, system())
const listeners = new Set<() => void>()
export const getLocale = () => locale
export const getLanguage = () => preference
export const subscribeLanguage = (listener: () => void) => { listeners.add(listener); return () => { listeners.delete(listener) } }
export function setLanguage(value: string) {
  preference = isLocale(value) ? value : 'system'
  const next = resolveLocale(preference, system())
  if (typeof document !== 'undefined') document.documentElement.lang = next === 'zh' ? 'zh-CN' : next
  if (next !== locale) { locale = next; listeners.forEach(listener => listener()) }
}
export const catalogs: Record<Exclude<Locale, 'zh'>, Record<string, string>> = { en: english, 'zh-TW': traditional, ja: japanese, de: german }
export function tr(key: string, values: unknown[] = []): string {
  const text = locale === 'zh' ? key : catalogs[locale][key] ?? english[key as keyof typeof english] ?? key
  return text.replace(/\{(\d+)\}/g, (match, index) => Number(index) < values.length ? String(values[Number(index)]) : match)
}

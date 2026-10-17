// Keep the web content independent of native-shell theme support (not every
// platform overrides prefers-color-scheme when an app preference changes).
export type Appearance = '' | 'light' | 'dark'
let appearance: Appearance = ''

export function setAppearance(value: Appearance) {
  appearance = value
  document.documentElement.dataset.appearance = value || (matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light')
}

export function observeSystemAppearance() {
  const media = matchMedia('(prefers-color-scheme: dark)')
  const changed = () => { if (!appearance) setAppearance('') }
  changed()
  media.addEventListener('change', changed)
  return () => media.removeEventListener('change', changed)
}

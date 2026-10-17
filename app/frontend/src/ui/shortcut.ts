/** Display the same modifier shortcuts using the desktop platform's key names. */
export function shortcut(text: string, platform = typeof navigator === 'undefined' ? '' : navigator.platform): string {
  if (/mac/i.test(platform)) return text
  return text.replaceAll('⌥⌘', 'Ctrl+Alt+').replaceAll('⌘', 'Ctrl+').replaceAll('⌥', 'Alt+')
    .replaceAll('⇧', 'Shift+').replaceAll('⌫', 'Backspace').replaceAll('↵', 'Enter')
}

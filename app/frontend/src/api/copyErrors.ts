import { tr } from '../i18n/core.ts'

/** Only classify explicit native project diagnostics; unrelated missing files stay unchanged. */
export function projectFolderError(reason: string): string | undefined {
  if (/\bcwd [\s\S]+? does not exist; choose an existing project folder\b/.test(reason)) {
    return tr('原项目目录已不存在，请选择一个存在的目录。')
  }
  if (/\bproject path is not a directory\b/.test(reason)) {
    return tr('项目路径不是目录，请在上方选择一个现有目录后再复制。')
  }
}

export function copyBlocker(notes: string[]): { message: string; detail?: string } {
  for (const note of notes) {
    const message = projectFolderError(note)
    if (message) return { message }
  }
  return { message: tr('暂时无法复制'), detail: notes.join('; ') }
}

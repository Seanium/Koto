import type { ReactNode } from 'react'

// Lucide static SVG paths, upstream 1ef60af944de207e0d5176905a0759ba12e6ee8d.
// ISC + Feather-derived MIT notice: licenses/assets/lucide-LICENSE.
// Paths are unchanged; koto supplies component size, color and a shared stroke.
const paths = {
  'arrow-down': <><path d="M12 5v14" /> <path d="m19 12-7 7-7-7" /></>,
  'arrow-up': <><path d="m5 12 7-7 7 7" /> <path d="M12 19V5" /></>,
  'check': <><path d="M20 6 9 17l-5-5" /></>,
  'chevron-down': <><path d="m6 9 6 6 6-6" /></>,
  'chevron-left': <><path d="m15 18-6-6 6-6" /></>,
  'chevron-right': <><path d="m9 18 6-6-6-6" /></>,
  'copy': <><rect width="14" height="14" x="8" y="8" rx="2" ry="2" /> <path d="M4 16c-1.1 0-2-.9-2-2V4c0-1.1.9-2 2-2h10c1.1 0 2 .9 2 2" /></>,
  'ellipsis': <><circle cx="12" cy="12" r="1" /> <circle cx="19" cy="12" r="1" /> <circle cx="5" cy="12" r="1" /></>,
  'file-text': <><path d="M6 22a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h8a2.4 2.4 0 0 1 1.704.706l3.588 3.588A2.4 2.4 0 0 1 20 8v12a2 2 0 0 1-2 2z" /> <path d="M14 2v5a1 1 0 0 0 1 1h5" /> <path d="M10 9H8" /> <path d="M16 13H8" /> <path d="M16 17H8" /></>,
  'folder': <><path d="M20 20a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2h-7.9a2 2 0 0 1-1.69-.9L9.6 3.9A2 2 0 0 0 7.93 3H4a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2Z" /></>,
  // Lucide 0.468.0/icons/github.svg (brand icons were later removed upstream).
  'github': <><path d="M15 22v-4a4.8 4.8 0 0 0-1-3.5c3 0 6-2 6-5.5.08-1.25-.27-2.48-1-3.5.28-1.15.28-2.35 0-3.5 0 0-1 0-3 1.5-2.64-.5-5.36-.5-8 0C6 2 5 2 5 2c-.3 1.15-.3 2.35 0 3.5A5.403 5.403 0 0 0 4 9c0 3.5 3 5.5 6 5.5-.39.49-.68 1.05-.85 1.65-.17.6-.22 1.23-.15 1.85v4" /><path d="M9 18c-4.51 2-5-2-7-2" /></>,
  'git-fork': <><circle cx="12" cy="18" r="3" /><circle cx="6" cy="6" r="3" /><circle cx="18" cy="6" r="3" /><path d="M18 9v2c0 .6-.4 1-1 1H7c-.6 0-1-.4-1-1V9" /><path d="M12 12v3" /></>,
  'info': <><circle cx="12" cy="12" r="10" /> <path d="M12 16v-4" /> <path d="M12 8h.01" /></>,
  'messages-square': <><path d="M16 10a2 2 0 0 1-2 2H6.828a2 2 0 0 0-1.414.586l-2.202 2.202A.71.71 0 0 1 2 14.286V4a2 2 0 0 1 2-2h10a2 2 0 0 1 2 2z" /> <path d="M20 9a2 2 0 0 1 2 2v10.286a.71.71 0 0 1-1.212.502l-2.202-2.202A2 2 0 0 0 17.172 19H10a2 2 0 0 1-2-2v-1" /></>,
  'monitor': <><rect width="20" height="14" x="2" y="3" rx="2" /> <line x1="8" x2="16" y1="21" y2="21" /> <line x1="12" x2="12" y1="17" y2="21" /></>,
  'refresh-cw': <><path d="M3 12a9 9 0 0 1 9-9 9.75 9.75 0 0 1 6.74 2.74L21 8" /> <path d="M21 3v5h-5" /> <path d="M21 12a9 9 0 0 1-9 9 9.75 9.75 0 0 1-6.74-2.74L3 16" /> <path d="M8 16H3v5" /></>,
  'rotate-cw': <><path d="M21 12a9 9 0 1 1-9-9c2.52 0 4.93 1 6.74 2.74L21 8" /> <path d="M21 3v5h-5" /></>,
  'search': <><path d="m21 21-4.34-4.34" /> <circle cx="11" cy="11" r="8" /></>,
  'settings': <><path d="M9.671 4.136a2.34 2.34 0 0 1 4.659 0 2.34 2.34 0 0 0 3.319 1.915 2.34 2.34 0 0 1 2.33 4.033 2.34 2.34 0 0 0 0 3.831 2.34 2.34 0 0 1-2.33 4.033 2.34 2.34 0 0 0-3.319 1.915 2.34 2.34 0 0 1-4.659 0 2.34 2.34 0 0 0-3.32-1.915 2.34 2.34 0 0 1-2.33-4.033 2.34 2.34 0 0 0 0-3.831A2.34 2.34 0 0 1 6.35 6.051a2.34 2.34 0 0 0 3.319-1.915" /> <circle cx="12" cy="12" r="3" /></>,
  'terminal': <><path d="M12 19h8" /> <path d="m4 17 6-6-6-6" /></>,
  'x': <><path d="M18 6 6 18" /> <path d="m6 6 12 12" /></>,
} satisfies Record<string, ReactNode>

function icon(name: keyof typeof paths, size: number) {
  return <svg width={size} height={size} viewBox="0 0 24 24" aria-hidden="true" focusable="false" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" data-icon={name}>{paths[name]}</svg>
}

export const Icon = {
  github: icon('github', 16),
  info: icon('info', 16),
  file: icon('file-text', 16),
  refresh: icon('rotate-cw', 16),
  back: icon('chevron-left', 12),
  chevron: icon('chevron-down', 12),
  gear: icon('settings', 16),
  check: icon('check', 16),
  copy: icon('copy', 16),
  terminal: icon('terminal', 16),
  desktop: icon('monitor', 16),
  all: icon('messages-square', 16),
  more: icon('ellipsis', 16),
  disclosure: icon('chevron-right', 12),
  folder: icon('folder', 16),
  branch: icon('git-fork', 16),
  left: icon('chevron-left', 12),
  right: icon('chevron-right', 12),
  up: icon('arrow-up', 16),
  down: icon('arrow-down', 16),
  search: icon('search', 16),
  close: icon('x', 16),
  sync: icon('refresh-cw', 16),
}

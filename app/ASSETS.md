# Third-party assets

## Interface icons — `frontend/src/ui/icons.tsx`

The App's controls use a static subset of [Lucide](https://github.com/lucide-icons/lucide/tree/1ef60af944de207e0d5176905a0759ba12e6ee8d/icons),
pinned to revision `1ef60af944de207e0d5176905a0759ba12e6ee8d`. The original SVG paths
are unchanged; their wrapper sets size, `currentColor`, and the common stroke style.
Only used icons are included. The ISC and Feather-derived MIT notices are preserved in
`licenses/assets/lucide-LICENSE`. Agent marks and the application icon remain separate
brand assets.

## Agent (harness) marks — `frontend/src/assets/harness/`

| File | Harness | Upstream icon |
|---|---|---|
| `claude-code.svg` | Claude Code | `claudecode-color` |
| `codex.svg` | Codex CLI | `codex-color` |
| `pi.svg` | pi | `pi` (monochrome; drawn as a mask in the text colour) |
| `dsh.svg` | DeepSeek Harness | `deepseek-color` |
| `antigravity.svg` | Antigravity | `antigravity-color` |
| `opencode.svg` | OpenCode | `opencode` (monochrome) |
| `cursor.svg` | Cursor CLI | `cursor` |

- **Source:** [lobehub/lobe-icons](https://github.com/lobehub/lobe-icons), MIT License, © LobeHub.
  Except for Cursor, copied unchanged from magpie's vendored set (`internal/gui/assets/icons/` in
  [yetone/magpie](https://github.com/yetone/magpie), MIT License, © 2026 yetone), which takes them
  from lobe-icons. The app does not depend on magpie; only these files were copied.
- `cursor.svg` is copied unchanged from [LobeHub Icons](https://github.com/lobehub/lobe-icons/blob/8a1b2821bb768c484fe8da20c5e1838b6bf551a9/packages/static-svg/icons/cursor.svg) under the same MIT notice.
- **Trademarks:** the logos are trademarks of their respective owners (Anthropic, OpenAI, DeepSeek,
  Google, the pi project). They are used only to identify which agent a conversation lives in; no
  endorsement is implied.
The `zcode.svg` mark comes from the official [ZCodeAboutLogo](https://github.com/zai-org/ZCode/blob/aac4755666d09fdcd70272fcf063c077a639015f/packages/ui/src/components/ui/ZCodeAboutLogo.tsx), Apache-2.0. It retains the original paths in a standalone SVG. Its license is preserved separately in `licenses/assets/zcode-LICENSE`.

`workbuddy.svg` is the unchanged WorkBuddy mark from Tencent's signed WorkBuddy 5.8.3
and WorkBuddy AI 5.8.4 App bundles (`renderer/assets/workbuddy-app-icon-BRLoOH8-.svg`).
Both editions contain identical artwork, SHA-256
`f44b9dd4290398dc16e6bf71385374f4f1ed0b6453729ec6916830dd066ebbb5`.
It identifies the corresponding installed App. The artwork and trademark belong to
Tencent and are not covered by koto's MIT license.

- To add a harness: drop its lobe-icons SVG here and register it in
  `MARKS` in `frontend/src/ui/index.tsx` (monochrome marks are inlined with `?raw` so they take the text colour).

## Application and menu-bar icons — `brand/`

All platforms and the website use the same fixed Apple speech-balloon artwork from
`brand/appicon.png`. This 1024 × 1024 PNG was rendered on macOS using Apple Color Emoji;
its SHA-256 is `526e5bbfe59c248c6101603558892199b2778743b7ba2a9a716880c7e55027ba`.
The checked-in master is retained unchanged. No runtime font lookup, Unicode label,
or platform-specific emoji fallback is used. The tray image is not a template, preserving
its shading on both light and dark desktops.

No Apple font file is included. The rendered Apple artwork is not covered by koto's MIT license.

| File | Used for |
| --- | --- |
| `brand/appicon.png` | Fixed master, Windows/Linux runtime, About window, Linux launcher |
| `brand/trayicon.png` | 64 px platform tray icon |
| `build/darwin/icons.icns` | macOS Finder, running Dock and DMG icon, including Retina representations |
| `build/windows/icon.ico` | Windows executable, launcher and taskbar |
| `../site/assets/koto.png` | 128 px website header and footer mark |
| `../site/favicon.png` | 64 px website favicon |

Generate derivative sizes from the existing master with project-local sharp and Wails:

```sh
npm install --prefix app/.tools/icon-generation sharp@0.35.5
node app/scripts/generate-icons.cjs
```

`KOTO_ICON_SHARP_PATH` and `KOTO_WAILS` can select existing local tools. Rebuild the app
after regeneration because the runtime PNGs are embedded in Go. macOS packages use
`CFBundleIconFile` and ICNS without replacing the running App's icon, so Finder and the Dock
receive the same standard macOS presentation. Windows uses ICO; Linux packages install the master PNG.

## macOS installer background

`brand/installer.svg` defines the neutral background, two cards, and the arrow. On a Mac,
`generate-installer.cjs` and `render-installer-emoji.swift` render 🐙 and 🐳 with the installed
Apple Color Emoji font into `brand/installer.png` and `brand/installer@2x.png`. The DMG includes
these rendered backgrounds, without the font file.

Regenerate the backgrounds with project-local `sharp`:

```sh
npm install --prefix app/.tools/icon-generation sharp@0.35.5
node app/scripts/generate-installer.cjs
```

`KOTO_ICON_SHARP_PATH` can select an existing project-local `sharp` package.

The actual draggable items remain `koto.app` and the Applications shortcut. The backgrounds
illustrate a transfer between Agents; dragging the app installs it without copying sessions.

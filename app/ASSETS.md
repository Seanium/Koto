# Third-party assets

## Agent (harness) marks — `frontend/src/assets/harness/`

| File | Harness | Upstream icon |
|---|---|---|
| `claude-code.svg` | Claude Code | `claudecode-color` |
| `codex.svg` | Codex CLI | `codex-color` |
| `pi.svg` | pi | `pi` (monochrome; drawn as a mask in the text colour) |
| `dsh.svg` | DeepSeek Harness | `deepseek-color` |
| `antigravity.svg` | Antigravity | `antigravity-color` |

- **Source:** [lobehub/lobe-icons](https://github.com/lobehub/lobe-icons), MIT License, © LobeHub.
  Copied unchanged from magpie's vendored set (`internal/gui/assets/icons/` in
  [yetone/magpie](https://github.com/yetone/magpie), MIT License, © 2026 yetone), which takes them
  from lobe-icons. The app does not depend on magpie; only these files were copied.
- **Trademarks:** the logos are trademarks of their respective owners (Anthropic, OpenAI, DeepSeek,
  Google, the pi project). They are used only to identify which agent a conversation lives in; no
  endorsement is implied.
- To add a harness: drop its lobe-icons SVG here and register it in
  `MARKS` in `frontend/src/ui/index.tsx` (monochrome marks are inlined with `?raw` so they take the text colour).

## Beta application and menu-bar icons — `brand/`

The Beta uses 💬 rendered with macOS's installed **Apple Color Emoji** font. The generated
PNG, ICNS, ICO, and tray assets are included in the platform packages. `generate-icons.cjs`
and `render-app-emoji.swift` generate them on a Mac; their appearance follows the build
machine's macOS version.

On macOS, `platform_darwin.go` also renders the running Dock and menu-bar icons in memory
using the user's installed font. The tray image is not a template, preserving the emoji's
colors. If the font is unavailable at runtime, the Dock retains its bundle icon and the tray
uses a text label.

The website's wordmark and favicon use Unicode text rendered by the visitor's system.
No Apple font file is included in the source or installers. The rendered Apple emoji
artwork is not covered by koto's MIT license.

| File | Used for |
| --- | --- |
| `brand/appicon.png` | Packaged application icon |
| `brand/trayicon.png` | Platform tray icon |
| `build/darwin/icons.icns` | Finder/prelaunch and DMG icon, including Retina representations |
| `build/windows/icon.ico` | Windows icon |

To regenerate the icons on macOS with the project-local Wails CLI:

```sh
node app/scripts/generate-icons.cjs
```

After regeneration, rebuild the app because tray images are embedded in Go. macOS uses
`CFBundleIconFile` and ICNS. Linux packages install the rendered PNG launcher icon.

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

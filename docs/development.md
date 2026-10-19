# Development

[English](development.md) · [简体中文](zh/development.md) · [繁體中文](zh-TW/development.md) · [日本語](ja/development.md) · [Deutsch](de/development.md)

The source tree keeps the desktop and CLI in `app/`. The desktop uses Wails v3 with the system WebView, Go, modernc SQLite, React, and zustand. There is no Node.js runtime in the application backend.

## Layout

- `app/frontend/`: interface, translations, and UI tests.
- `app/internal/`: connectors, copy planning, comparison, and local storage.
- `app/build/`, `app/scripts/`: native packaging and verification.
- `site/`: project website; `docs/`: user and contributor guides in all app languages.
- `updates/`: Cloudflare update service and pinned local Wrangler; see [deployment](../updates/README.md).
- `scripts/`: read-only source checks and third-party license maintenance.
- `app/.tools/`, `app/.cache/`: ignored local environments and test evidence; `app/bin/`: generated packages.

## Prerequisites

- Go 1.26 or newer, matching `app/go.mod`.
- Node.js and npm compatible with `app/frontend/package-lock.json`; use Node.js 24 or newer for the TypeScript test command below.
- Wails CLI matching the version in `app/go.mod` for Wails development and generated bindings.
- The native compiler and system WebView development packages required by Wails on the target operating system.

`app/env.sh` uses project-local Go in `app/.tools/go` and Wails in `app/.tools/bin`. Prepare these tools before running the commands below.

## Build and check

From the repository root:

```sh
source app/env.sh
export npm_config_cache="$PWD/app/.cache/npm"
export TMPDIR="$PWD/app/.cache/tmp"
mkdir -p "$TMPDIR"
npm --prefix app/frontend ci
npm --prefix app/frontend run build
go -C app test -race ./...
node --experimental-strip-types --test app/frontend/tests/*.test.ts
node --test site/*.test.mjs updates/*.test.mjs
go -C app build -o bin/koto ./cmd/koto
```

Build the frontend before testing the desktop package because Go embeds `frontend/dist`. The race detector requires a supported platform and native C toolchain. If a Go service signature changes, regenerate TypeScript bindings from `app/`:

```sh
wails3 generate bindings -clean=true -ts -i
```

The optional DeepSeek log check needs its local package and Node.js. Install the package with the `tools:dsh` task; otherwise this check is skipped.

## Isolated sessions

Generate only synthetic data for UI work:

```sh
cd app
go run ./cmd/seed
KOTO_APP_SANDBOX="$PWD/.sandbox" wails3 dev -config ./build/config.yml
```

The seed tool creates `.sandbox` with a `.koto-sandbox` marker. Re-seeding clears only a marked sandbox and refuses an unmarked existing directory. Never pass a personal Agent Harness root to the seed tool.

`app/scripts/preview-mac.sh` builds a macOS preview using the synthetic sandbox.

Optional `agy_native` and `codex_native` tests require explicit test roots, CLI paths, and output directories. Do not point them at existing personal sessions. Default tests use their own temporary directories.

## Packaging

Run the packaging command on its target OS. Use `amd64` instead of `arm64` for Intel/x64:

```sh
app/scripts/package-mac.sh arm64
app/scripts/linux-package.sh arm64
app/scripts/package-windows.sh arm64
app/scripts/package-cli.sh linux arm64
```

macOS DMG packaging also needs Python 3 and `uv` (`KOTO_UV` can select a project-local executable). The script installs its pinned build dependencies in `app/.tools/dmgbuild` and caches downloads in `app/.cache/uv`; it does not change system packages.

Packages go to `app/bin/release/`. macOS uses ad-hoc signing. Linux needs GTK3, WebKitGTK 4.1, GCC, pkg-config, and dpkg-deb. Windows produces separate App and CLI executables.

The project page is buildless. Preview it with:

```sh
UV_CACHE_DIR="$PWD/app/.cache/uv" uv venv app/.cache/release-venv
app/.cache/release-venv/bin/python -m http.server 4173 --bind 127.0.0.1 --directory site
```

Pages deployment and [native CI](ci.md) are manual. CI records test, packaging, and WebView results separately; All three platforms are enabled by default.

For a Windows site preview, use `app\.cache\release-venv\Scripts\python.exe`.

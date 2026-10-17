# Native CI

[English](ci.md) · [简体中文](zh/ci.md) · [繁體中文](zh-TW/ci.md) · [日本語](ja/ci.md) · [Deutsch](de/ci.md)

`.github/workflows/ci.yml` is a manually dispatched workflow. It tests the frontend and Go packages, builds packages, and attempts an actual native WebView smoke test on each host. It has only `contents: read` permission and uploads CI artifacts; it does not create a release, push code, or deploy the site.

The default matrix covers macOS, Linux, and Windows, each on arm64 and amd64. Use `include_windows` to disable Windows jobs for a focused run.

| Target | Native runner label |
| --- | --- |
| macOS arm64 | `macos-15` |
| macOS Intel / amd64 | `macos-15-intel` |
| Linux arm64 | `ubuntu-24.04-arm` |
| Linux amd64 | `ubuntu-24.04` |
| Windows arm64 | `windows-11-arm` |
| Windows amd64 | `windows-2025` |

Runner labels follow the [GitHub hosted-runner list](https://docs.github.com/en/actions/reference/runners/github-hosted-runners). The workflow checks actual host and tool architectures; cross-compilation does not replace an unavailable native runner.

Go, Node, Wails, Codex, and OpenCode use `app/.tools`; caches and evidence use `app/.cache`. Downloads are SHA-256 checked. Linux system dependencies are installed only on the disposable runner.

## What a result means

Each artifact includes `stages.json`, which records frontend tests, Go tests, packaging, and native GUI checks independently. Packages are CI previews. Producing an installer is not evidence of successful installation, and cross-compiling a binary is not evidence of native GUI execution.

macOS checks the app bundle and DMG, then runs the native WebView against synthetic sessions. Linux extracts the DEB and runs its GTK3/WebKitGTK application under Xvfb. The smoke contract requires JSON with `passed: true`, the matching OS and architecture, and completion of the application checks; a zero process exit alone is insufficient.

Windows GUI checks require the [WebView2 Runtime](https://learn.microsoft.com/en-us/microsoft-edge/webview2/concepts/distribution) and an interactive desktop. Missing either fails the GUI check. Built packages and stage results remain available for diagnosis.

App and CLI are separate artifacts. Tests run the CLI independently and ensure desktop packages do not bundle it. Windows checks run both executables directly from paths containing spaces and Unicode, with no installer or PATH changes.

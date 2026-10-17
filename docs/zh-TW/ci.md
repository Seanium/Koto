# 原生 CI

[English](../ci.md) · [简体中文](../zh/ci.md) · [繁體中文](ci.md) · [日本語](../ja/ci.md) · [Deutsch](../de/ci.md)

`.github/workflows/ci.yml` 是手動觸發的工作流程，會測試前端和 Go 套件、建構安裝套件，並嘗試在各主機執行實際的原生 WebView 冒煙測試。權限只有 `contents: read`，僅上傳 CI 產物，不建立 release、不 push 程式碼，也不部署網站。

預設矩陣涵蓋 macOS 與 Linux。只有明確啟用 `include_windows` 才執行 Windows 任務。

| 目標 | 原生 runner 標籤 |
| --- | --- |
| macOS arm64 | `macos-15` |
| macOS Intel / amd64 | `macos-15-intel` |
| Linux arm64 | `ubuntu-24.04-arm` |
| Linux amd64 | `ubuntu-24.04` |
| Windows arm64 | `windows-11-arm` |
| Windows amd64 | `windows-2025` |

Runner 標籤依據 [GitHub 託管 runner 清單](https://docs.github.com/en/actions/reference/runners/github-hosted-runners)。工作流程檢查實際主機與工具架構，不以交叉編譯替代不可用的原生 runner。

Go、Node、Wails 與 NSIS 使用 `app/.tools`，快取和測試紀錄使用 `app/.cache`。下載會驗證 SHA-256；Linux 系統相依套件只安裝在暫存 runner 上。

## 如何解讀結果

每份產物包含 `stages.json`，分別記錄前端測試、Go 測試、封裝和原生 GUI 檢查。安裝套件是 CI 預覽產物。產生安裝程式不等於安裝成功，交叉編譯出二進位檔也不等於執行過原生 GUI。

macOS 檢查 app bundle 和 DMG，再以合成工作階段執行原生 WebView。Linux 解開 DEB，在 Xvfb 下執行 GTK3/WebKitGTK 應用程式。測試結果 JSON 必須含 `passed: true`、相符的作業系統與架構，並完成應用程式檢查；程序結束代碼為 0 本身不足以通過。

Windows 介面測試需要 [WebView2 Runtime](https://learn.microsoft.com/en-us/microsoft-edge/webview2/concepts/distribution) 與互動式桌面，缺少任一條件都會失敗。已建置的套件與分階段結果會保留，方便排查。

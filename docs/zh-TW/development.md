# 開發指南

[English](../development.md) · [简体中文](../zh/development.md) · [繁體中文](development.md) · [日本語](../ja/development.md) · [Deutsch](../de/development.md)

桌面端與 CLI 原始碼位於 `app/`。桌面端使用 Wails v3、系統 WebView、Go、modernc SQLite、React 和 zustand，應用程式後端沒有 Node.js 執行環境。

## 目錄

- `app/frontend/`：介面、翻譯與前端測試。
- `app/internal/`：連接器、複製計畫、比較與本機儲存。
- `app/build/`、`app/scripts/`：原生封裝與驗證。
- `site/`：專案網站；`docs/`：與 App 語言一致的使用及開發文件。
- `scripts/`：唯讀原始碼檢查與第三方授權維護。
- `app/.tools/`、`app/.cache/`：由 Git 忽略的本機環境與測試證據；`app/bin/`：產生的安裝套件。

## 環境需求

- 與 `app/go.mod` 一致的 Go 1.26 或更新版本。
- 相容於 `app/frontend/package-lock.json` 的 Node.js 和 npm。下方 TypeScript 測試命令使用 Node.js 24 或更新版本。
- Wails 開發和繫結產生需要與 `app/go.mod` 中版本一致的 Wails CLI。
- 目標作業系統上 Wails 所需的原生編譯器與系統 WebView 開發套件。

`app/env.sh` 使用專案內的 Go（`app/.tools/go`）與 Wails（`app/.tools/bin`）。準備好工具後再執行下方命令。

## 建構與檢查

在儲存庫根目錄執行：

```sh
source app/env.sh
export npm_config_cache="$PWD/app/.cache/npm"
export TMPDIR="$PWD/app/.cache/tmp"
mkdir -p "$TMPDIR"
npm --prefix app/frontend ci
npm --prefix app/frontend run build
go -C app test -race ./...
node --experimental-strip-types --test app/frontend/tests/*.test.ts
go -C app build -o bin/koto ./cmd/koto
```

Go 會嵌入 `frontend/dist`，因此測試桌面套件前應先建構前端。race detector 需要支援的平台與原生 C 工具鏈。Go 服務簽章變更時，從 `app/` 重新產生 TypeScript 繫結：

```sh
wails3 generate bindings -clean=true -ts -i
```

DeepSeek 原生日誌檢查需要本機對應套件與 Node.js；以 `tools:dsh` 任務安裝，缺少時略過此項檢查。

## 隔離工作階段

介面開發只產生合成資料：

```sh
cd app
go run ./cmd/seed
KOTO_APP_SANDBOX="$PWD/.sandbox" wails3 dev -config ./build/config.yml
```

seed 工具建立帶有 `.koto-sandbox` 標記的 `.sandbox`。重新產生時只清空有標記的沙箱，拒絕沒有標記的既有目錄。不要向 seed 工具傳入個人 Agent 根目錄。

`app/scripts/preview-mac.sh` 建置使用合成沙箱的 macOS 預覽應用程式。

選用的 `agy_native` 和 `codex_native` 測試需要明確提供測試根目錄、CLI 路徑和輸出目錄。不要指向既有個人工作階段。預設測試使用各自的暫存目錄。

## 封裝

在對應作業系統上執行封裝命令；Intel/x64 請將 `arm64` 改為 `amd64`：

```sh
app/scripts/package-mac.sh arm64
app/scripts/linux-package.sh arm64
```

macOS DMG 打包還需要 Python 3 與 `uv`（可用 `KOTO_UV` 指定專案內的程式）。腳本將固定版本的建構依賴安裝在 `app/.tools/dmgbuild`，下載快取放在 `app/.cache/uv`，不會修改系統依賴。

產物放在 `app/bin/release/`。macOS 使用 ad-hoc 簽署。Linux 需要 GTK3、WebKitGTK 4.1、GCC、pkg-config 與 dpkg-deb。Windows 建置指令碼尚未驗收。

專案首頁無需建構，可這樣預覽：

```sh
UV_CACHE_DIR="$PWD/app/.cache/uv" uv venv app/.cache/release-venv
app/.cache/release-venv/bin/python -m http.server 4173 --bind 127.0.0.1 --directory site
```

Pages 部署與[原生 CI](ci.md)皆手動觸發。CI 分別記錄測試、封裝與 WebView 結果，預設關閉 Windows 任務。

Windows 預覽網站時使用 `app\.cache\release-venv\Scripts\python.exe`。

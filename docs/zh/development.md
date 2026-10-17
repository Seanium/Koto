# 开发指南

[English](../development.md) · [简体中文](development.md) · [繁體中文](../zh-TW/development.md) · [日本語](../ja/development.md) · [Deutsch](../de/development.md)

桌面端与 CLI 源码位于 `app/`。桌面端使用 Wails v3、系统 WebView、Go、modernc SQLite、React 和 zustand，应用后端没有 Node.js 运行时。

## 目录

- `app/frontend/`：界面、翻译和前端测试。
- `app/internal/`：连接器、复制计划、对比与本地存储。
- `app/build/`、`app/scripts/`：原生打包与验证。
- `site/`：项目网站；`docs/`：与 App 语言一致的使用和开发文档。
- `scripts/`：只读源码检查与第三方许可维护。
- `app/.tools/`、`app/.cache/`：被 Git 忽略的本地环境与测试证据；`app/bin/`：生成的安装包。

## 环境要求

- 与 `app/go.mod` 一致的 Go 1.26 或更新版本。
- 兼容 `app/frontend/package-lock.json` 的 Node.js 和 npm。下方 TypeScript 测试命令使用 Node.js 24 或更新版本。
- Wails 开发和绑定生成需要与 `app/go.mod` 中版本一致的 Wails CLI。
- 目标操作系统上 Wails 所需的原生编译器和系统 WebView 开发包。

`app/env.sh` 使用项目内的 Go（`app/.tools/go`）与 Wails（`app/.tools/bin`）。准备好工具后再执行下方命令。

## 构建与检查

在仓库根目录执行：

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

Go 会嵌入 `frontend/dist`，因此测试桌面包前应先构建前端。race detector 需要受支持的平台和原生 C 工具链。Go 服务签名发生变化时，从 `app/` 重新生成 TypeScript 绑定：

```sh
wails3 generate bindings -clean=true -ts -i
```

DeepSeek 原生日志检查需要本地对应包与 Node.js；用 `tools:dsh` 任务安装，缺少时跳过此项检查。

## 隔离会话

界面开发只生成合成数据：

```sh
cd app
go run ./cmd/seed
KOTO_APP_SANDBOX="$PWD/.sandbox" wails3 dev -config ./build/config.yml
```

seed 工具创建带有 `.koto-sandbox` 标记的 `.sandbox`。重新生成时只清空有标记的沙箱，拒绝没有标记的已有目录。不要向 seed 工具传入个人 Agent 根目录。

`app/scripts/preview-mac.sh` 构建使用合成沙箱的 macOS 预览应用。

可选的 `agy_native` 和 `codex_native` 测试需要显式提供测试根目录、CLI 路径和输出目录。不要指向已有个人会话。默认测试使用各自的临时目录。

## 打包

在对应操作系统上运行打包命令；Intel/x64 将 `arm64` 改为 `amd64`：

```sh
app/scripts/package-mac.sh arm64
app/scripts/linux-package.sh arm64
app/scripts/package-windows.sh arm64
app/scripts/package-cli.sh linux arm64
```

macOS DMG 打包还需要 Python 3 和 `uv`（可用 `KOTO_UV` 指定项目内的程序）。脚本将固定版本的构建依赖安装在 `app/.tools/dmgbuild`，下载缓存放在 `app/.cache/uv`，不会修改系统依赖。

产物放在 `app/bin/release/`。macOS 使用 ad-hoc 签名。Linux 需要 GTK3、WebKitGTK 4.1、GCC、pkg-config 与 dpkg-deb。Windows 分别生成可直接运行的 App 与 CLI。

项目主页无需构建，可这样预览：

```sh
UV_CACHE_DIR="$PWD/app/.cache/uv" uv venv app/.cache/release-venv
app/.cache/release-venv/bin/python -m http.server 4173 --bind 127.0.0.1 --directory site
```

Pages 部署和[原生 CI](ci.md)均手动触发。CI 分别记录测试、打包和 WebView 结果，默认包含三个平台。

Windows 预览网站时使用 `app\.cache\release-venv\Scripts\python.exe`。

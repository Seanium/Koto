# 原生 CI

[English](../ci.md) · [简体中文](ci.md) · [繁體中文](../zh-TW/ci.md) · [日本語](../ja/ci.md) · [Deutsch](../de/ci.md)

`.github/workflows/ci.yml` 是手动触发的工作流，会测试前端和 Go 包、构建安装包，并尝试在各主机执行真实原生 WebView 冒烟测试。权限只有 `contents: read`，仅上传 CI 产物，不创建 release、不 push 代码，也不部署网站。

默认测试 macOS、Linux 和 Windows，各含 arm64 与 amd64。单独排查时，可用 `include_windows` 关闭 Windows 任务。

| 目标 | 原生 runner 标签 |
| --- | --- |
| macOS arm64 | `macos-15` |
| macOS Intel / amd64 | `macos-15-intel` |
| Linux arm64 | `ubuntu-24.04-arm` |
| Linux amd64 | `ubuntu-24.04` |
| Windows arm64 | `windows-11-arm` |
| Windows amd64 | `windows-2025` |

Runner 标签依据 [GitHub 托管 runner 列表](https://docs.github.com/en/actions/reference/runners/github-hosted-runners)。工作流检查实际主机和工具架构，不用交叉编译替代不可用的原生 runner。

Go、Node、Wails、Codex 和 OpenCode 使用 `app/.tools`，缓存与测试记录使用 `app/.cache`。下载校验 SHA-256；Linux 系统依赖只安装在临时 runner 上。

## 如何解读结果

每份产物包含 `stages.json`，分别记录前端测试、Go 测试、打包和原生 GUI 检查。安装包是 CI 预览产物。生成安装器不等于安装成功，交叉编译出二进制也不等于运行过原生 GUI。

macOS 检查 app bundle 和 DMG，然后以合成会话运行原生 WebView。Linux 解开 DEB，在 Xvfb 下运行 GTK3/WebKitGTK 应用。测试结果 JSON 必须含 `passed: true`、匹配的操作系统与架构，并完成应用检查；进程退出码为 0 本身不足以通过。

Windows 界面测试需要 [WebView2 Runtime](https://learn.microsoft.com/en-us/microsoft-edge/webview2/concepts/distribution) 和交互式桌面，缺少任一条件都会失败。已构建的包与分阶段结果会保留，便于排查。

App 与 CLI 单独发布。测试独立运行 CLI，并确认桌面包未捆绑 CLI。Windows 在带空格和中文的路径中直接运行两个 exe，不用安装器，也不修改 PATH。

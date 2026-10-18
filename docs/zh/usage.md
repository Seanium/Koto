# 使用 koto

[English](../usage.md) · [简体中文](usage.md) · [繁體中文](../zh-TW/usage.md) · [日本語](../ja/usage.md) · [Deutsch](../de/usage.md)

koto 读取设置中已启用的 Agent 目录。搜索支持已索引的标题、会话 ID、首条提问、项目路径和 Agent 名称。

## 复制会话

1. 打开会话，点击「选择复制范围」，默认全选。
2. 保留全部内容，或点击、拖动选择轮次、提问和回复。
3. 选择目标 Agent，查看转换说明。复制后可打开新会话继续聊。

也可以复制到同一个 Agent。每次复制都新建会话，按原顺序保留所选消息，来源保持不变。目标使用自己的模型、服务商、权限和系统配置，不带入来源的系统与开发者指令。

缺失文件和不支持的内容会在转换说明中提示，详见[附件支持](compatibility.md)。

## 账号与目录

在会话列表中按来源目录或已识别账号筛选；其他 Agent 目录可在设置中添加。无法识别账号的会话仍会显示，服务商或模型名称不会被当成账号。

复制时可选择目标目录，同产品默认使用来源目录。仅当目标按账号存储会话时才需要选账号；只有一个合法账号会自动选中。续聊使用目标自己的登录或 API 配置，koto 不复制凭据。

```sh
koto profiles --json
koto threads --profile '<profile-id>' --json
koto copy '@<source-profile>:<session-id>' --to pi --to-profile '<target-profile>' --json
```

使用 koto 返回的准确 profile 和会话标识。`--to-root <目录>` 注册现有 Agent 数据目录并复制到其中；`--to-account <id>` 仅用于支持选择账号的目标。多个目录出现相同会话 ID 时，须明确指定 profile。

## 对比会话

进入「会话对比」，选择同一 Agent 的 2–32 条会话。用树查看共同历史与分叉，再任选两条会话逐行对比。

保存的对比只记录会话引用。可以修改名称和会话列表，删除对比不会删除会话。树根据当前内容计算；移动或删除原会话后，对应引用可能失效。

## 让 Agent 帮你复制

App 和 CLI 从 [Releases](https://github.com/Seanium/koto/releases) 分别下载。CLI 不依赖 App 或 WebView，`--help` 内含 Agent 操作说明。

也可以通过命令行安装 CLI：

macOS / Linux:

```sh
curl -fsSL https://kotoai.site/install.sh | sh
```

Windows (PowerShell):

```powershell
irm https://kotoai.site/install.ps1 | iex
```

默认安装到 macOS/Linux 的 `~/.local/bin` 或 Windows 的 `%LOCALAPPDATA%\Programs\koto\bin`。若安装器提示，请将该目录加入 PATH。

将 CLI 放到 PATH 中的目录，或把完整路径告诉 Agent。Windows 续聊命令在 PowerShell 中执行。Windows App 是直接运行的 `.exe`；Linux App 提供二进制和 DEB，均不捆绑 CLI。运行 Linux App 二进制前，先执行 `chmod +x koto-linux-<架构>`。

直接说：**「用 koto 复制当前会话到 pi」**

OpenCode 未提供宿主会话 ID 时，koto 通过正在运行的工具调用确认当前会话。

自动识别使用宿主提供的 `CODEX_THREAD_ID`、`PI_SESSION_ID`、`DSH_SESSION_ID`、`ANTIGRAVITY_CONVERSATION_ID`、`CURSOR_CONVERSATION_ID` 或 `CLAUDE_CODE_SESSION_ID`。没有这些信息时，需要准确的 `agent:session-id`，可直接指定或通过 `KOTO_SESSION` 提供。明确设置的 `KOTO_SESSION` 优先；多个宿主 ID 冲突时会报错。koto 不按时间或项目目录猜测会话。

WorkBuddy 使用 `CODEBUDDY_SESSION_ID` 与匹配的 `WORKBUDDY_CONFIG_DIR` 确认来源；仅有会话 ID 不能确定 WorkBuddy 目录。

回复会说明复制数量、转换情况，并给出继续聊的命令。当前未完成的轮次（包括这条复制请求）不会带入新会话。原会话保持不变，目标 Agent 不会自动启动。

DeepSeek Harness 使用 headless 界面精确续聊。运行命令并输入下一条消息：macOS/Linux 换行后按 **Ctrl+D** 提交；Windows 按提示输入，按 **Enter** 提交。每次运行回答一条消息。

Windows ARM 上使用 OpenCode 1.19.3 时，请选官方 x64 版；原生 ARM 版的终端界面目前无法启动。

## CLI 与 MCP

将下方示例 ID 替换为实际会话 ID。`--dry-run` 预览复制，`--help` 查看完整参数。

```sh
koto scan
koto threads
koto show 'claude-code:<session-id>'
koto current --json
koto copy --current --to pi --json
koto copy 'claude-code:<session-id>' --to pi --dry-run
koto copy 'claude-code:<session-id>' --to pi --json
koto analyze 'claude-code:<session-a>' 'claude-code:<session-b>' --json
```

`koto` 不在 PATH 时，替换为已安装程序的完整路径。`--json` 仅在复制成功后返回 `state: "copied"`，预览返回 `state: "preview"`；失败会返回错误和非零退出码。任意选择提问、回复等内容请使用 App。

`koto mcp` 通过 stdio JSON-RPC 提供只读的 `list_sessions` 和 `get_session` 工具。

## 更新

在 App「设置 → 通用」中检查更新，有新版时点击「更新并重启」。

更新 CLI：

```sh
koto update --check
koto update
```

App 和 CLI 通过 Cloudflare 更新服务检查版本，分别从 GitHub Releases 下载，安装前校验 SHA-256。默认检查正式版和测试版，并标注测试版；不会降级已安装的版本。CLI 可用 `--channel stable` 只检查正式版。

## 设置与存储

语言可选英语、简体中文、繁体中文、日语、德语或跟随系统；外观可选跟随系统、浅色和深色。

koto 在本地保存索引、偏好和对比引用，不保存对话备份或复制历史。复制的附件归新 Agent 会话所有。复制只由用户手动触发。

索引结构或读取规则变化时会自动重建，会话逐步显示；原 Agent 文件和已保存设置保持不变。

App 和 CLI 共用本地数据目录：macOS 为 `~/Library/Application Support/koto`，Windows 为 `%LOCALAPPDATA%\koto`，Linux 为 `$XDG_DATA_HOME/koto`（默认 `~/.local/share/koto`）。可通过 `KOTO_HOME` 或 `--data` 指定其他目录。

# 使用 koto

[English](../usage.md) · [简体中文](usage.md) · [繁體中文](../zh-TW/usage.md) · [日本語](../ja/usage.md) · [Deutsch](../de/usage.md)

koto 读取设置中已启用的 Agent 目录。搜索支持已索引的标题、会话 ID、首条提问、项目路径和 Agent 名称。

## 复制会话

1. 打开会话，点击「选择复制范围」，默认全选。
2. 保留全部内容，或点击、拖动选择轮次、提问和回复。
3. 选择目标 Agent，查看转换说明。复制后可打开新会话继续聊。

也可以复制到同一个 Agent。每次复制都新建会话，按原顺序保留所选消息，来源保持不变。目标使用自己的模型、服务商、权限和系统配置，不带入来源的系统与开发者指令。

缺失文件和不支持的内容会在转换说明中提示，详见[附件支持](compatibility.md)。

## 对比会话

进入「会话对比」，选择同一 Agent 的 2–32 条会话。用树查看共同历史与分叉，再任选两条会话逐行对比。

保存的对比只记录会话引用。可以修改名称和会话列表，删除对比不会删除会话。树根据当前内容计算；移动或删除原会话后，对应引用可能失效。

## 让 Agent 帮你复制

App 和 CLI 从 [Releases](https://github.com/Seanium/koto/releases) 分别下载。CLI 不依赖 App 或 WebView，`--help` 内含 Agent 操作说明。

| 平台 | CLI 配置 |
| --- | --- |
| macOS / Linux | 下载 `koto-cli-<系统>-<架构>`，改名为 `koto`，执行 `chmod +x koto`。 |
| Windows | 下载 `koto-cli-windows-<架构>.exe`，改名为 `koto.exe`。 |

将 CLI 放到 PATH 中的目录，或把完整路径告诉 Agent。Windows 续聊命令在 PowerShell 中执行。Windows App 是直接运行的 `.exe`；Linux App 提供二进制和 DEB，均不捆绑 CLI。

直接说：**「用 koto 复制当前会话到 pi」**

OpenCode 未提供宿主会话 ID 时，koto 通过正在运行的工具调用确认当前会话，无需安装 Skill。

自动识别使用宿主提供的 `CODEX_THREAD_ID`、`PI_SESSION_ID`、`DSH_SESSION_ID`、`ANTIGRAVITY_CONVERSATION_ID` 或 `CLAUDE_CODE_SESSION_ID`。没有这些信息时，需要准确的 `agent:session-id`，可直接指定或通过 `KOTO_SESSION` 提供。明确设置的 `KOTO_SESSION` 优先；多个宿主 ID 冲突时会报错。koto 不按时间或项目目录猜测会话。

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

`koto` 不在 PATH 时，替换为表中的完整路径。`--json` 仅在复制成功后返回 `state: "copied"`，预览返回 `state: "preview"`；失败会返回错误和非零退出码。任意选择提问、回复等内容请使用 App。

`koto trash <selector>` 将会话移入回收站或 Agent 归档区。`koto restore <trash-id>` 恢复会话，拒绝覆盖已有路径。测试时通过 `--data`、Agent 根目录和 `--only` 指向可丢弃的目录。

`koto mcp` 通过 stdio JSON-RPC 提供只读的 `list_sessions` 和 `get_session` 工具。

## 设置与存储

语言可选英语、简体中文、繁体中文、日语、德语或跟随系统；外观可选跟随系统、浅色和深色。

koto 在本地保存索引、偏好、对比引用和回收站信息，不保存对话备份或复制历史。复制的附件归新 Agent 会话所有。复制只由用户手动触发。

# 会话格式与附件

[English](../compatibility.md) · [简体中文](compatibility.md) · [繁體中文](../zh-TW/compatibility.md) · [日本語](../ja/compatibility.md) · [Deutsch](../de/compatibility.md)

下表说明存储格式的支持情况。Agent 升级可能改变格式；附件能否被识别取决于目标模型。

| Agent | 读取 | 创建副本 |
| --- | --- | --- |
| Claude Code / Claude Desktop（Code） | JSONL 与原生分叉 | 新 UUID 与原生 JSONL |
| pi | v3 JSONL 树 | 新会话及新的 ID / 父节点链 |
| Codex | rollout JSONL / zstd | 新原生 rollout，再用 app-server 验证读取 |
| DeepSeek Harness | v4 JSONL / zstd | 新目录与原生压缩日志 |
| Antigravity CLI | SQLite 与 protobuf 轨迹 | 新对话数据库和摘要条目 |
| OpenCode | SQLite v1（`session`、`message`、`part`） | 在原生数据库中新建会话记录 |
| Cursor CLI | 每会话 SQLite `store.db` | 新原生 CLI 会话 |
| Cursor Desktop | `state.vscdb` 中的会话与消息 blob | 新 IDE 对话，在 App 中打开 |
| WorkBuddy / WorkBuddy AI | 国内外独立目录、SQLite 索引与 JSONL | 在已有原生账号下新建会话 |
| ZCode | App / CLI 共用 SQLite 历史与 App 任务索引 | 新原生会话及对应任务条目 |

支持 OpenCode 1.19.3 的 v1 格式；遇到 v2 会话存储会明确拒绝。复制到 OpenCode 需要其 CLI 和已配置的默认模型，续聊使用目标模型。

所有复制都会新建原生会话，不覆盖已有会话 ID 或独立文件。目标 Agent 加载自己的设置，来源的系统、开发者、环境、配置和权限上下文不会复制。目标不支持对应原生表达时，工具记录和推理内容可能转换为可读文本。

Cursor Desktop 与 CLI 的存储和续聊入口独立。ZCode 支持正式版的原生 Agent，不含 Preview 或由 ZCode 启动的外部 Agent。WorkBuddy 国内版与国际版是独立目标。历史账号标识不会改变续聊使用的凭据。

Claude Desktop 支持读取和新建本地 Code 会话。Cowork 的已有本地日志可读取并复制到支持的目标，暂不支持新建 Cowork 任务；云端 Chat 不在本地扫描范围内。

Claude Desktop 中若未出现复制的 Code 会话，请重启应用刷新会话列表。

## 附件

| 材料 | Claude Code | pi | Codex | DeepSeek Harness | Antigravity CLI | OpenCode |
| --- | --- | --- | --- | --- | --- | --- |
| PNG、JPEG、GIF、WebP | 原生图片 | 原生图片 | 原生图片 | 原生图片附件 | 原生 Media | 原生文件 |
| PDF、UTF-8 文本 | 原生文档 | 完整文件 | 完整文件 | 原生文件附件 | 原生 Media | 原生文件 |
| 音频 | 完整文件 | 完整文件 | 原生音频 | 原生文件附件 | 原生 Media | 原生文件 |
| DOCX、ZIP、JSON、其他二进制文件 | 完整文件 | 完整文件 | 完整文件 | 原生文件附件 | 原生 Media | 原生文件 |

| 新增 Agent | 附件表达 |
| --- | --- |
| Cursor CLI / Cursor Desktop | 原生图片；完整文件或文件引用 |
| WorkBuddy / WorkBuddy AI | 原生图片、用户文件引用；工具文件使用校验后的引用 |
| ZCode | 原生媒体与目标会话中的完整文件 |

如果某个角色或目标格式不能内嵌文件，koto 会在新会话的附件目录中保存完整字节，并添加文件引用。工具记录转成文本时，工具结果中的附件仍会保留。

- 附件只从可识别的原生附件来源读取，不读取消息中随意提到的路径。
- 再次复制 koto 管理的文件引用时，会验证目录、真实路径、大小和 SHA-256，拒绝外部符号链接、路径穿越和已被修改的文件。
- 缺失 blob 会被报告。远程 URI 保持引用，不会下载为离线文件。
- 如果原会话只保存了文本附件的片段，koto 复制已保存的内容并标注，不会读取磁盘上的当前文件来补全。
- 单个预览上限为 25 MiB，附件读取上限为 256 MiB。限额不会让文件变成截断的文本摘要。

## 验证边界

默认测试使用合成 fixture 和可丢弃的 Agent 根目录，覆盖受支持 Agent 间的复制、同 Agent 复制、显式选择、重复新建会话、路径冲突、附件与再复制完整性。

原生续聊测试取决于已安装的 Agent、模型和服务商，与默认测试分开执行。目前仍未覆盖所有模型、格式与角色组合，以及全部桌面平台。

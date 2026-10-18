# Session formats and attachments

[English](compatibility.md) · [简体中文](zh/compatibility.md) · [繁體中文](zh-TW/compatibility.md) · [日本語](ja/compatibility.md) · [Deutsch](de/compatibility.md)

The tables describe supported storage formats. Agent updates can change those formats. Attachment recognition depends on the destination model.

| Agent | Read | Create a copy |
| --- | --- | --- |
| Claude Code / Claude Desktop | JSONL and native forks | New UUID and native JSONL |
| pi | v3 JSONL trees | New session with a new ID/parent chain |
| Codex | rollout JSONL / zstd | New native rollout, then app-server read validation |
| DeepSeek Harness | v4 JSONL / zstd | New directory and native compressed log |
| Antigravity CLI | SQLite and protobuf trajectories | New conversation database and summary entry |
| OpenCode | SQLite v1 (`session`, `message`, `part`) | New session rows in the native database |
| Cursor CLI | Per-session SQLite `store.db` | New native CLI session |
| Cursor Desktop | `state.vscdb` with composer and message blobs | New IDE conversation; open it in the App |
| WorkBuddy / WorkBuddy AI | Separate China/international roots, SQLite index and JSONL | New session under an existing native account |
| ZCode | Shared App/CLI SQLite history and App task index | New native session and matching task entry |

OpenCode 1.19.3 uses the supported v1 schema. v2 session storage is rejected explicitly. OpenCode copies require its CLI and a configured default model; continuation uses the destination model.

All copies create a new native session; existing session IDs and standalone files are not overwritten. The target agent loads its own settings; source system/developer/environment/configuration and permission context are not copied. Tools and reasoning may be converted to readable text where the target has no compatible native representation.

Cursor Desktop and CLI use separate stores and continuation methods. ZCode support covers the production native agent, not Preview or external agents launched by ZCode. WorkBuddy and WorkBuddy AI remain separate destinations. Historical account labels do not change the credentials used for continuation.

If a copied session does not appear in Claude Desktop, restart the app to refresh its session list.

## Attachments

| Material | Claude Code | pi | Codex | DeepSeek Harness | Antigravity CLI | OpenCode |
| --- | --- | --- | --- | --- | --- | --- |
| PNG, JPEG, GIF, WebP | Native image | Native image | Native image | Native image attachment | Native Media | Native file |
| PDF, UTF-8 text | Native document | Complete file | Complete file | Native file attachment | Native Media | Native file |
| Audio | Complete file | Complete file | Native audio | Native file attachment | Native Media | Native file |
| DOCX, ZIP, JSON, other binary | Complete file | Complete file | Complete file | Native file attachment | Native Media | Native file |

| Additional Agent | Attachment representation |
| --- | --- |
| Cursor CLI / Cursor Desktop | Native images; complete files or file references |
| WorkBuddy / WorkBuddy AI | Native images and user-file references; tool files use verified references |
| ZCode | Native media and complete files in the destination session |

When a role or destination cannot embed a file, koto keeps the complete bytes in the new session's attachment directory and adds a file reference. Tool-result attachments are preserved even when the tool transcript is converted to text.

- Attachments are read from recognized native attachment sources, not arbitrary paths mentioned in messages.
- Re-copying koto-managed file references checks their directory, real path, size, and SHA-256. External symlinks, traversal, and modified files are rejected.
- Missing blobs are reported. Remote URIs remain references and are not downloaded for offline storage.
- Individual previews are limited to 25 MiB. Attachment reads are limited to 256 MiB. Limits do not convert a file into a truncated text summary.

## Verification boundaries

Default tests use synthetic fixtures and disposable agent roots. They cover copying between supported agents, same-agent copies, explicit selections, repeated new sessions, path conflicts, attachments, and re-copy integrity.

Native continuation tests depend on the installed Agent, model, and provider. They run separately from the default suite; full coverage of models, format/role combinations, and desktop platforms is still incomplete.

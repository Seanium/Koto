# Using koto

[English](usage.md) · [简体中文](zh/usage.md) · [繁體中文](zh-TW/usage.md) · [日本語](ja/usage.md) · [Deutsch](de/usage.md)

koto reads sessions from the Agent folders enabled in Settings. Search matches indexed titles, session IDs, first prompts, project paths, and Agent names.

## Copy a conversation

1. Open a session and click **Select copy range**. All content is selected initially.
2. Keep everything, or click and drag to select turns, prompts, and replies.
3. Choose a destination Agent and review the conversion notes. After copying, open the new session to continue.

You can copy to the same Agent. Each copy creates a new session, keeps the selected messages in order, and leaves the source unchanged. The destination loads its own model, provider, permissions, and system configuration; source system and developer instructions are excluded.

Missing files and unsupported content appear in the conversion notes. See [attachment support](compatibility.md).

## Compare conversations

Choose **Compare sessions** and select 2–32 sessions from the same Agent. The tree shows shared history and branches. Pick any two sessions for a line-by-line diff.

A saved comparison contains only session references. You can edit its name and session list; removing it leaves the sessions intact. The tree is calculated from current content, so a moved or deleted session can become unavailable.

## Copy with your Agent

App and CLI are separate downloads from [Releases](https://github.com/Seanium/koto/releases). The CLI works without the app or a WebView. Its `--help` tells your Agent how to copy a session.

| Platform | CLI setup |
| --- | --- |
| macOS / Linux | Download `koto-cli-<os>-<arch>`, rename it to `koto`, and run `chmod +x koto`. |
| Windows | Download `koto-cli-windows-<arch>.exe` and rename it to `koto.exe`. |

Put the CLI in a folder on PATH, or give your Agent its full path. Run Windows continuation commands in PowerShell. Windows App downloads are standalone `.exe` files; Linux App downloads are a binary or DEB. Neither includes the CLI.

Ask: **“Use koto to copy this session to pi.”**

OpenCode verifies the running shell-tool invocation when no host session ID is available. No Skill installation is needed.

Current-session detection uses `CODEX_THREAD_ID`, `PI_SESSION_ID`, `DSH_SESSION_ID`, `ANTIGRAVITY_CONVERSATION_ID`, or `CLAUDE_CODE_SESSION_ID` when the host provides one. Otherwise, supply the exact `agent:session-id` directly or through `KOTO_SESSION`. An explicit `KOTO_SESSION` takes priority; conflicting host IDs are rejected. koto never chooses a session by recency or project folder.

The reply includes the copied counts, conversion notes, and command to continue. The unfinished current turn, including the copy request, is excluded. The source stays unchanged; the target Agent is not launched automatically.

DeepSeek Harness uses its headless interface for exact-session continuation. Run the command and type your next message. On macOS/Linux, submit on a new line with **Ctrl+D**; on Windows, the command prompts for a message and **Enter** submits it. Each command answers one message.

For OpenCode 1.19.3 on Windows ARM, use the official x64 build; the native ARM build cannot launch its terminal interface.

## CLI and MCP

Replace the example IDs below with actual session IDs. `--dry-run` previews a copy; `--help` lists all options.

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

Use the full executable path from the table when `koto` is not on PATH. `--json` returns `state: "copied"` only after a successful copy; a preview is `state: "preview"`. Failures return an error and a nonzero exit status. Use the app for arbitrary message selections.

`koto trash <selector>` moves a session to trash or its Agent's archive. `koto restore <trash-id>` restores it without overwriting an existing path. For tests, set `--data`, Agent roots, and `--only` to disposable directories.

`koto mcp` exposes read-only `list_sessions` and `get_session` tools over stdio JSON-RPC.

## Settings and storage

Choose English, simplified or traditional Chinese, Japanese, German, or the system language. Appearance supports System, Light, and Dark.

koto stores its index, preferences, comparison references, and trash metadata locally. It does not save conversation backups or copy history. Copied attachments belong to the new Agent session. All copying is manual.

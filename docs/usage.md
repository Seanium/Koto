# Using koto

[English](usage.md) · [简体中文](zh/usage.md) · [繁體中文](zh-TW/usage.md) · [日本語](ja/usage.md) · [Deutsch](de/usage.md)

koto reads sessions from the Agent folders enabled in Settings. Search matches indexed titles, session IDs, first prompts, project paths, and Agent names.

## Copy a conversation

1. Open a session and click **Select copy range**. All content is selected initially.
2. Keep everything, or click and drag to select turns, prompts, and replies.
3. Choose a destination Agent and review the conversion notes. After copying, open the new session to continue.

You can copy to the same Agent. Each copy creates a new session, keeps the selected messages in order, and leaves the source unchanged. The destination loads its own model, provider, permissions, and system configuration; source system and developer instructions are excluded.

Missing files and unsupported content appear in the conversion notes. See [attachment support](compatibility.md).

## Accounts and folders

Use the session-list filters to choose a storage folder or an identified account. Settings can add other Agent folders. Unknown accounts stay visible; a provider or model name is not an account identity.

The copy dialog lets you choose the destination folder. Same-product copies default to the source folder. Choose an account only when the destination stores sessions by account; a single valid account is selected automatically. Continuing uses the destination’s own login or API configuration. koto does not copy credentials.

```sh
koto profiles --json
koto threads --profile '<profile-id>' --json
koto copy '@<source-profile>:<session-id>' --to pi --to-profile '<target-profile>' --json
```

Use the exact profile and session selector returned by koto. `--to-root <folder>` registers an existing Agent data folder as a destination; `--to-account <id>` selects an account only in a destination that supports it. Identical session IDs in different folders require an explicit profile.

## Compare conversations

Choose **Compare sessions** and select 2–32 sessions from the same Agent. The tree shows shared history and branches. Pick any two sessions for a line-by-line diff.

A saved comparison contains only session references. You can edit its name and session list; removing it leaves the sessions intact. The tree is calculated from current content, so a moved or deleted session can become unavailable.

## Copy with your Agent

App and CLI are separate downloads from [Releases](https://github.com/Seanium/koto/releases). The CLI works without the app or a WebView. Its `--help` tells your Agent how to copy a session.

Install the CLI from your terminal:

macOS / Linux:

```sh
curl -fsSL https://kotoai.site/install.sh | sh
```

Windows (PowerShell):

```powershell
irm https://kotoai.site/install.ps1 | iex
```

The default folder is `~/.local/bin` on macOS/Linux and `%LOCALAPPDATA%\Programs\koto\bin` on Windows. Add it to PATH if the installer asks.

Put the CLI in a folder on PATH, or give your Agent its full path. Run Windows continuation commands in PowerShell. Windows App downloads are standalone `.exe` files; Linux App downloads are a binary or DEB. Neither includes the CLI. Before running a Linux App binary, use `chmod +x koto-linux-<arch>`.

Ask: **“Use koto to copy this session to pi.”**

OpenCode verifies the running shell-tool invocation when no host session ID is available.

Current-session detection uses `CODEX_THREAD_ID`, `PI_SESSION_ID`, `DSH_SESSION_ID`, `ANTIGRAVITY_CONVERSATION_ID`, `CURSOR_CONVERSATION_ID`, or `CLAUDE_CODE_SESSION_ID` when the host provides one. Otherwise, supply the exact `agent:session-id` directly or through `KOTO_SESSION`. An explicit `KOTO_SESSION` takes priority; conflicting host IDs are rejected. koto never chooses a session by recency or project folder.

WorkBuddy uses `CODEBUDDY_SESSION_ID` together with its matching `WORKBUDDY_CONFIG_DIR`. A session ID alone does not identify a WorkBuddy profile.

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

Use the full path to the installed executable when `koto` is not on PATH. `--json` returns `state: "copied"` only after a successful copy; a preview is `state: "preview"`. Failures return an error and a nonzero exit status. Use the app for arbitrary message selections.

`koto mcp` exposes read-only `list_sessions` and `get_session` tools over stdio JSON-RPC.

## Updates

In the app, open **Settings → General → Check for updates**. Choose **Update and restart** when a new version is available.

For the CLI:

```sh
koto update --check
koto update
```

Both download from GitHub Releases and verify SHA-256 before installing. Stable versions stay on the stable channel; use `--channel beta` to opt into prereleases.

## Settings and storage

Choose English, simplified or traditional Chinese, Japanese, German, or the system language. Appearance supports System, Light, and Dark.

koto stores its index, preferences, and comparison references locally. It does not save conversation backups or copy history. Copied attachments belong to the new Agent session. All copying is manual.

The index rebuilds automatically when its structure or a reader changes. Sessions appear progressively; original Agent files and saved settings are kept.

App and CLI share the local data folder: `~/Library/Application Support/koto` on macOS, `%LOCALAPPDATA%\koto` on Windows, and `$XDG_DATA_HOME/koto` (default `~/.local/share/koto`) on Linux. `KOTO_HOME` or `--data` overrides it.

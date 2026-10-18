# koto の使い方

[English](../usage.md) · [简体中文](../zh/usage.md) · [繁體中文](../zh-TW/usage.md) · [日本語](usage.md) · [Deutsch](../de/usage.md)

koto は設定で有効にした Agent フォルダーからセッションを読み取ります。検索対象は索引内のタイトル、セッション ID、最初の質問、プロジェクトパス、Agent 名です。

## 対話をコピーする

1. セッションを開いて「コピー範囲を選択」を押します。最初はすべて選択されています。
2. 全選択のまま進めるか、クリックやドラッグでターン、質問、回答を選びます。
3. コピー先の Agent と変換内容を確認します。コピー後は新しいセッションを開いて続けられます。

同じ Agent にもコピーできます。毎回新しいセッションを作成し、選択したメッセージの順序とコピー元を保ちます。コピー先は自身のモデル、プロバイダー、権限、システム設定を使い、コピー元のシステム・開発者指示は引き継ぎません。

不足しているファイルや未対応の内容は変換説明に表示します。[添付ファイルの対応](compatibility.md)も確認してください。

## アカウントとフォルダー

セッション一覧では保存フォルダーや識別済みのアカウントで絞り込めます。別の Agent フォルダーは設定から追加できます。アカウント不明のセッションも表示し、プロバイダー名やモデル名をアカウントとして扱いません。

コピー先のフォルダーを選べます。同じ製品へのコピーは元のフォルダーが既定です。コピー先がアカウント別に保存する場合のみアカウントを選び、有効な候補が 1 つなら自動選択します。続きにはコピー先のログインや API 設定を使い、認証情報はコピーしません。

```sh
koto profiles --json
koto threads --profile '<profile-id>' --json
koto copy '@<source-profile>:<session-id>' --to pi --to-profile '<target-profile>' --json
```

koto が返した正確な profile とセッション識別子を使ってください。`--to-root <folder>` は既存の Agent データフォルダーをコピー先として登録します。`--to-account <id>` はアカウント選択に対応するコピー先専用です。異なるフォルダーに同じセッション ID がある場合は profile を指定します。

## セッションを比較する

「セッションの比較」で同じ Agent のセッションを 2〜32 個選びます。ツリーで共通の履歴と分岐を確認し、任意の 2 つを行ごとに比較できます。

保存する比較データはセッション参照だけです。名前やセッション一覧を変更でき、比較を削除しても元のセッションは残ります。ツリーは現在の内容から計算するため、元のセッションを移動・削除すると参照できなくなることがあります。

## Agent にコピーを頼む

App と CLI は [Releases](https://github.com/Seanium/koto/releases) から個別にダウンロードできます。CLI に App や WebView は不要です。`--help` に Agent 向けの手順があります。

CLI はコマンドからもインストールできます。

macOS / Linux:

```sh
curl -fsSL https://kotoai.site/install.sh | sh
```

Windows (PowerShell):

```powershell
irm https://kotoai.site/install.ps1 | iex
```

既定の保存先は macOS/Linux が `~/.local/bin`、Windows が `%LOCALAPPDATA%\Programs\koto\bin` です。インストーラーの案内に従って PATH に追加してください。

CLI を PATH 内のフォルダーに置くか、Agent にフルパスを伝えてください。Windows の継続コマンドは PowerShell で実行します。Windows App は直接起動する `.exe`、Linux App はバイナリと DEB です。CLI は同梱しません。Linux App のバイナリは、起動前に `chmod +x koto-linux-<arch>` を実行してください。

**「koto で今の対話を pi にコピーして。」**と頼みます。

OpenCode がホストのセッション ID を提供しない場合は、実行中のツール呼び出しで現在のセッションを確認します。

自動識別には、ホストが提供する `CODEX_THREAD_ID`、`PI_SESSION_ID`、`DSH_SESSION_ID`、`ANTIGRAVITY_CONVERSATION_ID`、`CURSOR_CONVERSATION_ID`、`CLAUDE_CODE_SESSION_ID` を使います。提供されない場合は、正確な `agent:session-id` を直接、または `KOTO_SESSION` で指定します。明示的な `KOTO_SESSION` を優先し、複数のホスト ID が競合する場合はエラーを返します。更新日時やプロジェクトフォルダーからは推測しません。

WorkBuddy は `CODEBUDDY_SESSION_ID` と対応する `WORKBUDDY_CONFIG_DIR` でコピー元を確認します。セッション ID だけでは保存先を特定しません。

回答にはコピー数、変換内容、続けるためのコマンドが含まれます。コピーの依頼を含む、未完了のターンは引き継ぎません。コピー元は変わらず、コピー先の Agent は自動起動しません。

DeepSeek Harness は headless モードで指定したセッションを再開します。コマンドを実行して次のメッセージを入力します。macOS/Linux は改行して **Ctrl+D**、Windows は入力欄で **Enter** を押して送信します。1 回の実行で 1 件のメッセージに回答します。

Windows ARM で OpenCode 1.19.3 を使う場合は、公式の x64 版を選んでください。ネイティブ ARM 版ではターミナル画面が起動しません。

## CLI と MCP

以下の ID は実際のセッション ID に置き換えてください。`--dry-run` でコピーをプレビューし、`--help` で全オプションを確認できます。

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

`koto` が PATH にない場合はインストール先のフルパスを使ってください。`--json` はコピー成功時にのみ `state: "copied"`、プレビュー時には `state: "preview"` を返します。失敗時はエラーと 0 以外の終了コードを返します。質問や回答を個別に選ぶ場合はアプリを使ってください。

`koto mcp` は stdio JSON-RPC で読み取り専用の `list_sessions` と `get_session` を提供します。

## アップデート

App の「設定 → 一般」で更新を確認し、新しいバージョンがあれば「更新して再起動」を選びます。

CLI の更新：

```sh
koto update --check
koto update
```

App と CLI は Cloudflare の更新サービスでバージョンを確認し、それぞれ GitHub Releases からダウンロードして、インストール前に SHA-256 を検証します。安定版とテスト版を確認し、テスト版にはその旨を表示します。インストール済みのバージョンより古いものには更新しません。CLI で安定版だけを確認する場合は `--channel stable` を指定してください。

## 設定と保存データ

言語は英語、簡体字中国語、繁体字中国語、日本語、ドイツ語、またはシステム設定に対応します。外観はシステム、ライト、ダークから選べます。

索引、設定、比較の参照をローカルに保存します。対話のバックアップやコピー履歴は保存しません。コピーした添付ファイルは新しい Agent セッションに属します。コピーは手動操作のみです。

索引の構造や読み取り規則が変わると自動で再構築し、セッションを順次表示します。元の Agent ファイルと保存済み設定は保持します。

App と CLI は同じデータフォルダーを使います。macOS は `~/Library/Application Support/koto`、Windows は `%LOCALAPPDATA%\koto`、Linux は `$XDG_DATA_HOME/koto`（既定は `~/.local/share/koto`）です。`KOTO_HOME` または `--data` で変更できます。

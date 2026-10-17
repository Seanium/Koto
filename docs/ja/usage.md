# koto の使い方

[English](../usage.md) · [简体中文](../zh/usage.md) · [繁體中文](../zh-TW/usage.md) · [日本語](usage.md) · [Deutsch](../de/usage.md)

koto は設定で有効にした Agent フォルダーからセッションを読み取ります。検索対象は索引内のタイトル、セッション ID、最初の質問、プロジェクトパス、Agent 名です。

## 対話をコピーする

1. セッションを開いて「コピー範囲を選択」を押します。最初はすべて選択されています。
2. 全選択のまま進めるか、クリックやドラッグでターン、質問、回答を選びます。
3. コピー先の Agent と変換内容を確認します。コピー後は新しいセッションを開いて続けられます。

同じ Agent にもコピーできます。毎回新しいセッションを作成し、選択したメッセージの順序とコピー元を保ちます。コピー先は自身のモデル、プロバイダー、権限、システム設定を使い、コピー元のシステム・開発者指示は引き継ぎません。

不足しているファイルや未対応の内容は変換説明に表示します。[添付ファイルの対応](compatibility.md)も確認してください。

## セッションを比較する

「セッションの比較」で同じ Agent のセッションを 2〜32 個選びます。ツリーで共通の履歴と分岐を確認し、任意の 2 つを行ごとに比較できます。

保存する比較データはセッション参照だけです。名前やセッション一覧を変更でき、比較を削除しても元のセッションは残ります。ツリーは現在の内容から計算するため、元のセッションを移動・削除すると参照できなくなることがあります。

## Agent にコピーを頼む

CLI はアプリに同梱されています。`--help` に Agent 向けの手順があります。

| パッケージ | CLI |
| --- | --- |
| macOS | `/Applications/koto.app/Contents/Helpers/koto` |
| Linux DEB | `koto` |
| Linux アーカイブ | 展開先の `./koto` |

macOS では CLI は PATH に追加されません。Agent がコマンドを見つけられない場合は、上記のパスを伝えてください。アプリを別の場所に置いた場合はパスを変更します。

**「koto で今の対話を pi にコピーして。」**と頼みます。

自動識別には、ホストが提供する `CODEX_THREAD_ID`、`PI_SESSION_ID`、`DSH_SESSION_ID`、`ANTIGRAVITY_CONVERSATION_ID`、`CLAUDE_CODE_SESSION_ID` を使います。提供されない場合は、正確な `agent:session-id` を直接、または `KOTO_SESSION` で指定します。明示的な `KOTO_SESSION` を優先し、複数のホスト ID が競合する場合はエラーを返します。更新日時やプロジェクトフォルダーからは推測しません。

回答にはコピー数、変換内容、続けるためのコマンドが含まれます。コピーの依頼を含む、未完了のターンは引き継ぎません。コピー元は変わらず、コピー先の Agent は自動起動しません。

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

`koto` が PATH にない場合は表のフルパスを使ってください。`--json` はコピー成功時にのみ `state: "copied"`、プレビュー時には `state: "preview"` を返します。失敗時はエラーと 0 以外の終了コードを返します。質問や回答を個別に選ぶ場合はアプリを使ってください。

`koto trash <selector>` はセッションをゴミ箱または Agent のアーカイブへ移動します。`koto restore <trash-id>` は既存パスを上書きせず復元します。テストでは `--data`、Agent ルート、`--only` を使い、破棄できるディレクトリを指定してください。

`koto mcp` は stdio JSON-RPC で読み取り専用の `list_sessions` と `get_session` を提供します。

## 設定と保存データ

言語は英語、簡体字中国語、繁体字中国語、日本語、ドイツ語、またはシステム設定に対応します。外観はシステム、ライト、ダークから選べます。

索引、設定、比較の参照、ゴミ箱情報をローカルに保存します。対話のバックアップやコピー履歴は保存しません。コピーした添付ファイルは新しい Agent セッションに属します。コピーは手動操作のみです。

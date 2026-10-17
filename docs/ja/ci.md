# ネイティブ CI

[English](../ci.md) · [简体中文](../zh/ci.md) · [繁體中文](../zh-TW/ci.md) · [日本語](ci.md) · [Deutsch](../de/ci.md)

`.github/workflows/ci.yml` は手動実行のワークフローです。フロントエンドと Go パッケージをテストし、配布パッケージをビルドし、各ホストで実際のネイティブ WebView スモークテストを試みます。権限は `contents: read` のみです。CI 成果物をアップロードしますが、リリース作成、コードの push、サイト公開は行いません。

標準のマトリクスは macOS と Linux です。Windows ジョブは `include_windows` を明示的に有効にした場合だけ実行します。

| 対象 | ネイティブ runner ラベル |
| --- | --- |
| macOS arm64 | `macos-15` |
| macOS Intel / amd64 | `macos-15-intel` |
| Linux arm64 | `ubuntu-24.04-arm` |
| Linux amd64 | `ubuntu-24.04` |
| Windows arm64 | `windows-11-arm` |
| Windows amd64 | `windows-2025` |

Runner ラベルは [GitHub のホスト型 runner 一覧](https://docs.github.com/en/actions/reference/runners/github-hosted-runners)に基づきます。ワークフローは実際のホストとツールのアーキテクチャを確認します。利用できないネイティブ runner をクロスコンパイルで代用しません。

Go、Node、Wails、NSIS は `app/.tools`、キャッシュとテスト記録は `app/.cache` を使います。ダウンロードは SHA-256 で確認し、Linux のシステム依存関係は一時 runner 内にのみインストールします。

## 結果の意味

各成果物の `stages.json` は、フロントエンドテスト、Go テスト、パッケージ作成、ネイティブ GUI 確認を別々に記録します。パッケージは CI プレビューです。インストーラーの生成はインストール成功の証拠ではなく、クロスコンパイルはネイティブ GUI 実行の証拠ではありません。

macOS は app バンドルと DMG を確認し、合成セッションでネイティブ WebView を実行します。Linux は DEB を展開し、GTK3/WebKitGTK アプリを Xvfb 上で実行します。テスト結果には、`passed: true`、一致する OS とアーキテクチャ、アプリの検証完了を示す JSON が必要です。プロセスの終了コードが 0 だけでは不十分です。

Windows の GUI 検証には [WebView2 Runtime](https://learn.microsoft.com/en-us/microsoft-edge/webview2/concepts/distribution) と操作可能なデスクトップが必要です。どちらかが欠けると検証は失敗します。作成済みパッケージと段階ごとの結果は調査用に残ります。

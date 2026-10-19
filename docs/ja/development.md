# 開発

[English](../development.md) · [简体中文](../zh/development.md) · [繁體中文](../zh-TW/development.md) · [日本語](development.md) · [Deutsch](../de/development.md)

デスクトップと CLI のソースは `app/` にあります。デスクトップは Wails v3 とシステム WebView、Go、modernc SQLite、React、zustand を使います。バックエンドに Node.js ランタイムは含まれません。

## ディレクトリ

- `app/frontend/`：UI、翻訳、フロントエンドテスト。
- `app/internal/`：コネクター、コピー計画、比較、ローカルストレージ。
- `app/build/`、`app/scripts/`：ネイティブパッケージの作成と検証。
- `site/`：プロジェクトサイト。`docs/`：アプリと同じ言語の利用・開発ガイド。
- `updates/`：Cloudflare 更新サービスと固定バージョンのローカル Wrangler。[デプロイ手順](../../updates/README.md)。
- `scripts/`：読み取り専用のソース確認と第三者ライセンスの管理。
- `app/.tools/`、`app/.cache/`：Git 管理外のローカル環境とテスト記録。`app/bin/`：生成したパッケージ。

## 必要な環境

- `app/go.mod` に対応する Go 1.26 以降。
- `app/frontend/package-lock.json` に対応する Node.js と npm。以下の TypeScript テストには Node.js 24 以降を使用します。
- Wails 開発とバインディング生成には、`app/go.mod` と同じバージョンの Wails CLI。
- 対象 OS で Wails が必要とするネイティブコンパイラーとシステム WebView の開発パッケージ。

`app/env.sh` はプロジェクト内の Go（`app/.tools/go`）と Wails（`app/.tools/bin`）を使います。これらを準備してから以下を実行してください。

## ビルドと確認

リポジトリのルートで実行します。

```sh
source app/env.sh
export npm_config_cache="$PWD/app/.cache/npm"
export TMPDIR="$PWD/app/.cache/tmp"
mkdir -p "$TMPDIR"
npm --prefix app/frontend ci
npm --prefix app/frontend run build
go -C app test -race ./...
node --experimental-strip-types --test app/frontend/tests/*.test.ts
node --test site/*.test.mjs updates/*.test.mjs
go -C app build -o bin/koto ./cmd/koto
```

Go が `frontend/dist` を埋め込むため、デスクトップパッケージのテストより先にフロントエンドをビルドしてください。race detector には対応プラットフォームとネイティブ C ツールチェーンが必要です。Go サービスのシグネチャが変わった場合は、`app/` から TypeScript バインディングを再生成します。

```sh
wails3 generate bindings -clean=true -ts -i
```

任意の DeepSeek ログ検証には専用パッケージと Node.js が必要です。`tools:dsh` タスクでインストールでき、未準備の場合はスキップします。

## 分離したセッション

UI 開発には合成データだけを生成します。

```sh
cd app
go run ./cmd/seed
KOTO_APP_SANDBOX="$PWD/.sandbox" wails3 dev -config ./build/config.yml
```

seed ツールは `.koto-sandbox` マーカーを持つ `.sandbox` を作成します。再生成時に消去するのはマーカーのあるサンドボックスだけで、マーカーのない既存ディレクトリは拒否します。個人の Agent Harness ルートを seed ツールに渡さないでください。

`app/scripts/preview-mac.sh` は合成サンドボックスを使う macOS プレビューアプリをビルドします。

任意の `agy_native` と `codex_native` テストには、テストルート、CLI パス、出力ディレクトリを明示する必要があります。既存の個人セッションを指定しないでください。標準テストは専用の一時ディレクトリを使います。

## パッケージ作成

対象 OS でパッケージ作成コマンドを実行します。Intel/x64 では `arm64` を `amd64` に置き換えてください。

```sh
app/scripts/package-mac.sh arm64
app/scripts/linux-package.sh arm64
app/scripts/package-windows.sh arm64
app/scripts/package-cli.sh linux arm64
```

macOS の DMG 作成には Python 3 と `uv` も必要です（`KOTO_UV` でプロジェクト内の実行ファイルを指定できます）。スクリプトは固定バージョンのビルド依存を `app/.tools/dmgbuild` に、ダウンロードキャッシュを `app/.cache/uv` に置きます。システムのパッケージは変更しません。

出力先は `app/bin/release/` です。macOS は ad-hoc 署名を使います。Linux には GTK3、WebKitGTK 4.1、GCC、pkg-config、dpkg-deb が必要です。Windows では App と CLI を別々の実行ファイルとして作成します。

プロジェクトページにビルドは不要です。以下でプレビューできます。

```sh
UV_CACHE_DIR="$PWD/app/.cache/uv" uv venv app/.cache/release-venv
app/.cache/release-venv/bin/python -m http.server 4173 --bind 127.0.0.1 --directory site
```

Pages の公開と[ネイティブ CI](ci.md)は手動実行です。CI はテスト、パッケージ作成、WebView の結果を分けて記録し、標準で 3 つのプラットフォームを有効にしています。

Windows でサイトをプレビューする場合は `app\.cache\release-venv\Scripts\python.exe` を使います。

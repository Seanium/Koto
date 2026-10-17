#!/bin/sh
# A standalone CLI: no frontend build, desktop bundle, or WebView dependency.
set -eu
APP_DIR=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$APP_DIR"
TARGET_OS=${1:?Usage: package-cli.sh darwin|linux|windows arm64|amd64}
ARCH=${2:?Usage: package-cli.sh darwin|linux|windows arm64|amd64}
case "$TARGET_OS/$ARCH" in darwin/arm64|darwin/amd64|linux/arm64|linux/amd64|windows/arm64|windows/amd64) ;; *) echo 'Unsupported CLI target.' >&2; exit 2 ;; esac
SUFFIX=
[ "$TARGET_OS" != windows ] || SUFFIX=.exe
GO=${KOTO_GO:-"$APP_DIR/.tools/go/bin/go"}
[ -x "$GO" ] || GO=go
OUT="$APP_DIR/bin/release"
mkdir -p "$OUT" "$APP_DIR/.cache"
STAGE=$(mktemp -d "$APP_DIR/.cache/cli-package-$TARGET_OS-$ARCH.XXXXXX")
trap 'rm -rf "$STAGE"' EXIT HUP INT TERM
VERSION=$(cat VERSION)
NAME="koto-cli-$TARGET_OS-$ARCH$SUFFIX"
GOOS="$TARGET_OS" GOARCH="$ARCH" CGO_ENABLED=0 GOTOOLCHAIN=local \
  "$GO" build -trimpath -buildvcs=false -ldflags="-w -s -X main.version=$VERSION" -o "$STAGE/$NAME" ./cmd/koto
if [ "$TARGET_OS" = darwin ] && [ "$(uname -s)" = Darwin ]; then
  codesign --force --sign - "$STAGE/$NAME"
  codesign --verify --strict "$STAGE/$NAME"
fi
mv -f "$STAGE/$NAME" "$OUT/$NAME"
node - "$OUT/$NAME" <<'JS'
const fs = require('node:fs'), path = require('node:path'), crypto = require('node:crypto');
const file = process.argv[2];
fs.writeFileSync(file+'.sha256', crypto.createHash('sha256').update(fs.readFileSync(file)).digest('hex')+'  '+path.basename(file)+'\n');
JS
printf '%s\n' "$OUT/$NAME"

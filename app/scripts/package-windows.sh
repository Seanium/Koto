#!/bin/sh
# Build separate portable Windows App and CLI executables in the checkout.
set -eu
APP_DIR=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$APP_DIR"
ARCH=${1:-amd64}
case "$ARCH" in amd64|arm64) ;; *) echo 'Usage: package-windows.sh amd64|arm64' >&2; exit 2 ;; esac
VERSION=$(cat VERSION)
NUMERIC_VERSION=${VERSION%%-*}
OUT="$APP_DIR/bin/release"
STAGE="$APP_DIR/.cache/package-windows-$ARCH"
WINDOWS_HOST=0
EXE_SUFFIX=
case "$(uname -s)" in
  MINGW*|MSYS*|CYGWIN*) WINDOWS_HOST=1; EXE_SUFFIX=.exe ;;
esac
native_path() {
  if [ "$WINDOWS_HOST" = 1 ]; then cygpath -m "$1"; else printf '%s\n' "$1"; fi
}
WAILS=${KOTO_WAILS:-$APP_DIR/.tools/bin/wails3$EXE_SUFFIX}
GO=${KOTO_GO:-$APP_DIR/.tools/go/bin/go$EXE_SUFFIX}
mkdir -p "$OUT" "$STAGE"
# Native Go on Windows requires drive-qualified cache paths.
export GOPATH="$(native_path "$APP_DIR/.cache/gopath")"
export GOMODCACHE="$(native_path "$APP_DIR/.cache/gomod")"
export GOCACHE="$(native_path "$APP_DIR/.cache/gobuild")" GOTOOLCHAIN=local
if [ "${KOTO_SKIP_FRONTEND_BUILD:-0}" != 1 ]; then npm --prefix frontend run build; fi
SYSO="$APP_DIR/koto_windows_$ARCH.syso"
[ ! -e "$SYSO" ] || { echo "Refusing to replace $SYSO" >&2; exit 1; }
trap 'rm -f "$SYSO"' EXIT
node - "$VERSION" "$NUMERIC_VERSION" "$STAGE/info.json" <<'JS'
const fs = require('node:fs');
const info = JSON.parse(fs.readFileSync('build/windows/info.json', 'utf8'));
info.fixed.file_version = process.argv[3];
for (const values of Object.values(info.info)) values.ProductVersion = process.argv[2];
fs.writeFileSync(process.argv[4], JSON.stringify(info));
JS
"$WAILS" generate syso -arch "$ARCH" -icon build/windows/icon.ico -manifest build/windows/wails.exe.manifest -info "$STAGE/info.json" -out "$SYSO"
GOOS=windows GOARCH="$ARCH" CGO_ENABLED=0 "$GO" build -tags production -trimpath -buildvcs=false -ldflags='-w -s -H windowsgui' -o "$OUT/koto-windows-$ARCH.exe" .
node - "$OUT/koto-windows-$ARCH.exe" <<'JS'
const fs = require('node:fs'), path = require('node:path'), crypto = require('node:crypto');
const file = process.argv[2];
fs.writeFileSync(file+'.sha256', crypto.createHash('sha256').update(fs.readFileSync(file)).digest('hex')+'  '+path.basename(file)+'\n');
JS
KOTO_GO="$GO" sh scripts/package-cli.sh windows "$ARCH"
printf '%s\n' "$OUT/koto-windows-$ARCH.exe"

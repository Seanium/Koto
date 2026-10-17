#!/bin/sh
# Build native Windows executables without installing anything on the host.
# NSIS (makensis) is needed for the final installer; it can run inside the Linux VM.
set -eu
APP_DIR=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$APP_DIR"
ARCH=${1:-amd64}
case "$ARCH" in amd64|arm64) ;; *) echo 'Usage: package-windows.sh amd64|arm64' >&2; exit 2 ;; esac
VERSION=$(cat VERSION)
OUT="$APP_DIR/bin/release/windows-$ARCH"
WINDOWS_HOST=0
EXE_SUFFIX=
NSIS_OPTION=-
case "$(uname -s)" in
  MINGW*|MSYS*|CYGWIN*) WINDOWS_HOST=1; EXE_SUFFIX=.exe; NSIS_OPTION=/ ;;
esac
native_path() {
  if [ "$WINDOWS_HOST" = 1 ]; then cygpath -m "$1"; else printf '%s\n' "$1"; fi
}
WAILS=${KOTO_WAILS:-$APP_DIR/.tools/bin/wails3$EXE_SUFFIX}
GO=${KOTO_GO:-$APP_DIR/.tools/go/bin/go$EXE_SUFFIX}
MAKENSIS=${KOTO_MAKENSIS:-makensis$EXE_SUFFIX}
mkdir -p "$OUT" "$APP_DIR/.cache/windows-nsis"
# Native Go on Windows requires drive-qualified cache paths. Do not depend on
# Git Bash's heuristic environment-variable conversion for these values.
export GOPATH="$(native_path "$APP_DIR/.cache/gopath")"
export GOMODCACHE="$(native_path "$APP_DIR/.cache/gomod")"
export GOCACHE="$(native_path "$APP_DIR/.cache/gobuild")" GOTOOLCHAIN=local
if [ "${KOTO_SKIP_BINARY_BUILD:-0}" != 1 ]; then
  if [ "${KOTO_SKIP_FRONTEND_BUILD:-0}" != 1 ]; then npm --prefix frontend run build; fi
  SYSO="$APP_DIR/koto_windows_$ARCH.syso"
  [ ! -e "$SYSO" ] || { echo "Refusing to replace $SYSO" >&2; exit 1; }
  trap 'rm -f "$SYSO"' EXIT
  "$WAILS" generate syso -arch "$ARCH" -icon build/windows/icon.ico -manifest build/windows/wails.exe.manifest -info build/windows/info.json -out "$SYSO"
  GOOS=windows GOARCH="$ARCH" CGO_ENABLED=0 "$GO" build -tags production -trimpath -buildvcs=false -ldflags='-w -s -H windowsgui' -o "$OUT/koto.exe" .
  GOOS=windows GOARCH="$ARCH" CGO_ENABLED=0 "$GO" build -trimpath -buildvcs=false -ldflags="-w -s -X main.version=$VERSION" -o "$OUT/koto-cli.exe" ./cmd/koto
fi
if [ "${KOTO_WINDOWS_BINARIES_ONLY:-0}" = 1 ]; then echo "$OUT"; exit 0; fi
STAGE="$APP_DIR/.cache/windows-nsis"
if [ ! -f "$STAGE/MicrosoftEdgeWebview2Setup.exe" ]; then "$WAILS" generate webview2bootstrapper -dir "$STAGE"; fi
cp build/windows/nsis/project.nsi build/windows/nsis/wails_tools.nsh "$STAGE/"
cp build/windows/icon.ico "$STAGE/icon.ico"
case "$ARCH" in amd64) FLAG=AMD64 ;; arm64) FLAG=ARM64 ;; esac
# Use documented Windows /options and Unix -options. Convert paths explicitly
# and disable MSYS rewriting of /DNAME=value. On Unix this env var is harmless.
MSYS_NO_PATHCONV=1 "$MAKENSIS" "${NSIS_OPTION}V2" "${NSIS_OPTION}DINFO_PRODUCTVERSION=$VERSION" \
  "${NSIS_OPTION}DARG_WAILS_${FLAG}_BINARY=$(native_path "$OUT/koto.exe")" \
  "${NSIS_OPTION}DARG_KOTO_CLI=$(native_path "$OUT/koto-cli.exe")" \
  "${NSIS_OPTION}DARG_KOTO_ICON=$(native_path "$STAGE/icon.ico")" \
  "${NSIS_OPTION}DARG_KOTO_INSTALLER=$(native_path "$APP_DIR/bin/release/koto-$VERSION-windows-$ARCH-setup.exe")" \
  "$(native_path "$STAGE/project.nsi")"

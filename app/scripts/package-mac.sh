#!/bin/sh
# Builds the installer: bin/koto.app (production build) with the koto CLI in Contents/Helpers/koto
# (for MCP and scripts; not beside koto in MacOS/, which a case-insensitive disk takes for the same
# name), then bin/koto-<version>.dmg. One Go binary each, no runtime to bundle. Ad-hoc signed, not
# notarized: on another Mac, open it once with right-click › 打开. Nothing is installed on this machine; the build uses the project-local Go (env.sh).
#
#   app/scripts/package-mac.sh                  # native build, original output paths
#   app/scripts/package-mac.sh arm64            # bin/release/darwin-arm64/koto.app
#   app/scripts/package-mac.sh amd64            # bin/release/darwin-amd64/koto.app
# Explicit architectures produce bin/release/koto-<version>-macos-<arch>.dmg.
# Set KOTO_SKIP_FRONTEND_BUILD=1 only after building frontend/dist once, when
# packaging multiple architectures from exactly the same frontend assets.
# uv creates a pinned build-only environment in .tools/dmgbuild. Finder layout
# is written into the disk image; the builder never changes Finder preferences.
set -eu
usage() {
  echo 'Usage: app/scripts/package-mac.sh [arm64|amd64]'
  echo 'KOTO_SKIP_FRONTEND_BUILD=1 reuses an existing frontend/dist build.'
}
case "${1:-}" in
  -h|--help) usage; exit 0 ;;
  ''|arm64|amd64) ;;
  *) usage >&2; exit 2 ;;
esac
[ "$#" -le 1 ] || { usage >&2; exit 2; }
[ "$(uname -s)" = Darwin ] || { echo 'macOS packaging requires macOS.' >&2; exit 1; }
APP_DIR=$(cd "$(dirname "$0")/.." && pwd)
cd "$APP_DIR"
. ./env.sh
VERSION=$(cat VERSION)
SHORT_VERSION=${VERSION%%-*}
ARCH=${1:-$(go env GOARCH)}
case "$ARCH" in
  arm64) MACH_ARCH=arm64 ;;
  amd64) MACH_ARCH=x86_64 ;;
  *) echo "Unsupported macOS architecture: $ARCH" >&2; exit 2 ;;
esac

if [ "${KOTO_SKIP_FRONTEND_BUILD:-0}" = 1 ]; then
  [ -f frontend/dist/index.html ] || { echo 'Build frontend/dist before skipping the frontend build.' >&2; exit 1; }
else
  npm --prefix frontend run build
fi

if [ "$#" -eq 0 ]; then
  OUT="$APP_DIR/bin/koto.app"
  DMG="$APP_DIR/bin/koto-$VERSION.dmg"
else
  OUT="$APP_DIR/bin/release/darwin-$ARCH/koto.app"
  DMG="$APP_DIR/bin/release/koto-$VERSION-macos-$ARCH.dmg"
fi
mkdir -p "$APP_DIR/.cache" "$(dirname "$OUT")" "$(dirname "$DMG")"
STAGE=$(mktemp -d "$APP_DIR/.cache/mac-package-$ARCH.XXXXXX")
trap 'rm -rf "$STAGE"' EXIT
trap 'exit 1' HUP INT TERM
UV=${KOTO_UV:-$(command -v uv || true)}
[ -n "$UV" ] || { echo 'macOS packaging requires uv; set KOTO_UV to a project-local executable.' >&2; exit 1; }
export UV_CACHE_DIR="$APP_DIR/.cache/uv"
export UV_PYTHON_INSTALL_DIR="$APP_DIR/.tools/uv-python"
if [ ! -x "$APP_DIR/.tools/dmgbuild/bin/python" ]; then
  "$UV" venv --no-python-downloads --python python3 "$APP_DIR/.tools/dmgbuild"
fi
"$UV" pip sync --python "$APP_DIR/.tools/dmgbuild/bin/python" "$APP_DIR/scripts/dmg-requirements.txt"
BUNDLE="$STAGE/koto.app"
mkdir -p "$BUNDLE/Contents/MacOS" "$BUNDLE/Contents/Helpers" "$BUNDLE/Contents/Resources"
export GOOS=darwin GOARCH="$ARCH" CGO_ENABLED=1
export CGO_CFLAGS="${CGO_CFLAGS:-} -mmacosx-version-min=12.0"
export CGO_LDFLAGS="${CGO_LDFLAGS:-} -mmacosx-version-min=12.0"
export MACOSX_DEPLOYMENT_TARGET=12.0
go build -tags production -trimpath -buildvcs=false -ldflags="-w -s" -o "$BUNDLE/Contents/MacOS/koto" .
CGO_ENABLED=0 go build -trimpath -buildvcs=false -ldflags="-w -s -X main.version=$VERSION" -o "$BUNDLE/Contents/Helpers/koto" ./cmd/koto
cp build/darwin/icons.icns "$BUNDLE/Contents/Resources/"
mkdir -p "$BUNDLE/Contents/Resources/legal"
cp "$APP_DIR/../LICENSE" "$APP_DIR/../THIRD_PARTY_NOTICES.md" "$BUNDLE/Contents/Resources/legal/"
cp -R "$APP_DIR/../licenses" "$BUNDLE/Contents/Resources/legal/"
find "$BUNDLE/Contents/Resources/legal" -type f \( -name .DS_Store -o -name '._*' \) -delete
cp build/darwin/Info.plist "$BUNDLE/Contents/Info.plist"
# Apple's bundle version fields accept numeric versions, not SemVer suffixes.
# The app embeds VERSION; Finder's information string retains the beta label.
/usr/libexec/PlistBuddy -c "Set :CFBundleShortVersionString $SHORT_VERSION" "$BUNDLE/Contents/Info.plist"
/usr/libexec/PlistBuddy -c "Set :CFBundleVersion $SHORT_VERSION" "$BUNDLE/Contents/Info.plist"
/usr/libexec/PlistBuddy -c "Set :CFBundleGetInfoString koto $VERSION" "$BUNDLE/Contents/Info.plist"

plutil -lint "$BUNDLE/Contents/Info.plist"
lipo "$BUNDLE/Contents/MacOS/koto" -verify_arch "$MACH_ARCH"
lipo "$BUNDLE/Contents/Helpers/koto" -verify_arch "$MACH_ARCH"
codesign --force --deep --sign - "$BUNDLE"
codesign --verify --deep --strict "$BUNDLE"

TMPDIR="$STAGE" "$APP_DIR/.tools/dmgbuild/bin/dmgbuild" \
  -s "$APP_DIR/scripts/dmg-settings.py" \
  -D "app=$BUNDLE" -D "background=$APP_DIR/brand/installer.png" \
  "koto $VERSION" "$STAGE/koto.dmg"
hdiutil verify "$STAGE/koto.dmg"
"$APP_DIR/.tools/dmgbuild/bin/python" "$APP_DIR/scripts/verify-mac-installer.py" "$STAGE/koto.dmg"
# Replace existing generated artifacts only after both the bundle and image pass.
[ ! -d "$OUT" ] || rm -rf "$OUT"
mv "$BUNDLE" "$OUT"
mv -f "$STAGE/koto.dmg" "$DMG"
(cd "$(dirname "$DMG")" && shasum -a 256 "$(basename "$DMG")") > "$DMG.sha256"
printf '%s\n' "$OUT" "$DMG"

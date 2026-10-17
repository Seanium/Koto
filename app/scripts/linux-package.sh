#!/bin/sh
# Run on Linux after building frontend/dist. No install, upload, or signing.
set -eu
APP_DIR=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$APP_DIR"
[ "$(uname -s)" = Linux ] || { echo 'Run this script inside Linux.' >&2; exit 1; }
GO=${KOTO_GO:-"$APP_DIR/.tools/go-linux-arm64/bin/go"}
[ -x "$GO" ] || GO=go
ARCH=${1:-$("$GO" env GOARCH)}
case "$ARCH" in amd64|arm64) ;; *) echo "Unsupported Linux architecture: $ARCH" >&2; exit 1;; esac
VERSION=$(cat VERSION)
# Debian prereleases sort before the corresponding final version.
DEB_VERSION=$(printf '%s' "$VERSION" | sed 's/-/~/')
export GOTOOLCHAIN=local GOOS=linux GOARCH="$ARCH" CGO_ENABLED=1
export GOCACHE=${GOCACHE:-"$APP_DIR/.cache/linux-gobuild"}
export GOMODCACHE=${GOMODCACHE:-"$APP_DIR/.cache/gomod"}
if [ "$ARCH" = amd64 ] && [ "$(uname -m)" = aarch64 ]; then
  export CC=x86_64-linux-gnu-gcc
  export PKG_CONFIG_LIBDIR=/usr/lib/x86_64-linux-gnu/pkgconfig:/usr/share/pkgconfig
fi
if [ "$ARCH" = arm64 ] && [ "$(uname -m)" = x86_64 ]; then
  export CC=aarch64-linux-gnu-gcc
  export PKG_CONFIG_LIBDIR=/usr/lib/aarch64-linux-gnu/pkgconfig:/usr/share/pkgconfig
fi
pkg-config --exists gtk+-3.0 webkit2gtk-4.1
[ -f frontend/dist/index.html ] || { echo 'Build frontend/dist first.' >&2; exit 1; }
OUT="$APP_DIR/bin/release"
mkdir -p "$OUT" "$APP_DIR/.cache"
STAGE=$(mktemp -d "$APP_DIR/.cache/linux-package-$ARCH.XXXXXX")
trap 'rm -rf "$STAGE"' EXIT HUP INT TERM
ROOT="$STAGE/root"
mkdir -p "$ROOT/usr/lib/koto" "$ROOT/usr/share/applications" "$ROOT/usr/share/icons/hicolor/1024x1024/apps" "$ROOT/DEBIAN"
"$GO" build -tags production,gtk3 -trimpath -buildvcs=false -ldflags='-w -s' -o "$ROOT/usr/lib/koto/koto-desktop" .
cp build/linux/desktop "$ROOT/usr/share/applications/koto.desktop"
cp brand/appicon.png "$ROOT/usr/share/icons/hicolor/1024x1024/apps/koto.png"
mkdir -p "$ROOT/usr/share/doc/koto"
cp "$APP_DIR/../LICENSE" "$APP_DIR/../THIRD_PARTY_NOTICES.md" "$ROOT/usr/share/doc/koto/"
cp -R "$APP_DIR/../licenses" "$ROOT/usr/share/doc/koto/"
find "$ROOT/usr/share/doc/koto" -type f \( -name .DS_Store -o -name '._*' \) -delete
SIZE=$(du -sk "$ROOT/usr" | awk '{print $1}')
LIBC_VERSION=$(getconf GNU_LIBC_VERSION | awk '{print $2}')
cat > "$ROOT/DEBIAN/control" <<EOF
Package: koto
Version: $DEB_VERSION
Architecture: $ARCH
Maintainer: koto contributors
Section: utils
Priority: optional
Installed-Size: $SIZE
Depends: libc6 (>= $LIBC_VERSION), libgtk-3-0, libwebkit2gtk-4.1-0, libayatana-appindicator3-1
Description: Copy coding-agent conversations between harnesses
 koto is a desktop app for reading, comparing, and copying coding-agent sessions.
 The command-line tool is distributed separately.
EOF
cp build/linux/nfpm/scripts/postinstall.sh "$ROOT/DEBIAN/postinst"
chmod 0755 "$ROOT/DEBIAN/postinst"
dpkg-deb --root-owner-group --build "$ROOT" "$OUT/koto-linux-$ARCH.deb"
cp "$ROOT/usr/lib/koto/koto-desktop" "$OUT/koto-linux-$ARCH"
dpkg-deb --info "$OUT/koto-linux-$ARCH.deb"
(cd "$OUT" && sha256sum "koto-linux-$ARCH.deb" "koto-linux-$ARCH" > "koto-linux-$ARCH.sha256")
KOTO_GO="$GO" sh scripts/package-cli.sh linux "$ARCH"
printf '%s\n' "$OUT/koto-linux-$ARCH.deb" "$OUT/koto-linux-$ARCH"

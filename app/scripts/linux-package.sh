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
mkdir -p "$ROOT/usr/lib/koto" "$ROOT/usr/bin" "$ROOT/usr/share/applications" "$ROOT/usr/share/icons/hicolor/1024x1024/apps" "$ROOT/DEBIAN"
"$GO" build -tags production,gtk3 -trimpath -buildvcs=false -ldflags='-w -s' -o "$ROOT/usr/lib/koto/koto-desktop" .
CGO_ENABLED=0 "$GO" build -trimpath -buildvcs=false -ldflags="-w -s -X main.version=$VERSION" -o "$ROOT/usr/bin/koto" ./cmd/koto
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
 koto is a local desktop app and command-line tool for reading, comparing,
 and copying coding-agent sessions.
EOF
cp build/linux/nfpm/scripts/postinstall.sh "$ROOT/DEBIAN/postinst"
chmod 0755 "$ROOT/DEBIAN/postinst"
dpkg-deb --root-owner-group --build "$ROOT" "$OUT/koto-$VERSION-linux-$ARCH.deb"
cp "$ROOT/usr/lib/koto/koto-desktop" "$OUT/koto-$VERSION-linux-$ARCH"
cp "$ROOT/usr/bin/koto" "$OUT/koto-cli-$VERSION-linux-$ARCH"
PORTABLE="$STAGE/koto-$VERSION-linux-$ARCH"
mkdir -p "$PORTABLE"
cp "$ROOT/usr/lib/koto/koto-desktop" "$PORTABLE/koto-desktop"
cp "$ROOT/usr/bin/koto" "$PORTABLE/koto"
cp "$ROOT/usr/share/applications/koto.desktop" "$PORTABLE/"
cp brand/appicon.png "$PORTABLE/koto.png"
cp -R "$ROOT/usr/share/doc/koto" "$PORTABLE/legal"
cat > "$PORTABLE/README.txt" <<EOF
koto $VERSION / Linux $ARCH

Run ./koto-desktop to open the app, or ./koto --help for the CLI.
Requires glibc $LIBC_VERSION or newer, GTK3, WebKitGTK 4.1, and Ayatana
AppIndicator3. On a compatible Debian/Ubuntu system:
  sudo apt install libgtk-3-0 libwebkit2gtk-4.1-0 libayatana-appindicator3-1

The .deb package installs the desktop launcher and the koto command.
This archive does not install anything automatically.
The CLI includes instructions for AI agents in ./koto --help.
No separate Skill setup is required.
EOF
tar -C "$STAGE" -czf "$OUT/koto-$VERSION-linux-$ARCH.tar.gz" "koto-$VERSION-linux-$ARCH"
dpkg-deb --info "$OUT/koto-$VERSION-linux-$ARCH.deb"
(cd "$OUT" && sha256sum "koto-$VERSION-linux-$ARCH.deb" "koto-$VERSION-linux-$ARCH.tar.gz" > "koto-$VERSION-linux-$ARCH.sha256")
printf '%s\n' "$OUT/koto-$VERSION-linux-$ARCH.deb" "$OUT/koto-$VERSION-linux-$ARCH.tar.gz"

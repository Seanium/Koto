#!/bin/sh
# Inspect and extract both archives; compare the shipped binaries and desktop entry.
set -eu
APP_DIR=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
ARCH=${1:?Usage: test-linux-package.sh arm64|amd64}
VERSION=$(cat "$APP_DIR/VERSION")
OUT="$APP_DIR/bin/release"
NAME="koto-$VERSION-linux-$ARCH"
TEST_DIR=$(mktemp -d /tmp/koto-package-check.XXXXXX)
trap 'rm -rf "$TEST_DIR"' EXIT HUP INT TERM
(cd "$OUT" && sha256sum -c "$NAME.sha256")
[ "$(dpkg-deb -f "$OUT/$NAME.deb" Architecture)" = "$ARCH" ]
[ "$(dpkg-deb -f "$OUT/$NAME.deb" Version)" = "$(printf '%s' "$VERSION" | sed 's/-/~/')" ]
dpkg-deb --info "$OUT/$NAME.deb"
dpkg-deb --extract "$OUT/$NAME.deb" "$TEST_DIR/deb"
tar -xzf "$OUT/$NAME.tar.gz" -C "$TEST_DIR"
cmp "$TEST_DIR/deb/usr/lib/koto/koto-desktop" "$TEST_DIR/$NAME/koto-desktop"
cmp "$TEST_DIR/deb/usr/bin/koto" "$TEST_DIR/$NAME/koto"
[ ! -d "$TEST_DIR/deb/usr/share/koto/skills" ]
[ ! -d "$TEST_DIR/$NAME/skills" ]
cmp "$APP_DIR/../LICENSE" "$TEST_DIR/deb/usr/share/doc/koto/LICENSE"
cmp "$APP_DIR/../licenses/INDEX.json" "$TEST_DIR/$NAME/legal/licenses/INDEX.json"
desktop-file-validate "$TEST_DIR/deb/usr/share/applications/koto.desktop"
"$TEST_DIR/$NAME/koto" version
readelf -h "$TEST_DIR/$NAME/koto-desktop" | sed -n '/Class:/p; /Machine:/p'
ldd "$TEST_DIR/$NAME/koto-desktop" > "$TEST_DIR/ldd.txt"
if grep -q 'not found' "$TEST_DIR/ldd.txt"; then
  cat "$TEST_DIR/ldd.txt"
  exit 1
fi
printf 'PASS: %s DEB, tar.gz, desktop entry, CLI, and shared libraries\n' "$ARCH"

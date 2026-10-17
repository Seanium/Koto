#!/bin/sh
# Check desktop and CLI downloads independently; the app must not bundle a CLI.
set -eu
APP_DIR=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
ARCH=${1:?Usage: test-linux-package.sh arm64|amd64}
VERSION=$(cat "$APP_DIR/VERSION")
OUT="$APP_DIR/bin/release"
NAME="koto-linux-$ARCH"
mkdir -p "$APP_DIR/.cache"
TEST_DIR=$(mktemp -d "$APP_DIR/.cache/linux-package-check.XXXXXX")
trap 'rm -rf "$TEST_DIR"' EXIT HUP INT TERM
(cd "$OUT" && sha256sum -c "$NAME.sha256")
(cd "$OUT" && sha256sum -c "koto-cli-linux-$ARCH.sha256")
[ "$(dpkg-deb -f "$OUT/$NAME.deb" Architecture)" = "$ARCH" ]
[ "$(dpkg-deb -f "$OUT/$NAME.deb" Version)" = "$(printf '%s' "$VERSION" | sed 's/-/~/')" ]
dpkg-deb --info "$OUT/$NAME.deb"
dpkg-deb --extract "$OUT/$NAME.deb" "$TEST_DIR/deb"
cmp "$TEST_DIR/deb/usr/lib/koto/koto-desktop" "$OUT/$NAME"
[ ! -f "$TEST_DIR/deb/usr/bin/koto" ]
[ ! -d "$TEST_DIR/deb/usr/share/koto/skills" ]
cmp "$APP_DIR/../LICENSE" "$TEST_DIR/deb/usr/share/doc/koto/LICENSE"
cmp "$APP_DIR/../licenses/INDEX.json" "$TEST_DIR/deb/usr/share/doc/koto/licenses/INDEX.json"
desktop-file-validate "$TEST_DIR/deb/usr/share/applications/koto.desktop"
[ "$("$OUT/koto-cli-linux-$ARCH" version)" = "koto $VERSION" ]
readelf -h "$OUT/$NAME" | sed -n '/Class:/p; /Machine:/p'
ldd "$OUT/$NAME" > "$TEST_DIR/ldd.txt"
if grep -q 'not found' "$TEST_DIR/ldd.txt"; then
  cat "$TEST_DIR/ldd.txt"
  exit 1
fi
printf 'PASS: %s desktop DEB/binary, separate CLI, and shared libraries\n' "$ARCH"

#!/bin/sh
# Builds bin/koto-Preview.app: the app with this checkout's sandbox and PATH baked in (brand.go), so
# it runs on app/.sandbox test data even when opened from Finder. It never reads or writes ~/.claude,
# ~/.codex, ~/.pi, ~/.dsh or ~/.gemini. macOS only; ad-hoc signed, local use only.
set -eu
APP_DIR=$(cd "$(dirname "$0")/.." && pwd)
cd "$APP_DIR"
. ./env.sh
SANDBOX="$APP_DIR/.sandbox"
[ -d "$SANDBOX/hub" ] || go run ./cmd/seed "$SANDBOX"
npm --prefix frontend run build

OUT="$APP_DIR/bin/koto-Preview.app"
[ ! -d "$OUT" ] || rm -r "$OUT"
mkdir -p "$OUT/Contents/MacOS" "$OUT/Contents/Resources"
go build -ldflags "-X 'main.previewSandbox=$SANDBOX' -X 'main.previewPath=$PATH'" -o "$OUT/Contents/MacOS/koto" .
cp build/darwin/icons.icns "$OUT/Contents/Resources/"
sed -e 's#<string>app.koto.desktop.dev</string>#<string>app.koto.desktop.preview</string>#' \
    -e '/<key>CFBundleName<\/key>/{n;s#<string>koto</string>#<string>koto 预览</string>#;}' \
    build/darwin/Info.dev.plist > "$OUT/Contents/Info.plist"
codesign --force --deep --sign - "$OUT"
echo "$OUT"

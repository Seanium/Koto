#!/bin/sh
set -eu
APP_DIR=$(cd "$(dirname "$0")/.." && pwd)
cd "$APP_DIR"
. ./env.sh
mkdir -p .cache
OUT_DIR=$(mktemp -d "$APP_DIR/.cache/platform-check.XXXXXX")
trap '[ ! -f "$OUT_DIR/koto" ] || rm "$OUT_DIR/koto"; rmdir "$OUT_DIR"' EXIT
for target in darwin/arm64 darwin/amd64 linux/amd64 linux/arm64 windows/amd64 windows/arm64; do
    GOOS=${target%/*} GOARCH=${target#*/} CGO_ENABLED=0 go build -o "$OUT_DIR/koto" ./cmd/koto
    echo "CLI build passed: $target"
done

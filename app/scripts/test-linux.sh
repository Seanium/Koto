#!/bin/sh
# A real Linux GTK/WebKit run under Xvfb. Never uses native agent histories.
set -eu
APP_DIR=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$APP_DIR"
ARCH=${1:-arm64}
VERSION=$(cat VERSION)
GO=${KOTO_GO:-"$APP_DIR/.tools/go-linux-arm64/bin/go"}
[ -x "$GO" ] || GO=go
export GOTOOLCHAIN=local
export GOCACHE=${GOCACHE:-"$APP_DIR/.cache/linux-gobuild"}
export GOMODCACHE=${GOMODCACHE:-"$APP_DIR/.cache/gomod"}
EVIDENCE=$(mktemp -d "$APP_DIR/.cache/linux-smoke-$ARCH.XXXXXX")
RUN=$(mktemp -d "/tmp/koto-smoke-$ARCH.XXXXXX")
trap 'cp -a "$RUN/." "$EVIDENCE/"; rm -rf "$RUN"' EXIT HUP INT TERM
export KOTO_APP_SANDBOX="$RUN/sandbox" KOTO_APP_PLATFORM_SMOKE="$RUN/result.json" KOTO_APP_LIVE_MS=0
export XDG_CONFIG_HOME="$RUN/config" XDG_DATA_HOME="$RUN/data" XDG_CACHE_HOME="$RUN/cache"
mkdir -p "$XDG_CONFIG_HOME" "$XDG_DATA_HOME" "$XDG_CACHE_HOME"
CGO_ENABLED=0 GOARCH="$ARCH" "$GO" run ./cmd/seed "$KOTO_APP_SANDBOX"
printf '%s\n' '{"liveMinutes":0,"language":"zh","appearance":"light"}' > "$KOTO_APP_SANDBOX/app-prefs.json"
export GDK_BACKEND=x11 LIBGL_ALWAYS_SOFTWARE=1 WEBKIT_DISABLE_DMABUF_RENDERER=1
BIN=${KOTO_LINUX_BINARY:-"$APP_DIR/bin/release/koto-linux-$ARCH"}
printf 'Linux smoke binary: %s\nEvidence: %s\n' "$BIN" "$EVIDENCE"
timeout 120s dbus-run-session -- xvfb-run -a -s '-screen 0 1440x1000x24' sh -c '
  openbox >"$KOTO_APP_SANDBOX/../openbox.log" 2>&1 &
  "$1" &
  app_pid=$!
  (
    frame=0
    while kill -0 "$app_pid" 2>/dev/null; do
      frame=$((frame + 1))
      import -window root "$KOTO_APP_SANDBOX/../native-window-$frame.png" >/dev/null 2>&1 || true
      sleep 0.25
    done
  ) &
  capture_pid=$!
  status=0
  wait "$app_pid" || status=$?
  kill "$capture_pid" 2>/dev/null || true
  wait "$capture_pid" 2>/dev/null || true
  exit "$status"
' sh "$BIN" > "$RUN/native.log" 2>&1 || {
  cat "$RUN/native.log"
  [ ! -f "$RUN/result.json" ] || cat "$RUN/result.json"
  exit 1
}
# Keep every original frame; select the largest PNG as the representative view
# so a fast startup's empty WebView does not replace the loaded application.
python3 - "$RUN" <<'PY'
from pathlib import Path
import shutil, sys
root = Path(sys.argv[1])
frames = list(root.glob('native-window-*.png'))
if frames:
    shutil.copyfile(max(frames, key=lambda path: path.stat().st_size), root / 'native-window.png')
PY
cat "$RUN/result.json"
python3 -c 'import json,sys; result=json.load(open(sys.argv[1])); assert result.get("passed") is True, result' "$RUN/result.json"
printf 'PASS: %s\n' "$EVIDENCE"

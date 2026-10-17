#!/bin/sh
# Run all Go packages on the selected Linux architecture, retaining JSON evidence.
set -eu
APP_DIR=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$APP_DIR"
[ "$(uname -s)" = Linux ] || { echo 'Linux required.' >&2; exit 1; }
GO=${KOTO_GO:-"$APP_DIR/.tools/go-linux-arm64/bin/go"}
[ -x "$GO" ] || GO=go
ARCH=${1:-$("$GO" env GOARCH)}
export GOTOOLCHAIN=local GOOS=linux GOARCH="$ARCH" CGO_ENABLED=1
export GOCACHE=${GOCACHE:-"$APP_DIR/.cache/linux-gobuild"}
export GOMODCACHE=${GOMODCACHE:-"$APP_DIR/.cache/gomod"}
if [ "$ARCH" = amd64 ] && [ "$(uname -m)" = aarch64 ]; then
  export CC=x86_64-linux-gnu-gcc
  export PKG_CONFIG_LIBDIR=/usr/lib/x86_64-linux-gnu/pkgconfig:/usr/share/pkgconfig
fi
RUN=$(mktemp -d "$APP_DIR/.cache/linux-core-$ARCH.XXXXXX")
# Exercise Linux filesystem semantics. A macOS virtiofs share updates mmap
# metadata differently and is not representative of a native Agent directory.
TMPDIR=$(mktemp -d "/tmp/koto-core-$ARCH.XXXXXX")
export TMPDIR
trap 'rm -rf "$TMPDIR"' EXIT HUP INT TERM
if ! "$GO" test -tags gtk3 -count=1 -json ./... > "$RUN/tests.jsonl" 2> "$RUN/build.log"; then
  cat "$RUN/build.log"
  tail -100 "$RUN/tests.jsonl"
  exit 1
fi
python3 - "$RUN/tests.jsonl" <<'PY'
import json,sys
events=[json.loads(line) for line in open(sys.argv[1])]
for action in ['pass','skip','fail']:
    tests=[event for event in events if event.get('Action')==action and event.get('Test')]
    print(action, len(tests))
    if action!='pass':
        for event in tests: print(' ',event['Package'],event['Test'])
PY
printf 'PASS: %s\n' "$RUN"

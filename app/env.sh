# Source this to use the project-local Go and wails3 (nothing is installed system-wide):
#   source app/env.sh
APP_DIR="$(cd "$(dirname "${BASH_SOURCE[0]:-${(%):-%x}}")" && pwd)"
export GOROOT="$APP_DIR/.tools/go"
export GOPATH="$APP_DIR/.cache/gopath"
export GOMODCACHE="$APP_DIR/.cache/gomod"
export GOCACHE="$APP_DIR/.cache/gobuild"
export GOBIN="$APP_DIR/.tools/bin"
export TMPDIR="$APP_DIR/.cache/tmp"
export GOTMPDIR="$TMPDIR/go"
mkdir -p "$GOTMPDIR"
export GOTOOLCHAIN=local
export GOFLAGS=-modcacherw
export PATH="$GOBIN:$GOROOT/bin:$APP_DIR/.tools/agents/node_modules/.bin:$APP_DIR/node_modules/.bin:$PATH"

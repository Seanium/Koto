#!/bin/sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
# Keep the CLI, authentication, logs and local runtime state in this checkout.
export XDG_CONFIG_HOME="$root/app/.cache/cloudflare/config"
export XDG_CACHE_HOME="$root/app/.cache/cloudflare/cache"
export WRANGLER_LOG_PATH="$root/app/.cache/cloudflare/logs"
export WRANGLER_SEND_METRICS=false
export TMPDIR="$root/app/.cache/tmp"
mkdir -p "$XDG_CONFIG_HOME" "$XDG_CACHE_HOME" "$WRANGLER_LOG_PATH" "$TMPDIR"
cd "$root/updates"
exec ./node_modules/.bin/wrangler "$@"

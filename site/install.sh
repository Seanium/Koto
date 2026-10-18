#!/bin/sh
# Installs the standalone CLI; no desktop app or Agent configuration is changed.
set -eu

fail() { printf 'koto: %s\n' "$*" >&2; exit 1; }
usage() { printf '%s\n' 'Usage: install.sh [--version VERSION] [--dir DIRECTORY]' 'Default: latest stable release, installed to ~/.local/bin/koto.'; }
tag=
install_dir=${KOTO_INSTALL_DIR:-"$HOME/.local/bin"}
while [ "$#" -gt 0 ]; do
  case "$1" in
    --version) [ "$#" -ge 2 ] || fail 'missing version'; tag=$2; shift 2 ;;
    --dir) [ "$#" -ge 2 ] || fail 'missing directory'; install_dir=$2; shift 2 ;;
    --help|-h) usage; exit 0 ;;
    *) fail "unknown option: $1" ;;
  esac
done
command -v curl >/dev/null 2>&1 || fail 'curl is required'
case $(uname -s) in
  Darwin) os=darwin ;;
  Linux) os=linux ;;
  *) fail 'use install.ps1 on Windows; only macOS and Linux are supported here' ;;
esac
case $(uname -m) in
  arm64|aarch64) arch=arm64 ;;
  x86_64|amd64) arch=amd64 ;;
  *) fail 'this CPU architecture is not supported' ;;
esac
# A terminal running under Rosetta can still install the native executable.
if [ "$os" = darwin ] && [ "$(sysctl -n hw.optional.arm64 2>/dev/null || :)" = 1 ]; then arch=arm64; fi
repo=https://github.com/Seanium/koto
fetch() { curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' --connect-timeout 15 --max-time 300 "$@"; }
if [ -z "$tag" ]; then
  tag=$(fetch 'https://updates.kotoai.site/api/latest?format=tag') || fail 'cannot confirm the latest version; try again later or specify --version'
fi
case "$tag" in v*) ;; *) tag=v$tag ;; esac
case "$tag" in *[!A-Za-z0-9.-]*) fail 'invalid version' ;; esac
printf '%s\n' "$tag" | LC_ALL=C grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z]+([.-][0-9A-Za-z]+)*)?$' || fail 'invalid version'
asset=koto-cli-$os-$arch
mkdir -p "$install_dir"
install_dir=$(cd "$install_dir" && pwd -P)
target=$install_dir/koto
[ ! -L "$target" ] || fail "$target is a symlink; update its installation with koto update instead"
[ ! -d "$target" ] || fail "$target is a directory"
stage=$(mktemp -d "$install_dir/.koto-install.XXXXXXXX")
trap 'rm -rf "$stage"' EXIT
trap 'exit 130' INT
trap 'exit 143' TERM HUP
fetch "$repo/releases/download/$tag/SHA256SUMS" --output "$stage/SHA256SUMS"
digest=$(awk -v name="$asset" '{ file=$2; sub(/^\*/, "", file); if (file==name) { count++; hash=$1 } } END { if(count==1) print hash; else exit 1 }' "$stage/SHA256SUMS") || fail 'release checksum is missing or duplicated'
printf '%s\n' "$digest" | LC_ALL=C grep -Eq '^[0-9a-fA-F]{64}$' || fail 'invalid release checksum'
fetch "$repo/releases/download/$tag/$asset" --output "$stage/koto"
if command -v sha256sum >/dev/null 2>&1; then
  actual=$(sha256sum "$stage/koto" | awk '{print $1}')
elif command -v shasum >/dev/null 2>&1; then
  actual=$(shasum -a 256 "$stage/koto" | awk '{print $1}')
else
  fail 'sha256sum or shasum is required'
fi
[ "$(printf '%s' "$actual" | tr A-F a-f)" = "$(printf '%s' "$digest" | tr A-F a-f)" ] || fail 'checksum mismatch; existing installation was not changed'
chmod 755 "$stage/koto"
reported=$("$stage/koto" version) || fail 'downloaded CLI cannot run on this machine'
[ "$reported" = "koto ${tag#v}" ] || fail 'downloaded CLI reports an unexpected version'
# The verified CLI shares its OS lock, atomic replacement and rollback code with
# `koto update`; shell installers cannot accidentally race an in-app update.
"$stage/koto" --koto-install "$target" "$digest" || fail 'installation failed; see recovery details above'
printf 'Installed koto CLI %s at %s\n' "${tag#v}" "$target"
case :$PATH: in
  *:"$install_dir":*) ;;
  *) printf 'Add this directory to your PATH: %s\n' "$install_dir" ;;
esac

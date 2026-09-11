#!/bin/sh
# Selects the right arch-specific cudly-mcp binary at launch. The MCPB
# manifest format's platform_overrides differentiate by OS only (darwin,
# linux, win32) -- there is no architecture-level template variable -- so
# each OS gets one of these tiny wrapper scripts to bridge the gap between
# a single bundled command and the amd64/arm64 binaries GoReleaser produces.
set -eu

dir=$(cd "$(dirname "$0")" && pwd)
arch=$(uname -m)

case "$arch" in
  arm64) bin="$dir/darwin-arm64/cudly-mcp" ;;
  x86_64) bin="$dir/darwin-amd64/cudly-mcp" ;;
  *)
    echo "cudly-mcp: unsupported macOS architecture: $arch" >&2
    exit 1
    ;;
esac

exec "$bin" "$@"

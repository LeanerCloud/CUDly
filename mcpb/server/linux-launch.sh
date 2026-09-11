#!/bin/sh
# Selects the right arch-specific cudly-mcp binary at launch. See
# darwin-launch.sh for why this exists: MCPB's platform_overrides
# differentiate by OS only, not architecture.
set -eu

dir=$(cd "$(dirname "$0")" && pwd)
arch=$(uname -m)

case "$arch" in
  aarch64|arm64) bin="$dir/linux-arm64/cudly-mcp" ;;
  x86_64) bin="$dir/linux-amd64/cudly-mcp" ;;
  *)
    echo "cudly-mcp: unsupported Linux architecture: $arch" >&2
    exit 1
    ;;
esac

exec "$bin" "$@"

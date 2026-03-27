#!/bin/sh
# Detects the host CPU architecture and outputs JSON for Terraform's data "external" source.
# Used to select the native Docker build platform on hosts that support multiple architectures
# (Lambda, Fargate, Cloud Run, Container Apps), avoiding cross-compilation overhead.
set -e

ARCH=$(uname -m)
case "$ARCH" in
  x86_64)
    printf '{"arch":"x86_64","platform":"linux/amd64"}'
    ;;
  arm64|aarch64)
    printf '{"arch":"arm64","platform":"linux/arm64"}'
    ;;
  *)
    printf '{"arch":"x86_64","platform":"linux/amd64"}'
    ;;
esac

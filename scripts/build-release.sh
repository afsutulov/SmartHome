#!/usr/bin/env bash
set -euo pipefail
cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.."
go_binary="${GO_BINARY:-go}"
for release_target in linux-amd64 linux-arm64 windows-amd64; do
  release_os="${release_target%-*}"
  release_arch="${release_target#*-}"
  release_suffix=""
  if [[ "$release_os" == "windows" ]]; then release_suffix=".exe"; fi
  mkdir -p "bin/$release_target"
  CGO_ENABLED=0 GOOS="$release_os" GOARCH="$release_arch" "$go_binary" build \
    -trimpath -buildvcs=false -ldflags='-s -w' -o "bin/$release_target/SmartHome$release_suffix" .
done
sha256sum bin/linux-amd64/SmartHome bin/linux-arm64/SmartHome bin/windows-amd64/SmartHome.exe > SHA256SUMS

#!/usr/bin/env bash
# Builds self-contained harness binaries into dist/. Copy one onto your PATH,
# then run `harness init` inside any project.
set -euo pipefail
cd "$(dirname "$0")/.."
rm -rf dist && mkdir -p dist
for target in darwin/arm64 darwin/amd64 linux/arm64 linux/amd64 windows/amd64 windows/arm64; do
  os=${target%/*} arch=${target#*/}
  ext=""; if [ "$os" = windows ]; then ext=.exe; fi
  CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath -ldflags="-s -w" -o "dist/harness-$os-$arch$ext" ./cmd/harness
  echo "built dist/harness-$os-$arch$ext"
done
(cd dist && shasum -a 256 harness-* > SHA256SUMS)

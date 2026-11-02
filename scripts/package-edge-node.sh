#!/usr/bin/env bash
# Builds the zqnt-edge-node release archives: one zqnt-edge-node_<version>_linux_<arch>.tar.gz per
# target with the static binary, licences, example config, systemd unit and install.sh, plus
# SHA256SUMS. Usage: scripts/package-edge-node.sh <version> [out-dir]
set -euo pipefail

VERSION=${1:?usage: $0 <version> [out-dir]}
OUT=${2:-dist}

cd "$(dirname "$0")/.."
rm -rf "$OUT"
mkdir -p "$OUT"
OUT=$(cd "$OUT" && pwd)

build() {
  local arch=$1 goarch=$2 goarm=${3:-}
  local name="zqnt-edge-node_${VERSION}_linux_${arch}"
  local dir="$OUT/$name"
  mkdir -p "$dir"
  CGO_ENABLED=0 GOOS=linux GOARCH=$goarch GOARM=$goarm \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" \
    -o "$dir/zqnt-edge-node" ./cmd/zqnt-edge-node
  cp LICENSE NOTICE THIRD_PARTY.md examples/edge-node/node.yaml \
    packaging/systemd/zqnt-edge-node.service packaging/install.sh "$dir/"
  tar -C "$OUT" --owner=0 --group=0 --numeric-owner -czf "$OUT/$name.tar.gz" "$name"
  rm -rf "$dir"
}

build amd64 amd64
build arm64 arm64
build armv7 arm 7

(cd "$OUT" && sha256sum -- *.tar.gz >SHA256SUMS)
ls -l "$OUT"

#!/bin/bash
# Reproducible ARM64 build for all DroidMCP MCP servers. Flags match
# .github/workflows/build.yml and the Makefile so local, CI and release
# binaries are byte-identical when produced from the same commit:
#
#   -trimpath           : remove $GOPATH / build host paths from the binary
#   -ldflags '-s -w'    : strip symbol table + DWARF
#   -ldflags '-buildid=': zero out the link-time random build ID
#   -ldflags '-X ...Version=<v>': stamp the build version (from the git tag)
#
# A SHA256SUMS file is generated alongside the binaries so operators can
# verify what they install.
set -euo pipefail

mkdir -p bin

# Same version derivation as the Makefile; override with VERSION=... if needed.
VERSION="${VERSION:-$(git describe --tags --always 2>/dev/null || echo dev)}"

SERVICES=("filesystem" "github" "scraper" "termux" "network" "clipboard" "media" "sqlite" "sensors")

for service in "${SERVICES[@]}"; do
    echo "Building $service for ARM64 (version $VERSION)..."
    CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
        go build \
            -trimpath \
            -ldflags="-s -w -buildid= -X github.com/kahz12/droidmcp/internal/buildinfo.Version=${VERSION}" \
            -o "bin/droidmcp-$service" \
            "./cmd/$service"
done

( cd bin && sha256sum droidmcp-* > SHA256SUMS )
echo "Wrote bin/SHA256SUMS"

echo "Done."

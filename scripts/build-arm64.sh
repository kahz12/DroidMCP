#!/bin/bash
set -e

mkdir -p bin

# List of services to build
SERVICES=("filesystem")

for service in "${SERVICES[@]}"; do
    echo "Building $service for ARM64..."
    CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o bin/droidmcp-$service ./cmd/$service
done

echo "Done."

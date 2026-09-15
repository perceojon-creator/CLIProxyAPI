#!/usr/bin/env bash
# build-production.sh - Production Release Builder for Linux / macOS
# Generates stripped, hardened, production-grade binaries with baked metadata.
set -euo pipefail

TARGET_OS="${GOOS:-$(go env GOOS)}"
TARGET_ARCH="${GOARCH:-$(go env GOARCH)}"
OUTPUT_PATH="${1:-}"

COMMIT=$(git rev-parse --short HEAD 2>/dev/null || echo "none")
VERSION=$(git describe --tags --always --dirty 2>/dev/null || echo "v7.0.0-phase5")
BUILD_DATE=$(date -u +"%Y-%m-%dT%H:%M:%SZ")

BINARY_NAME="cli-proxy-api"
if [ "$TARGET_OS" = "windows" ]; then
    BINARY_NAME="${BINARY_NAME}.exe"
fi

if [ -z "$OUTPUT_PATH" ]; then
    OUTPUT_PATH="bin/${BINARY_NAME}"
fi

mkdir -p "$(dirname "$OUTPUT_PATH")"

LDFLAGS="-s -w -X 'main.Version=${VERSION}' -X 'main.Commit=${COMMIT}' -X 'main.BuildDate=${BUILD_DATE}' \
-X 'github.com/router-for-me/CLIProxyAPI/v7/internal/buildinfo.Version=${VERSION}' \
-X 'github.com/router-for-me/CLIProxyAPI/v7/internal/buildinfo.Commit=${COMMIT}' \
-X 'github.com/router-for-me/CLIProxyAPI/v7/internal/buildinfo.BuildDate=${BUILD_DATE}'"

echo "=================================================="
echo "Building Production CLIProxyAPI Binary"
echo "  OS/Arch:    ${TARGET_OS}/${TARGET_ARCH}"
echo "  Version:    ${VERSION}"
echo "  Commit:     ${COMMIT}"
echo "  Build Date: ${BUILD_DATE}"
echo "  Output:     ${OUTPUT_PATH}"
echo "=================================================="

CGO_ENABLED=0 GOOS="${TARGET_OS}" GOARCH="${TARGET_ARCH}" go build -trimpath -ldflags="${LDFLAGS}" -o "${OUTPUT_PATH}" ./cmd/server

if [ -f "$OUTPUT_PATH" ]; then
    SIZE_BYTES=$(wc -c < "$OUTPUT_PATH")
    SIZE_MB=$(awk "BEGIN {printf \"%.2f\", ${SIZE_BYTES}/1048576}")
    echo "Build Succeeded!"
    echo "  Binary Size: ${SIZE_MB} MB (${SIZE_BYTES} bytes)"
    if command -v sha256sum >/dev/null 2>&1; then
        echo "  SHA-256:     $(sha256sum "$OUTPUT_PATH" | awk '{print $1}')"
    fi
else
    echo "Build failed: Output binary not found at ${OUTPUT_PATH}" >&2
    exit 1
fi

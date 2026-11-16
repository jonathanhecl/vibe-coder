#!/usr/bin/env bash
# Compiles and packages vibe for all supported operating systems and architectures.
# Usage:
#   ./build-all.sh
#   ./build-all.sh v1.0.0
set -euo pipefail

VERSION="${1:-dev}"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DIST="$ROOT/dist"
MODULE="github.com/jonathanhecl/vibe-coder"

for cmd in go zip; do
    command -v "$cmd" >/dev/null 2>&1 || { echo "Error: $cmd is not installed or is not in PATH." >&2; exit 1; }
done

rm -rf "$DIST"
mkdir -p "$DIST"

LDFLAGS="-s -w -X ${MODULE}/internal/version.Value=${VERSION}"

# os arch label
TARGETS="windows amd64 windows_amd64
linux amd64 linux_amd64
linux arm64 linux_arm64
darwin amd64 darwin_amd64
darwin arm64 darwin_arm64"

echo "Starting multiplatform build ($VERSION)..."
cd "$ROOT"
while read -r target_os target_arch label; do
    binary="vibe"
    [ "$target_os" = "windows" ] && binary="vibe.exe"
    pack="$DIST/vibe_${VERSION}_${label}.zip"
    tmp="$DIST/build_${target_os}_${target_arch}"
    rm -rf "$tmp"
    mkdir -p "$tmp"

    echo "Building for $target_os ($target_arch)..."
    if ! CGO_ENABLED=0 GOOS="$target_os" GOARCH="$target_arch" \
        go build -trimpath -ldflags "$LDFLAGS" -o "$tmp/$binary" ./cmd/vibe; then
        echo "Error: build failed for $target_os/$target_arch" >&2
        rm -rf "$tmp"
        exit 1
    fi

    (cd "$tmp" && zip -q -X "$pack" "$binary")
    echo "Packaged $(basename "$pack") ($binary)"
    rm -rf "$tmp"
done <<EOF
$TARGETS
EOF

write_checksums() {
    local out="$DIST/checksums.txt"
    ( cd "$DIST" && \
        if command -v sha256sum >/dev/null 2>&1; then
            sha256sum ./*.zip
        elif command -v shasum >/dev/null 2>&1; then
            shasum -a 256 ./*.zip
        elif command -v openssl >/dev/null 2>&1; then
            for f in ./*.zip; do
                printf '%s  %s\n' "$(openssl dgst -sha256 -r "$f" | awk '{print $1}')" "$f"
            done
        else
            echo "Error: no SHA-256 tool found (install sha256sum or shasum)." >&2
            exit 1
        fi ) | sed 's#\./##' >"$out"
    echo "Wrote dist/checksums.txt"
}

write_checksums

echo ""
echo "Build and packaging completed successfully. Files in dist/:"
ls -l "$DIST"

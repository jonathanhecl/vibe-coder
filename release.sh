#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

base_version="dev"
short_sha="nogit"
dirty_suffix=""
build_timestamp="$(date -u +%Y%m%d.%H%M)"

if command -v git >/dev/null 2>&1 && git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  if tag="$(git describe --tags --abbrev=0 --match 'v[0-9]*' 2>/dev/null)"; then
    base_version="$tag"
  fi

  if sha="$(git rev-parse --short HEAD 2>/dev/null)"; then
    short_sha="$sha"
  fi

  if ! git diff --quiet --ignore-submodules HEAD -- 2>/dev/null; then
    dirty_suffix=".dirty"
  fi
fi

version="${base_version}+${build_timestamp}.sha${short_sha}${dirty_suffix}"
ldflags="-s -w -X github.com/jonathanhecl/vibe-coder/internal/version.Value=${version}"

OUT="dist"
mkdir -p "$OUT"

targets=(
  "linux amd64 tar.gz"
  "linux arm64 tar.gz"
  "darwin amd64 tar.gz"
  "darwin arm64 tar.gz"
  "windows amd64 zip"
)

for t in "${targets[@]}"; do
  read -r GOOS GOARCH FORMAT <<<"$t"
  echo "[INFO] Building ${GOOS}/${GOARCH} ..."

  workdir="${OUT}/${GOOS}_${GOARCH}"
  mkdir -p "$workdir"

  bin="vibe-coder"
  if [[ "$GOOS" == "windows" ]]; then
    bin="vibe-coder.exe"
  fi

  GOOS="$GOOS" GOARCH="$GOARCH" CGO_ENABLED=0 \
    go build -trimpath -ldflags "$ldflags" -o "${workdir}/${bin}" ./cmd/vibe-coder

  archive_base="vibe-coder_${version}_${GOOS}_${GOARCH}"
  if [[ "$FORMAT" == "tar.gz" ]]; then
    tar -czf "${OUT}/${archive_base}.tar.gz" -C "$workdir" "$bin"
  else
    (cd "$workdir" && zip -q "${OUT}/${archive_base}.zip" "$bin")
  fi

  rm -rf "$workdir"
done

echo "[INFO] Generating checksums.txt ..."
(cd "$OUT" && sha256sum vibe-coder_* > checksums.txt)

echo "[OK] Release artifacts in ${OUT}/"
ls -la "$OUT"

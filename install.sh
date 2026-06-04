#!/usr/bin/env bash
set -euo pipefail

REPO="jonathanhecl/vibe-coder"
BIN="vibe-coder"

# Detect OS and architecture
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"

case "$OS" in
  linux)   OS="linux" ;;
  darwin)  OS="darwin" ;;
  *)       echo "[ERROR] Unsupported OS: $OS"; exit 1 ;;
esac

case "$ARCH" in
  x86_64|amd64)  ARCH="amd64" ;;
  arm64|aarch64) ARCH="arm64" ;;
  *)             echo "[ERROR] Unsupported architecture: $ARCH"; exit 1 ;;
esac

echo "[INFO] Detected ${OS}/${ARCH}"

# Fetch latest release version from GitHub API
echo "[INFO] Looking up latest release ..."
LATEST="$(curl -sL --fail "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')"
if [ -z "$LATEST" ]; then
  echo "[ERROR] Could not determine latest release. Are you offline or rate-limited?"
  exit 1
fi
echo "[INFO] Latest release: ${LATEST}"

# Build download URL
ASSET="${BIN}_${LATEST}_${OS}_${ARCH}.tar.gz"
URL="https://github.com/${REPO}/releases/download/${LATEST}/${ASSET}"

# Determine install directory
if [ -d /usr/local/bin ] && [ -w /usr/local/bin ]; then
  INSTALL_DIR="/usr/local/bin"
elif [ -d /usr/bin ] && [ -w /usr/bin ]; then
  INSTALL_DIR="/usr/bin"
else
  INSTALL_DIR="${HOME}/.local/bin"
  mkdir -p "$INSTALL_DIR"
fi

# Download to temp
echo "[INFO] Downloading ${ASSET} ..."
TMPDIR="$(mktemp -d)"
trap 'rm -rf "$TMPDIR"' EXIT
curl -sL --fail "$URL" -o "${TMPDIR}/${ASSET}"

# Extract
echo "[INFO] Extracting ..."
tar -xzf "${TMPDIR}/${ASSET}" -C "$TMPDIR"

# Install
echo "[INFO] Installing to ${INSTALL_DIR} ..."
if [ -w "$INSTALL_DIR" ]; then
  mv "${TMPDIR}/${BIN}" "${INSTALL_DIR}/${BIN}"
  chmod +x "${INSTALL_DIR}/${BIN}"
else
  mv "${TMPDIR}/${BIN}" "${INSTALL_DIR}/${BIN}" || {
    echo "[WARN] Need sudo to write to ${INSTALL_DIR}"
    sudo mv "${TMPDIR}/${BIN}" "${INSTALL_DIR}/${BIN}"
    sudo chmod +x "${INSTALL_DIR}/${BIN}"
  }
fi

echo "[OK] Installed ${BIN} ${LATEST} to ${INSTALL_DIR}/${BIN}"
echo "[INFO] Make sure ${INSTALL_DIR} is in your PATH"

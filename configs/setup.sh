#!/bin/sh
# RefConnect installer for macOS and Linux
# Usage: curl -fsSL https://raw.githubusercontent.com/S7R4nG3/refconnect/main/configs/setup.sh | sh

set -e

REPO="S7R4nG3/refconnect"
API="https://api.github.com/repos/${REPO}/releases/latest"

# Detect OS
OS="$(uname -s)"
case "${OS}" in
    Linux)  os="linux" ;;
    Darwin) os="darwin" ;;
    *)
        echo "Unsupported OS: ${OS}" >&2
        exit 1
        ;;
esac

# Detect architecture
ARCH="$(uname -m)"
case "${ARCH}" in
    x86_64)         arch="amd64" ;;
    aarch64|arm64)  arch="arm64" ;;
    *)
        echo "Unsupported architecture: ${ARCH}" >&2
        exit 1
        ;;
esac

# Resolve download command
if command -v curl >/dev/null 2>&1; then
    download() { curl -fsSL "$1" -o "$2"; }
    fetch()    { curl -fsSL "$1"; }
elif command -v wget >/dev/null 2>&1; then
    download() { wget -qO "$2" "$1"; }
    fetch()    { wget -qO- "$1"; }
else
    echo "curl or wget is required." >&2
    exit 1
fi

# Get latest release tag and find the matching asset URL
echo "Fetching latest release info..."
RELEASE_JSON="$(fetch "${API}")"

TAG="$(echo "${RELEASE_JSON}" | grep '"tag_name"' | head -1 | sed 's/.*"tag_name": *"\([^"]*\)".*/\1/')"
if [ -z "${TAG}" ]; then
    echo "Could not determine latest release tag." >&2
    exit 1
fi
echo "Latest release: ${TAG}"

# Asset naming convention: refconnect_<os>_<arch>.zip (darwin) or .tar.gz (linux)
if [ "${os}" = "darwin" ]; then
    ASSET="refconnect_${os}_${arch}.zip"
else
    ASSET="refconnect_${os}_${arch}.tar.gz"
fi

ASSET_URL="$(echo "${RELEASE_JSON}" | grep "\"browser_download_url\"" | grep "${ASSET}" | head -1 | sed 's/.*"browser_download_url": *"\([^"]*\)".*/\1/')"
if [ -z "${ASSET_URL}" ]; then
    echo "No asset found for ${ASSET} in release ${TAG}." >&2
    exit 1
fi

# Download
echo "Downloading ${ASSET}..."
download "${ASSET_URL}" "${ASSET}"

# Extract to current directory
echo "Extracting..."
if [ "${os}" = "darwin" ]; then
    unzip -oq "${ASSET}"
else
    tar -xzf "${ASSET}"
fi

rm -f "${ASSET}"

# Linux: install the icon and a desktop entry so RefConnect shows up in the
# application menu with its icon.
if [ "${os}" = "linux" ] && [ -f refconnect.desktop ] && [ -f refconnect.png ]; then
    DATA_HOME="${XDG_DATA_HOME:-${HOME}/.local/share}"
    ICON_DIR="${DATA_HOME}/icons/hicolor/32x32/apps"
    APP_DIR="${DATA_HOME}/applications"
    mkdir -p "${ICON_DIR}" "${APP_DIR}"

    mv -f refconnect.png "${ICON_DIR}/refconnect.png"
    sed "s|^Exec=.*|Exec=\"$(pwd)/refconnect\"|" refconnect.desktop > "${APP_DIR}/refconnect.desktop"
    rm -f refconnect.desktop

    command -v update-desktop-database >/dev/null 2>&1 && update-desktop-database "${APP_DIR}" >/dev/null 2>&1 || true
    command -v gtk-update-icon-cache >/dev/null 2>&1 && gtk-update-icon-cache -q -t "${DATA_HOME}/icons/hicolor" >/dev/null 2>&1 || true
    echo "Installed desktop entry to ${APP_DIR}/refconnect.desktop"
fi

echo "Done. RefConnect ${TAG} installed in the current directory."

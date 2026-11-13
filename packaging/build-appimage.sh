#!/bin/sh
# Build a RefConnect AppImage (x86_64 or aarch64, same arch as the host).
# Requires: go, appimagetool, Xorg/Wayland dev libs (see Makefile linux-packages).
# Usage: [VERSION=x.y.z] ./packaging/build-appimage.sh [output-dir]
# VERSION defaults to version.json (CI passes the release tag so every
# Linux artifact carries the same version).
# Output: refconnect_<version>_<arch>.AppImage
set -e
cd "$(dirname "$0")/.."

OUTDIR="${1:-.}"
VERSION="${VERSION:-$(python3 -c 'import json;print(json.load(open("version.json"))["version"])')}"
VERSION="${VERSION#v}"
ARCH="$(uname -m)"
case "${ARCH}" in
  x86_64)  APPARCH="x86_64" ;;
  aarch64) APPARCH="aarch64" ;;
  *) echo "Unsupported arch: ${ARCH}" >&2; exit 1 ;;
esac

echo "Building refconnect ${VERSION} (${APPARCH})..."
go build -ldflags="-s -w" -o refconnect .

APPDIR="$(mktemp -d)/RefConnect.AppDir"
mkdir -p "${APPDIR}/usr/bin" "${APPDIR}/usr/share/applications" \
         "${APPDIR}/usr/share/icons/hicolor/32x32/apps"
cp refconnect "${APPDIR}/usr/bin/refconnect"
cp configs/refconnect.desktop "${APPDIR}/usr/share/applications/refconnect.desktop"
cp docs/antenna.png "${APPDIR}/usr/share/icons/hicolor/32x32/apps/refconnect.png"
cp docs/antenna.png "${APPDIR}/refconnect.png"
cp configs/refconnect.desktop "${APPDIR}/refconnect.desktop"
cat > "${APPDIR}/AppRun" <<'EOF'
#!/bin/sh
HERE="$(dirname "$(readlink -f "$0")")"
exec "${HERE}/usr/bin/refconnect" "$@"
EOF
chmod +x "${APPDIR}/AppRun" "${APPDIR}/usr/bin/refconnect"

# APPIMAGETOOL env overrides the binary (CI uses the extracted AppRun, no FUSE).
ARCH="${APPARCH}" ${APPIMAGETOOL:-appimagetool} "${APPDIR}" "${OUTDIR}/refconnect_${VERSION}_${APPARCH}.AppImage"
echo "Wrote ${OUTDIR}/refconnect_${VERSION}_${APPARCH}.AppImage"

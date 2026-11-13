#!/bin/sh
# Build the signed APT and DNF repositories served from GitHub Pages.
# Requires: apt-utils (apt-ftparchive), createrepo-c, gpg, with the signing
# key already imported into the gpg keyring.
# Usage: SIGNING_KEY_ID=<fingerprint> [SIGNING_KEY_PASSPHRASE=...] \
#          ./packaging/build-repos.sh <package-dir> <output-dir>
# <package-dir> holds the release .deb/.rpm files (rpms already signed by nFPM).
# Output layout:
#   <out>/index.html, refconnect.asc (armored key), refconnect.gpg (keyring)
#   <out>/apt/dists/stable/{Release,Release.gpg,InRelease,main/binary-*/Packages*}
#   <out>/apt/pool/main/r/refconnect/*.deb
#   <out>/rpm/{*.rpm,repodata/,refconnect.repo}
set -eu

PKGS="${1:?usage: build-repos.sh <package-dir> <output-dir>}"
OUT="${2:?usage: build-repos.sh <package-dir> <output-dir>}"
: "${SIGNING_KEY_ID:?SIGNING_KEY_ID must be set}"
SITE_URL="${SITE_URL:-https://s7r4ng3.github.io/refconnect}"
HERE="$(cd "$(dirname "$0")" && pwd)"

gpg_sign() {
  printf '%s' "${SIGNING_KEY_PASSPHRASE:-}" | gpg --batch --yes --pinentry-mode loopback \
    --passphrase-fd 0 --local-user "${SIGNING_KEY_ID}" "$@"
}

rm -rf "${OUT}"
mkdir -p "${OUT}/apt/pool/main/r/refconnect" "${OUT}/rpm"
cp "${HERE}/site/index.html" "${OUT}/index.html"
gpg --batch --armor --export "${SIGNING_KEY_ID}" > "${OUT}/refconnect.asc"
gpg --batch --export "${SIGNING_KEY_ID}" > "${OUT}/refconnect.gpg"

# --- APT ---------------------------------------------------------------------
cp "${PKGS}"/*.deb "${OUT}/apt/pool/main/r/refconnect/"
cd "${OUT}/apt"
for arch in amd64 arm64; do
  dir="dists/stable/main/binary-${arch}"
  mkdir -p "${dir}"
  apt-ftparchive --arch "${arch}" packages pool > "${dir}/Packages"
  gzip -9nk "${dir}/Packages"
done
apt-ftparchive \
  -o APT::FTPArchive::Release::Origin=RefConnect \
  -o APT::FTPArchive::Release::Label=RefConnect \
  -o APT::FTPArchive::Release::Suite=stable \
  -o APT::FTPArchive::Release::Codename=stable \
  -o "APT::FTPArchive::Release::Architectures=amd64 arm64" \
  -o APT::FTPArchive::Release::Components=main \
  release dists/stable > ../Release.tmp
mv ../Release.tmp dists/stable/Release
gpg_sign --clearsign -o dists/stable/InRelease dists/stable/Release
gpg_sign --armor --detach-sign -o dists/stable/Release.gpg dists/stable/Release
cd - >/dev/null

# --- DNF ---------------------------------------------------------------------
cp "${PKGS}"/*.rpm "${OUT}/rpm/"
createrepo_c --quiet "${OUT}/rpm"
gpg_sign --armor --detach-sign -o "${OUT}/rpm/repodata/repomd.xml.asc" "${OUT}/rpm/repodata/repomd.xml"
cat > "${OUT}/rpm/refconnect.repo" <<EOF
[refconnect]
name=RefConnect
baseurl=${SITE_URL}/rpm
enabled=1
gpgcheck=1
repo_gpgcheck=1
gpgkey=${SITE_URL}/refconnect.asc
EOF

echo "Wrote APT and DNF repositories to ${OUT}"

#!/bin/sh
# Build a test APT/DNF repo from ./refconnect, signed with a throwaway key,
# for packaging/test-install.sh. Mirrors the release pipeline (nFPM signs the
# rpm, build-repos.sh signs the repo metadata) without touching the real key.
# Requires: nfpm, gpg, objdump, apt-ftparchive, createrepo_c.
# Usage: ARCH=amd64 ./packaging/build-test-repo.sh <out-dir>
# Output:
#   <out>/site/  repo for SITE_URL (default http://localhost:8080), serving VERSION
#   <out>/old/   OLD_VERSION .deb/.rpm, to install before testing the upgrade
set -eu

OUT="${1:?usage: build-test-repo.sh <out-dir>}"
: "${ARCH:?ARCH must be set (amd64 or arm64)}"
VERSION="${VERSION:-$(sed -n 's/.*"version": *"v\{0,1\}\([^"]*\)".*/\1/p' version.json)}"
OLD_VERSION="${OLD_VERSION:-0.0.1}"
NFPM="${NFPM:-nfpm}"
HERE="$(cd "$(dirname "$0")" && pwd)"
export SITE_URL="${SITE_URL:-http://localhost:8080}"

rm -rf "${OUT}"
mkdir -p "${OUT}/old" "${OUT}/new"
OUT="$(cd "${OUT}" && pwd)"

# gpg-agent's socket lives in GNUPGHOME; keep the path short.
GNUPGHOME="$(mktemp -d /tmp/gpg.XXXXXX)"
export GNUPGHOME
trap 'gpgconf --kill gpg-agent 2>/dev/null; rm -rf "${GNUPGHOME}"' EXIT
export SIGNING_KEY_PASSPHRASE="test-only"
gpg --batch --quiet --pinentry-mode loopback --passphrase "${SIGNING_KEY_PASSPHRASE}" \
  --quick-gen-key "RefConnect Test <test@invalid>" rsa3072 sign 1d
SIGNING_KEY_ID="$(gpg --list-secret-keys --with-colons | awk -F: '/^fpr/ {print $10; exit}')"
export SIGNING_KEY_ID
gpg --batch --pinentry-mode loopback --passphrase "${SIGNING_KEY_PASSPHRASE}" \
  --armor --export-secret-keys "${SIGNING_KEY_ID}" > "${GNUPGHOME}/key.asc"
export RPM_SIGNING_KEY_FILE="${GNUPGHOME}/key.asc"
export NFPM_RPM_PASSPHRASE="${SIGNING_KEY_PASSPHRASE}"

GLIBC_MIN="$(sh "${HERE}/glibc-min.sh" refconnect)"
export GLIBC_MIN ARCH
for v in "${OLD_VERSION}:old" "${VERSION}:new"; do
  for p in deb rpm; do
    VERSION="${v%%:*}" "${NFPM}" package --packager "${p}" \
      --config "${HERE}/nfpm.yaml" --target "${OUT}/${v##*:}/"
  done
done

sh "${HERE}/build-repos.sh" "${OUT}/new" "${OUT}/site"
echo "Test repo for ${VERSION} (old: ${OLD_VERSION}, glibc >= ${GLIBC_MIN}) in ${OUT}"

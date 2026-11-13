#!/bin/sh
# Print the minimum glibc version an ELF binary needs: the highest GLIBC_x.y
# symbol version it references. Used as the libc6/glibc floor in nfpm.yaml so
# apt/dnf refuse to install on distros too old to run the binary.
# Usage: ./packaging/glibc-min.sh <binary>
set -e
MIN="$(objdump -T "${1:?usage: glibc-min.sh <binary>}" | grep -o 'GLIBC_[0-9][0-9.]*' | sed 's/^GLIBC_//' | sort -uV | tail -n1)"
if [ -z "${MIN}" ]; then
  echo "glibc-min.sh: no GLIBC symbol versions found in $1" >&2
  exit 1
fi
echo "${MIN}"

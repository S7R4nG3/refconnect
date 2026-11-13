#!/bin/sh
# Install-path smoke test, run as root inside a fresh distro container.
# Follows the README install steps as an unprivileged sudo user, then checks
# that the installed refconnect has all its libraries and starts under Xvfb.
# Usage: test-install.sh <site-url> <expected-version> [<old-package>]
#   <site-url>          repo root, e.g. https://s7r4ng3.github.io/refconnect
#   <expected-version>  version the repo serves, e.g. 0.15.0
#   <old-package>       optional .deb/.rpm installed first; the repo version
#                       must then arrive via `apt upgrade` / `dnf upgrade`
# EXPECT=refuse inverts the check: the package manager must refuse to install
# (used on distros below the glibc floor).
set -eu

SITE="${1:?usage: test-install.sh <site-url> <expected-version> [<old-package>]}"
WANT="${2:?usage: test-install.sh <site-url> <expected-version> [<old-package>]}"
OLD="${3:-}"
EXPECT="${EXPECT:-install}"

step() { printf '\n==> %s\n' "$*"; }
fail() { printf '\nFAIL: %s\n' "$*" >&2; exit 1; }

if command -v apt-get >/dev/null 2>&1; then
  FAMILY=deb
elif command -v dnf >/dev/null 2>&1; then
  FAMILY=rpm
else
  fail "no apt-get or dnf in this image"
fi
. /etc/os-release
step "${PRETTY_NAME} ($(uname -m)), ${FAMILY}, expect=${EXPECT}"

# Not in the README: minimal container images lack what a desktop install has.
step "Preparing base system (curl, sudo, Xvfb, test user)"
if [ "${FAMILY}" = deb ]; then
  export DEBIAN_FRONTEND=noninteractive
  apt-get update -qq
  apt-get install -y -qq curl ca-certificates sudo xvfb procps >/dev/null
else
  dnf install -y -q curl sudo xorg-x11-server-Xvfb procps-ng >/dev/null
fi
useradd -m tester
echo 'tester ALL=(ALL) NOPASSWD:ALL' > /etc/sudoers.d/tester
as_user() { su tester -c "$1"; }

installed_version() {
  if [ "${FAMILY}" = deb ]; then
    dpkg-query -W -f='${Version}' refconnect 2>/dev/null || true
  else
    rpm -q --qf '%{VERSION}' refconnect 2>/dev/null || true
  fi
}

if [ -n "${OLD}" ]; then
  step "Installing previous release $(basename "${OLD}")"
  cp "${OLD}" /tmp/
  OLD="/tmp/$(basename "${OLD}")"
  if [ "${FAMILY}" = deb ]; then
    as_user "sudo DEBIAN_FRONTEND=noninteractive apt-get install -y -qq '${OLD}'"
  else
    # Release rpms are signed by the repo key, which is not imported yet.
    as_user "sudo dnf install -y -q --nogpgcheck '${OLD}'"
  fi
  echo "installed: $(installed_version)"
fi

step "Adding the repository (README steps)"
if [ "${FAMILY}" = deb ]; then
  as_user "sudo curl -fsSLo /usr/share/keyrings/refconnect.gpg ${SITE}/refconnect.gpg"
  as_user "echo 'deb [signed-by=/usr/share/keyrings/refconnect.gpg] ${SITE}/apt stable main' | sudo tee /etc/apt/sources.list.d/refconnect.list"
else
  as_user "sudo curl -fsSLo /etc/yum.repos.d/refconnect.repo ${SITE}/rpm/refconnect.repo"
fi

if [ -n "${OLD}" ]; then
  step "Upgrading (README: apt upgrade / dnf upgrade)"
  if [ "${FAMILY}" = deb ]; then
    as_user "sudo apt-get update && sudo DEBIAN_FRONTEND=noninteractive apt-get upgrade -y"
  else
    as_user "sudo dnf upgrade -y"
  fi
else
  step "Installing refconnect"
  if [ "${FAMILY}" = deb ]; then
    CMD="sudo apt-get update && sudo DEBIAN_FRONTEND=noninteractive apt-get install -y refconnect"
  else
    CMD="sudo dnf install -y refconnect"
  fi
  if [ "${EXPECT}" = refuse ]; then
    if as_user "${CMD}"; then
      fail "install succeeded on ${PRETTY_NAME}, expected the glibc floor to refuse it"
    fi
    [ -z "$(installed_version)" ] || fail "refconnect is installed despite the refused install"
    step "PASS: install refused as expected"
    exit 0
  fi
  # dnf asks to import the repo key; -y accepts it, like answering "y".
  as_user "${CMD}"
fi

step "Checking the installed package"
GOT="$(installed_version)"
[ "${GOT%%-*}" = "${WANT}" ] || fail "installed version '${GOT}', expected '${WANT}'"
echo "version: ${GOT}"
for f in /usr/bin/refconnect /usr/share/applications/refconnect.desktop \
         /usr/share/icons/hicolor/32x32/apps/refconnect.png; do
  [ -e "${f}" ] || fail "missing ${f}"
done

step "Checking shared libraries"
MISSING="$(ldd /usr/bin/refconnect | grep 'not found' || true)"
[ -z "${MISSING}" ] || fail "unresolved libraries (missing package dependency?):
${MISSING}"

step "Starting refconnect under Xvfb"
# setsid: background jobs started inside `su -c` are killed when it returns.
# -displayfd picks a free display: X's abstract sockets are per network
# namespace, so a fixed :99 collides with other X servers on a shared one.
setsid Xvfb -displayfd 3 -screen 0 1280x800x24 3>/tmp/xvfb.display >/tmp/xvfb.log 2>&1 < /dev/null &
for _ in $(seq 30); do [ -s /tmp/xvfb.display ] && break; sleep 1; done
DISP="$(cat /tmp/xvfb.display)"
[ -n "${DISP}" ] || { cat /tmp/xvfb.log >&2; fail "Xvfb did not start"; }
install -d -o tester -m 0700 /tmp/xdg-tester
as_user "DISPLAY=:${DISP} XDG_RUNTIME_DIR=/tmp/xdg-tester LIBGL_ALWAYS_SOFTWARE=1 \\
         setsid refconnect >/tmp/refconnect.out 2>&1 < /dev/null &
         echo \$! > /tmp/refconnect.pid"
sleep 8
PID="$(cat /tmp/refconnect.pid)"
if ! kill -0 "${PID}" 2>/dev/null; then
  cat /tmp/refconnect.out /tmp/xvfb.log >&2 || true
  fail "refconnect exited within 8s of launch"
fi
LOG="$(ls -t /home/tester/.config/refconnect/Logs/*.log 2>/dev/null | head -n1 || true)"
[ -n "${LOG}" ] && grep -q 'RefConnect' "${LOG}" || {
  cat /tmp/refconnect.out >&2 || true
  fail "no startup line in refconnect's log"
}
echo "running (pid ${PID}); log: $(head -n1 "${LOG}")"
kill "${PID}"

step "PASS: refconnect ${GOT} on ${PRETTY_NAME} ($(uname -m))"

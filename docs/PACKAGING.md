# Linux packaging

## Local builds

```sh
# needs Go + Xorg/Wayland dev libs (see Makefile linux-packages)
make build
make deb        # -> refconnect_<version>-1_<arch>.deb
make rpm        # -> refconnect-<version>-1.<rpm-arch>.rpm (x86_64/aarch64)
make appimage   # -> refconnect_<version>_<arch>.AppImage (needs appimagetool)
```

`VERSION` defaults to `version.json` without the leading `v`;
`GOARCH` defaults to `go env GOARCH`. Override: `VERSION=0.14.0 GOARCH=arm64 make deb`.
`make deb` / `make rpm` also need `objdump` (binutils): `packaging/glibc-min.sh`
reads the highest `GLIBC_x.y` symbol the binary uses and the packages declare
`libc6 (>= x.y)` / `glibc >= x.y`, so apt/dnf refuse to install on a distro
whose glibc is too old to run the binary.

Install / verify:

```sh
sudo apt install ./refconnect*.deb    # Debian/Ubuntu (resolves deps)
sudo dnf install ./refconnect*.rpm    # Fedora/RHEL (rpm deps are Fedora/RHEL names; openSUSE not covered)
chmod +x refconnect*.AppImage && ./refconnect*.AppImage
dpkg -c refconnect*.deb
rpm -qlp refconnect*.rpm
```

Files installed: `/usr/bin/refconnect`, `/usr/share/applications/refconnect.desktop`,
`/usr/share/icons/hicolor/32x32/apps/refconnect.png` (`docs/antenna.png` is 32×32).

## Releases

`.github/workflows/release.yml` builds on tag `v*`:

* `build-linux (amd64)`: `tar.gz` + `.deb` + `.rpm` + `.AppImage`
* `build-linux-arm64`: `tar.gz` + `.deb` + `.rpm` + `.AppImage`
* `publish-repos`: after the release is created, rebuilds the APT and DNF
  repositories from that release's `.deb`/`.rpm` files and deploys them to
  GitHub Pages (https://s7r4ng3.github.io/refconnect/).
* macOS / Windows unchanged.

All Linux artifacts take their version from the tag (`VERSION=${GITHUB_REF_NAME#v}`).
The binaries are built on Ubuntu 24.04 runners; as of v0.14.0 they need glibc
2.38 or newer (Debian 13+, Ubuntu 24.04+, Fedora 39+). The CI log prints the
computed floor for each build. The tarball and AppImage have the same floor;
the AppImage does not bundle glibc or the GL/X11 libraries.

## APT / DNF repositories (GitHub Pages)

`packaging/build-repos.sh <package-dir> <output-dir>` builds the site with
Ubuntu's own tools (`apt-ftparchive`, `createrepo_c`, `gpg`); no third-party
action sees the signing key.

```
index.html, refconnect.asc, refconnect.gpg     # install page, armored key, binary keyring
apt/dists/stable/{InRelease,Release,Release.gpg,main/binary-{amd64,arm64}/Packages*}
apt/pool/main/r/refconnect/*.deb
rpm/{*.rpm,repodata/repomd.xml(.asc),refconnect.repo}
```

Each deploy replaces the whole site, so the repos only carry the latest
release (enough for `apt upgrade` / `dnf upgrade`).

Signing: APT verifies the signed `InRelease`/`Release.gpg`; DNF verifies the
signed `repomd.xml` (`repo_gpgcheck=1`) and the rpm signatures (`gpgcheck=1`),
which nFPM adds at build time when `RPM_SIGNING_KEY_FILE` is set.

One-time setup:

1. Create a signing key (no expiry or a long one; users pin it):
   ```sh
   gpg --batch --quick-gen-key "RefConnect Packages <s7r4ng3@gmail.com>" rsa4096 sign never
   gpg --armor --export-secret-keys "RefConnect Packages" > pkg-signing.asc
   ```
   Add a passphrase with `--passphrase` if you want one.
2. Add repository secrets `PKG_SIGNING_KEY` (contents of `pkg-signing.asc`) and,
   if the key has one, `PKG_SIGNING_KEY_PASSPHRASE`. Keep an offline backup of
   the key; losing it means every user has to re-import a new one.
3. Settings → Pages → Build and deployment → Source: **GitHub Actions**.
4. Settings → Environments → `github-pages`: allow tags matching `v*` to deploy
   (by default only the default branch can).

### Install tests

`packaging/test-install.sh <site-url> <version> [<old-package>]` runs as root
in a fresh distro container. As an unprivileged sudo user it follows the
README steps (or installs `<old-package>` first and then runs `apt upgrade` /
`dnf upgrade`). It then checks the installed version and files, checks that
`ldd` resolves every library (i.e. the package dependencies are complete),
and checks that refconnect stays up under Xvfb.

* **Every PR** (`ci.yml`): `packaging/build-test-repo.sh` builds packages and a
  repo signed with a throwaway key. The repo is served on `localhost`, and the
  install is tested on Ubuntu 24.04, Debian 13 and Fedora (latest), amd64 and
  arm64, plus an upgrade from an older version on amd64. On Ubuntu 22.04 the
  test expects the install to be **refused**, because glibc is below the floor.
* **Every release** (`release.yml`, `verify-repos`): the same checks against
  the live Pages repo once it serves the new version. The upgrade runs start
  from the previous release's `.deb`/`.rpm`. They are skipped if that release
  has no packages.

Run locally (Docker), after building `./refconnect` on Ubuntu 24.04:

```sh
ARCH=arm64 sh packaging/build-test-repo.sh /tmp/t     # needs nfpm, apt-utils, createrepo-c
docker run -d --name reposrv -v /tmp/t/site:/site:ro python:3-alpine python -m http.server 8080 -d /site
docker run --rm --network container:reposrv -v "$PWD/packaging/test-install.sh:/t.sh:ro" \
  fedora:latest sh /t.sh http://localhost:8080 0.14.0
```

## AUR

Two AUR packages, maintained in separate AUR repos (not in this repo):

* `refconnect-git` (`packaging/aur/refconnect-git/PKGBUILD`) — builds from git HEAD.
* `refconnect-bin` (`packaging/aur/refconnect-bin/PKGBUILD`) — repackages the release `tar.gz`.

Publish / update:

```sh
# first time
git clone ssh://aur@aur.archlinux.org/refconnect-git.git
cp packaging/aur/refconnect-git/PKGBUILD refconnect-git/
cd refconnect-git && makepkg --printsrcinfo > .SRCINFO && git add PKGBUILD .SRCINFO && git commit -m "init" && git push

# new release (bin): bump pkgver, refresh sums
makepkg -g   # paste sha256sums_* into PKGBUILD
makepkg --printsrcinfo > .SRCINFO && git commit -am "vX.Y.Z" && git push
```

Install: `yay -S refconnect-git` or `yay -S refconnect-bin` (once published to the AUR).

## Self-updater behavior

`internal/update.ManagedInstall()` detects the `APPIMAGE` env var, the
`/usr/bin` and `/app` prefixes, and `dpkg -S` / `rpm -qf` / `pacman -Qo`
ownership probes. `/usr/local/bin` and `/opt` are treated as manual
`setup.sh` installs, so the in-app updater still works there. On managed
installs `Install()` refuses and the UI (`internal/ui/updater.go`) tells the
user to run `apt upgrade` / `dnf upgrade` (pointing hand-installed package users
at the repository page) or download a fresh AppImage.

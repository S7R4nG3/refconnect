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
* macOS / Windows unchanged.

All Linux artifacts take their version from the tag (`VERSION=${GITHUB_REF_NAME#v}`).
The binaries are built on Ubuntu 24.04 runners; as of v0.14.0 they need glibc
2.38 or newer (Debian 13+, Ubuntu 24.04+, Fedora 39+). The CI log prints the
computed floor for each build. The tarball and AppImage have the same floor;
the AppImage does not bundle glibc or the GL/X11 libraries.

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
user to download the new `.deb`/`.rpm`/AppImage from GitHub Releases
(there is no APT/DNF repository yet, so `apt`/`dnf upgrade` will not find it).

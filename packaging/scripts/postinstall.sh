#!/bin/sh
# Refresh desktop/icon caches when available. No-ops are fine in containers.
set -e
command -v update-desktop-database >/dev/null 2>&1 && update-desktop-database -q >/dev/null 2>&1 || true
command -v gtk-update-icon-cache >/dev/null 2>&1 && gtk-update-icon-cache -q -t /usr/share/icons/hicolor >/dev/null 2>&1 || true

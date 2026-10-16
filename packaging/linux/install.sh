#!/bin/sh
# Installs the verdana binary sitting next to this script, plus its menu entry
# and icon, for the current user (or system-wide when run as root).
#
#   ./install.sh             install
#   ./install.sh uninstall   remove what install put there
#
# PREFIX overrides where things go (default ~/.local, or /usr/local as root).
set -eu

here=$(cd "$(dirname "$0")" && pwd)
if [ -z "${PREFIX:-}" ]; then
  if [ "$(id -u)" = 0 ]; then PREFIX=/usr/local; else PREFIX="$HOME/.local"; fi
fi

bin="$PREFIX/bin/verdana"
desktop="$PREFIX/share/applications/com.verdana.app.desktop"
icon="$PREFIX/share/icons/hicolor/512x512/apps/com.verdana.app.png"

refresh() {
  command -v update-desktop-database >/dev/null 2>&1 &&
    update-desktop-database -q "$PREFIX/share/applications" || true
  command -v gtk-update-icon-cache >/dev/null 2>&1 &&
    gtk-update-icon-cache -q -t "$PREFIX/share/icons/hicolor" || true
}

if [ "${1:-}" = uninstall ]; then
  rm -f "$bin" "$desktop" "$icon"
  refresh
  echo "verdana removed from $PREFIX"
  exit 0
fi

install -Dm755 "$here/verdana" "$bin"
install -Dm644 "$here/com.verdana.app.png" "$icon"
# the entry names the binary by its full path, so it works even when
# $PREFIX/bin is not on the PATH the desktop session was started with
mkdir -p "$(dirname "$desktop")"
sed "s|^Exec=verdana|Exec=$bin|" "$here/com.verdana.app.desktop" > "$desktop"
chmod 644 "$desktop"
refresh

echo "verdana installed to $bin"

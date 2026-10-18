#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
cv_os="$(go env GOOS)"; cv_arch="$(go env GOARCH)"
case "$cv_os" in
 darwin) cv_default="$HOME/Library/Application Support/CodexVision" ;;
 linux) cv_default="${XDG_DATA_HOME:-$HOME/.local/share}/codex-vision" ;;
 *) echo 'Use install.ps1 on Windows' >&2; exit 1 ;;
esac
cv_destination="${1:-$cv_default}"
if [ -e "$cv_destination/codex-vision" ]; then echo 'Executable exists; choose a new destination. No file replaced.' >&2; exit 1; fi
./scripts/build.sh "$cv_os" "$cv_arch"
mkdir -p "$cv_destination"
cp "bin/codex-vision-$cv_os-$cv_arch" "$cv_destination/codex-vision"
if [ ! -e "$cv_destination/config.json" ]; then cp configs/config.example.json "$cv_destination/config.json"; fi
printf 'Installed in %s\nNo startup service, PATH or Codex config was changed.\n' "$cv_destination"

#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
cv_os="${1:-$(go env GOOS)}"
cv_arch="${2:-$(go env GOARCH)}"
case "$cv_os/$cv_arch" in
 darwin/amd64|darwin/arm64) cv_cgo=1; if [ "$(uname -s)" != Darwin ]; then echo 'macOS builds require Apple SDK on a Mac' >&2; exit 1; fi ;;
 windows/amd64|windows/arm64|linux/amd64|linux/arm64) cv_cgo=0 ;;
 *) echo "Unsupported native target: $cv_os/$cv_arch" >&2; exit 1 ;;
esac
cv_suffix=''
if [ "$cv_os" = windows ]; then cv_suffix='.exe'; fi
mkdir -p bin
GOOS="$cv_os" GOARCH="$cv_arch" CGO_ENABLED="$cv_cgo" go build -trimpath -o "bin/codex-vision-$cv_os-$cv_arch$cv_suffix" ./cmd/codex-vision
printf 'Built bin/codex-vision-%s-%s%s\n' "$cv_os" "$cv_arch" "$cv_suffix"

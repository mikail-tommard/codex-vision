#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
for cv_os in windows linux; do
 for cv_arch in amd64 arm64; do ./scripts/build.sh "$cv_os" "$cv_arch"; done
done
if [ "$(uname -s)" = Darwin ]; then
 for cv_arch in amd64 arm64; do ./scripts/build.sh darwin "$cv_arch"; done
fi

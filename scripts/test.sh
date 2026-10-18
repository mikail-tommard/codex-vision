#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
case "${1:-}" in
 --native) export CODEX_VISION_NATIVE_INTEGRATION=1 ;;
 --race) go test -race ./...; go vet ./...; exit ;;
 '') ;;
 *) echo 'usage: scripts/test.sh [--native|--race]' >&2; exit 1 ;;
esac
go test -v ./...
go vet ./...

#!/bin/sh
set -eu
if [ "$#" -ne 1 ]; then echo 'usage: scripts/stop.sh /absolute/path/to/codex-vision.stop' >&2; exit 1; fi
# noclobber prevents replacing any existing file; an existing path already stops input.
if [ ! -e "$1" ]; then (set -C; : > "$1"); fi
printf 'Stop latched for servers configured to use %s\n' "$1"

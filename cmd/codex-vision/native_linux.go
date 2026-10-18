//go:build linux

package main

import (
	"example.com/codex-vision/internal/platform"
	linuxdesktop "example.com/codex-vision/internal/platform/linux"
)

func newNative() (platform.Desktop, error) { return linuxdesktop.New() }

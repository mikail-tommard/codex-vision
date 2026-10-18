//go:build windows && (amd64 || arm64)

package main

import (
	"example.com/codex-vision/internal/platform"
	win "example.com/codex-vision/internal/platform/windows"
)

func newNative() (platform.Desktop, error) { return win.New() }

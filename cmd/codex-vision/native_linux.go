//go:build linux

package main

import (
	"example.com/codex-vision/internal/platform"
	linuxdesktop "example.com/codex-vision/internal/platform/linux"
	"example.com/codex-vision/internal/platform/wayland"
	"os"
)

func newNative() (platform.Desktop, error) {
	if (os.Getenv("XDG_SESSION_TYPE") == "wayland" || (os.Getenv("XDG_SESSION_TYPE") != "x11" && os.Getenv("WAYLAND_DISPLAY") != "")) && os.Getenv("CODEX_VISION_WAYLAND_PORTAL") == "1" {
		if os.Getenv("CODEX_VISION_WAYLAND_STREAM") == "1" {
			return wayland.NewStreaming()
		}
		return wayland.New()
	}
	return linuxdesktop.New()
}

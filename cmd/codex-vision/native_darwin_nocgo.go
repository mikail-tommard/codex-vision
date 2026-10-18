//go:build darwin && !cgo

package main

import (
	"example.com/codex-vision/internal/platform"
	"fmt"
)

func newNative() (platform.Desktop, error) {
	return nil, fmt.Errorf("native macOS requires CGO_ENABLED=1 and Apple command-line tools; rebuild or use --mock")
}
func nativeDoctor(bool) error { return fmt.Errorf("native macOS doctor requires a cgo build") }

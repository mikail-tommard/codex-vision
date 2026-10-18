//go:build !windows && !darwin && !linux

package main

import (
	"example.com/codex-vision/internal/platform"
	"fmt"
)

func newNative() (platform.Desktop, error) {
	return nil, fmt.Errorf("native desktop is unsupported on this OS; use --mock only for synthetic tests")
}

//go:build windows && !amd64 && !arm64

package main

import (
	"example.com/codex-vision/internal/platform"
	"fmt"
)

func newNative() (platform.Desktop, error) {
	return nil, fmt.Errorf("only Windows amd64 and arm64 are supported")
}

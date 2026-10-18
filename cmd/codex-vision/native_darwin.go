//go:build darwin && cgo

package main

import (
	"encoding/json"
	"example.com/codex-vision/internal/platform"
	"example.com/codex-vision/internal/platform/macos"
	"os"
)

func newNative() (platform.Desktop, error) { return macos.New() }
func nativeDoctor(request bool) error {
	status, e := macos.Permissions(request)
	if e != nil {
		return e
	}
	return json.NewEncoder(os.Stdout).Encode(status)
}

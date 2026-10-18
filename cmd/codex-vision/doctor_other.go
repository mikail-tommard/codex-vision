//go:build !darwin

package main

import (
	"context"
	"encoding/json"
	"os"
	"runtime"
	"time"
)

func nativeDoctor(request bool) error {
	d, e := newNative()
	out := map[string]any{"os": runtime.GOOS, "arch": runtime.GOARCH, "local_permission_prompt": false}
	if e != nil {
		out["available"] = false
		out["error"] = e.Error()
	} else {
		out["backend"] = d.Name()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		ms, err := d.Monitors(ctx)
		out["available"] = err == nil
		out["monitors"] = ms
		if err != nil {
			out["error"] = err.Error()
		}
	}
	return json.NewEncoder(os.Stdout).Encode(out)
}

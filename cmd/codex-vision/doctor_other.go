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
		if diagnostic, ok := d.(interface {
			Diagnostics(context.Context) (any, error)
		}); ok {
			detail, err := diagnostic.Diagnostics(ctx)
			out["session"] = detail
			if err != nil {
				out["available"] = false
				out["error"] = err.Error()
				return json.NewEncoder(os.Stdout).Encode(out)
			}
		}
		ms, err := d.Monitors(ctx)
		out["available"] = err == nil
		out["monitors"] = ms
		if err != nil {
			out["error"] = err.Error()
		}
	}
	return json.NewEncoder(os.Stdout).Encode(out)
}

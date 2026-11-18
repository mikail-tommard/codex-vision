//go:build !darwin

package main

import (
	"context"
	"encoding/json"
	"example.com/codex-vision/internal/platform"
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
		defer d.Release()
		out["backend"] = d.Name()
		if _, portal := d.(platform.ConsentedScreenshot); portal {
			out["available"] = nil
			out["availability_verified"] = false
			out["capabilities"] = platform.Capabilities(d)
			out["reason"] = "portal capture requires an explicit screen_capture and system consent; doctor does not open the dialog"
			return json.NewEncoder(os.Stdout).Encode(out)
		}
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

// Package command runs fixed native utility invocations, never shell strings.
package command

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"time"
)

type Runner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}
type Native struct{}
type bounded struct {
	bytes.Buffer
	limit int
}

func (w *bounded) Write(p []byte) (int, error) {
	if len(p) > w.limit-w.Len() {
		return 0, fmt.Errorf("native command output exceeds limit")
	}
	return w.Buffer.Write(p)
}
func (Native) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = 250 * time.Millisecond
	out := &bounded{limit: 128 << 20}
	cmd.Stdout = out
	var stderr bounded
	stderr.limit = 65536
	cmd.Stderr = &stderr
	if e := cmd.Run(); e != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%s failed: %w", filepath.Base(name), e)
	}
	return out.Bytes(), nil
}

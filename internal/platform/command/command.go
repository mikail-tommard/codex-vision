// Package command runs fixed native utility invocations, never shell strings.
package command

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type Runner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}

// InputRunner passes sensitive payloads over stdin rather than process arguments.
type InputRunner interface {
	RunInput(context.Context, string, string, ...string) ([]byte, error)
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
	return run(ctx, name, nil, args...)
}

func (Native) RunInput(ctx context.Context, name, input string, args ...string) ([]byte, error) {
	return run(ctx, name, strings.NewReader(input), args...)
}

func run(ctx context.Context, name string, input io.Reader, args ...string) ([]byte, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = input
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
	if input != nil && stderr.Len() != 0 {
		// xdotool can skip an unsupported character while returning exit 0.
		// Never echo stderr (it may contain text); treat delivery as partial.
		return nil, fmt.Errorf("%s reported diagnostics; input may be partial", filepath.Base(name))
	}
	return out.Bytes(), nil
}

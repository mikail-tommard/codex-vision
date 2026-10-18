package platform

import (
	"context"
	"runtime"
	"strings"
)

// WindowProcessMatcher verifies OS-managed executable aliases using native
// process identity. Window labels or client-supplied booleans are not evidence.
type WindowProcessMatcher interface {
	MatchesWindowProcess(context.Context, Window, string) (bool, error)
}

func ProcessEqual(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}
func ProcessMatches(ctx context.Context, d Desktop, w Window, expected string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if ProcessEqual(w.Process, expected) {
		return true, nil
	}
	if matcher, ok := d.(WindowProcessMatcher); ok {
		return matcher.MatchesWindowProcess(ctx, w, expected)
	}
	return false, nil
}

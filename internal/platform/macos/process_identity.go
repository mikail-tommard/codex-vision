package macos

import (
	"path"
	"strings"
	"unicode"
)

// Limit signed-alias recognition to the observed macOS code-sign clone layout.
// The path is only a candidate filter; it never grants authorization by itself.
func codeSignCloneCandidate(raw, expected string) bool {
	for _, p := range []string{raw, expected} {
		if !path.IsAbs(p) || path.Clean(p) != p || strings.ContainsFunc(p, unicode.IsControl) {
			return false
		}
	}
	if path.Base(raw) != path.Base(expected) {
		return false
	}
	if !strings.Contains(raw, ".code_sign_clone/") {
		return false
	}
	bin := path.Dir(raw)
	if path.Base(bin) != "MacOS" || path.Base(path.Dir(bin)) != "Contents" {
		return false
	}
	if !strings.HasSuffix(path.Dir(path.Dir(bin)), ".app.bundle") {
		return false
	}
	bin = path.Dir(expected)
	return path.Base(bin) == "MacOS" && path.Base(path.Dir(bin)) == "Contents" && strings.HasSuffix(path.Dir(path.Dir(bin)), ".app")
}

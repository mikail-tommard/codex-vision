//go:build darwin && cgo

package macos

import (
	"example.com/codex-vision/internal/platform"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestReviewJavaScriptCompiles(t *testing.T) {
	cmd := exec.Command("/usr/bin/osacompile", "-l", "JavaScript", "-o", filepath.Join(t.TempDir(), "review.scpt"), "-")
	cmd.Stdin = strings.NewReader(reviewScript(platform.Action{Tool: "keyboard_type", Text: "test\n\"Привет\""}, platform.Window{Process: "/Test.app", ID: "test"}))
	if out, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("review script does not compile: %v: %s", e, out)
	}
}

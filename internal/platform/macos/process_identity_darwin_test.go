//go:build darwin && cgo

package macos

import (
	"context"
	"errors"
	"example.com/codex-vision/internal/platform"
	"os"
	"path/filepath"
	"testing"
)

func TestSignedCloneVerificationUsesObservedPIDAndRawPath(t *testing.T) {
	const installed = "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
	const clone = "/private/var/folders/test/X/com.google.Chrome.code_sign_clone/session/Google Chrome.app.bundle/Contents/MacOS/Google Chrome"
	for _, kind := range []string{"verified", "signature_denied", "native_error", "unknown_window", "changed_path", "different_executable", "cancelled"} {
		t.Run(kind, func(t *testing.T) {
			d, _ := New()
			d.readWindows = func(context.Context, bool) ([]nativeWindow, error) {
				return []nativeWindow{{NativeID: 42, PID: 77, Process: clone, Bounds: platform.Rect{Width: 640, Height: 480}}}, nil
			}
			ws, err := d.Windows(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			w := ws[0]
			expected := installed
			calls := 0
			d.verifyProcess = func(ctx context.Context, pid int, raw, reference string) (bool, error) {
				calls++
				if pid != 77 || raw != clone || reference != installed {
					t.Fatal("unbound process verification", pid, raw, reference)
				}
				if kind == "native_error" {
					return false, errors.New("invalid signature")
				}
				return kind == "verified", nil
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch kind {
			case "unknown_window":
				w.ID = "unknown"
			case "changed_path":
				w.Process = "/private/tmp/com.google.Chrome.code_sign_clone/other/Google Chrome.app.bundle/Contents/MacOS/Google Chrome"
			case "different_executable":
				expected = "/Applications/Test.app/Contents/MacOS/Test"
			case "cancelled":
				cancel()
			}
			ok, err := d.MatchesWindowProcess(ctx, w, expected)
			if ok != (kind == "verified") {
				t.Fatal("identity match", ok, err)
			}
			wantCall := kind == "verified" || kind == "signature_denied" || kind == "native_error"
			if (calls == 1) != wantCall {
				t.Fatal("verification bypassed observed identity", calls, kind)
			}
		})
	}
}

func TestExecutableCopyRequiresIdenticalStableRegularFiles(t *testing.T) {
	dir := t.TempDir()
	reference := filepath.Join(dir, "reference")
	copy := filepath.Join(dir, "clone")
	payload := make([]byte, 140000)
	for i := range payload {
		payload[i] = byte(i % 251)
	}
	if err := os.WriteFile(reference, payload, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(copy, payload, 0600); err != nil {
		t.Fatal(err)
	}
	if !executableCopyMatches(copy, reference) {
		t.Fatal("identical executable copy rejected")
	}
	payload[len(payload)-1] ^= 1
	if err := os.WriteFile(copy, payload, 0600); err != nil {
		t.Fatal(err)
	}
	if executableCopyMatches(copy, reference) {
		t.Fatal("modified executable copy accepted")
	}
	if executableCopyMatches(dir, reference) || executableCopyMatches(filepath.Join(dir, "missing"), reference) {
		t.Fatal("nonregular or missing file accepted")
	}
	link := filepath.Join(dir, "symlink")
	if err := os.Symlink(reference, link); err != nil {
		t.Fatal(err)
	}
	if executableCopyMatches(link, reference) {
		t.Fatal("symlink alias accepted without clone identity")
	}
	empty := filepath.Join(dir, "empty")
	if err := os.WriteFile(empty, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if executableCopyMatches(empty, empty) {
		t.Fatal("empty executable accepted")
	}
}

package macos

import "testing"

func TestCodeSignCloneCandidateIsNotAuthorization(t *testing.T) {
	const installed = "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
	const clone = "/private/var/folders/test/X/com.google.Chrome.code_sign_clone/session/Google Chrome.app.bundle/Contents/MacOS/Google Chrome"
	if !codeSignCloneCandidate(clone, installed) {
		t.Fatal("real clone layout not recognized")
	}
	for _, raw := range []string{
		installed,
		"/private/tmp/Google Chrome",
		"/private/tmp/com.google.Chrome.code_sign_clone_fake/session/Google Chrome.app.bundle/Contents/MacOS/Google Chrome",
		"/private/tmp/com.google.Chrome.code_sign_clone/../Google Chrome.app.bundle/Contents/MacOS/Google Chrome",
		"/private/tmp/com.google.Chrome.code_sign_clone/session/Google Chrome.app.bundle/Contents/MacOS/Google Chrome Helper",
		clone + "\x00",
	} {
		if codeSignCloneCandidate(raw, installed) {
			t.Fatalf("unexpected candidate %q", raw)
		}
	}
	if codeSignCloneCandidate(clone, "/bin/Google Chrome") {
		t.Fatal("non-bundle reference accepted")
	}
}

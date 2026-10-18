//go:build darwin && cgo

package macos

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestNativeScrollTargetRequiresFocusedWindow(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	dir := t.TempDir()
	source, binary := filepath.Join(dir, "scroll.c"), filepath.Join(dir, "scroll")
	code := `#include "scroll_target.h"
#include <assert.h>
int main(void){
 cv_focus_rect focused={-800,28,800,600},missing={0},other={-700,28,600,400};
 // Normal unobstructed Quartz hit needs no AX fallback.
 assert(cv_scroll_target(42,focused,-400,150,1,0,missing));
 // A noninteractive overlay's bounding box no longer causes a false denial:
 // native AX must independently identify the same owner AND focused window.
 assert(cv_scroll_target(42,focused,-400,150,0,42,focused));
 // A real foreign window, missing AX evidence, or another window in the same
 // process must not be silently ignored to reach a window underneath.
 assert(!cv_scroll_target(42,focused,-400,150,0,99,focused));
 assert(!cv_scroll_target(42,focused,-400,150,0,0,missing));
 assert(!cv_scroll_target(42,focused,-400,150,0,42,other));
 assert(!cv_scroll_target(42,focused,-400,150,0,42,missing));
 assert(!cv_scroll_target(0,focused,-400,150,1,42,focused));
 // Menu bar, other monitor, exclusive lower/right bounds, invalid data.
 assert(!cv_scroll_target(42,focused,-400,20,1,42,focused));
 assert(!cv_scroll_target(42,focused,100,150,1,42,focused));
 assert(!cv_scroll_target(42,focused,0,150,1,42,focused));
 assert(!cv_scroll_target(42,focused,-400,628,1,42,focused));
 assert(!cv_scroll_target(42,focused,NAN,150,1,42,focused));
 focused.width=INFINITY;
 assert(!cv_scroll_target(42,focused,-400,150,1,42,focused));
 return 0;
}`
	if err := os.WriteFile(source, []byte(code), 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("clang", "-std=c11", "-Wall", "-Werror", "-I", filepath.Dir(file), source, "-o", binary).CombinedOutput(); err != nil {
		t.Fatalf("compile: %v: %s", err, out)
	}
	if out, err := exec.Command(binary).CombinedOutput(); err != nil {
		t.Fatalf("scroll policy: %v: %s", err, out)
	}
}

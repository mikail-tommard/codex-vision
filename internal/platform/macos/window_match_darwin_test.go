//go:build darwin && cgo

package macos

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestNativeWindowSelectionRequiresGeometryAndUniqueTitle(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	dir := t.TempDir()
	source := filepath.Join(dir, "match.c")
	binary := filepath.Join(dir, "match")
	code := `#include "window_match.h"
#include <assert.h>
int main(void){cv_focus_rect target={10,20,800,600};cv_window_candidate a[3]={{{10,20,800,600},1,0},{{10,20,800,600},1,1},{{20,20,800,600},1,1}};
 cv_window_match m=cv_select_window(target,a,1);assert(m.index==0&&m.bounds_matches==1);
 m=cv_select_window(target,a,3);assert(m.index==1&&m.bounds_matches==2&&m.title_matches==1);
 a[0].title_match=1;m=cv_select_window(target,a,3);assert(m.index==-1&&m.title_matches==2);
 a[0].title_match=0;a[1].title_match=0;m=cv_select_window(target,a,3);assert(m.index==-1&&m.bounds_matches==2&&m.title_matches==0);
 a[0].valid=0;a[1].valid=0;m=cv_select_window(target,a,3);assert(m.index==-1&&m.bounds_matches==0);
 a[0].valid=1;a[0].bounds.width=0;m=cv_select_window(target,a,1);assert(m.index==-1);
 a[0].bounds.width=NAN;m=cv_select_window(target,a,1);assert(m.index==-1);
 assert(cv_unique_active_window(2,1,1,1)==1);
 assert(cv_unique_active_window(2,0,-1,1)==-1);
 assert(cv_unique_active_window(2,2,1,1)==-1);
 assert(cv_unique_active_window(2,1,1,0)==-1);
 assert(cv_unique_active_window(2,1,2,1)==-1);
 assert(cv_focus_match_retryable(0)&&cv_focus_match_retryable(-25212)&&cv_focus_match_retryable(-25204));
 assert(!cv_focus_match_retryable(-25211)&&!cv_focus_match_retryable(-25205));
 return 0;}`
	if e := os.WriteFile(source, []byte(code), 0600); e != nil {
		t.Fatal(e)
	}
	if out, e := exec.Command("clang", "-std=c11", "-Wall", "-Werror", "-I", filepath.Dir(file), source, "-o", binary).CombinedOutput(); e != nil {
		t.Fatalf("compile: %v: %s", e, out)
	}
	if out, e := exec.Command(binary).CombinedOutput(); e != nil {
		t.Fatalf("selection: %v: %s", e, out)
	}
}

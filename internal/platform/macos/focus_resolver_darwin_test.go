//go:build darwin && cgo

package macos

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// Exercise the exact native resolver with synthetic readers, without touching
// the user's foreground, permissions, windows or event stream.
func TestNativeFocusResolverMissingValueFallback(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	dir := t.TempDir()
	source := filepath.Join(dir, "focus.c")
	binary := filepath.Join(dir, "focus")
	code := `#include "focus_resolver.h"
#include <assert.h>
typedef struct {int system_error,front_error,app_error,pid1,pid2,front_calls,app_calls,bad_bounds;} fixture;
static int sys(void *p,int *pid){fixture *f=p;*pid=42;return f->system_error;}
static int front(void *p,int *pid){fixture *f=p;*pid=f->front_calls++?f->pid2:f->pid1;return f->front_error;}
static int bounds(void *p,int pid,cv_focus_rect *out){fixture *f=p;f->app_calls++;assert(pid==42);*out=(cv_focus_rect){10,20,f->bad_bounds?0:640,480};return f->app_error;}
int main(void){cv_focus_readers read={sys,front,bounds};cv_focus_result out;fixture f={0};
 assert(cv_resolve_focus(&read,&f,&out));assert(f.front_calls==0&&f.app_calls==1&&!out.used_fallback);
 f=(fixture){.system_error=-25212,.pid1=42,.pid2=42};assert(cv_resolve_focus(&read,&f,&out));assert(out.pid==42&&out.used_fallback&&f.front_calls==2&&f.app_calls==1);
 f=(fixture){.system_error=-25212,.pid1=42,.pid2=99};assert(!cv_resolve_focus(&read,&f,&out));assert(out.stage==CV_FOCUS_CHANGED);
 f=(fixture){.system_error=-25212,.front_error=-1};assert(!cv_resolve_focus(&read,&f,&out));assert(f.app_calls==0);
 f=(fixture){.system_error=-25211};assert(!cv_resolve_focus(&read,&f,&out));assert(f.front_calls==0&&f.app_calls==0);
 f=(fixture){.system_error=-25204};assert(!cv_resolve_focus(&read,&f,&out));assert(f.front_calls==0);
 f=(fixture){.system_error=-25212,.pid1=42,.pid2=42,.app_error=-25212};assert(!cv_resolve_focus(&read,&f,&out));assert(out.stage==CV_FOCUS_WINDOW);
 f=(fixture){.system_error=-25212,.pid1=42,.pid2=42,.bad_bounds=1};assert(!cv_resolve_focus(&read,&f,&out));assert(out.stage==CV_FOCUS_BOUNDS);
 f=(fixture){.system_error=-25212,.pid1=0};assert(!cv_resolve_focus(&read,&f,&out));assert(f.app_calls==0);
 return 0;}`
	if e := os.WriteFile(source, []byte(code), 0600); e != nil {
		t.Fatal(e)
	}
	cmd := exec.Command("clang", "-std=c11", "-Wall", "-Werror", "-I", filepath.Dir(file), source, "-o", binary)
	if out, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("compile: %v: %s", e, out)
	}
	if out, e := exec.Command(binary).CombinedOutput(); e != nil {
		t.Fatalf("resolve: %v: %s", e, out)
	}
}

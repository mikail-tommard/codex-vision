//go:build darwin && cgo

package macos

import (
	"encoding/json"
	"example.com/codex-vision/internal/platform"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// Cross the actual Foundation JSON -> Go boundary without querying or mutating
// the desktop. C logical expressions/Boolean are not assumed to box as bool.
func TestNativeUIFlagsSerializeAsJSONBooleans(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	dir := t.TempDir()
	source := filepath.Join(dir, "flags.m")
	binary := filepath.Join(dir, "flags")
	code := `#import "json_bool.h"
#import <CoreFoundation/CoreFoundation.h>
#include <stdio.h>
int main(void){@autoreleasepool{
 BOOL enabled=YES,secure=NO,truncated=YES;Boolean settable=1;
 NSDictionary *field=@{@"role":@"AXTextField",@"label":@"Name",@"enabled":cv_json_bool(enabled),@"editable":cv_json_bool(settable&&!secure),@"secure":cv_json_bool(secure)};
 NSDictionary *out=@{@"fields":@{@"elements":@[field],@"truncated":cv_json_bool(truncated),@"visited":@1},@"controls":@{@"controls":@[@{@"role":@"AXButton",@"label":@"Ready",@"enabled":cv_json_bool(CFBooleanGetValue(kCFBooleanTrue))},@{@"role":@"AXButton",@"label":@"Disabled",@"enabled":cv_json_bool(CFBooleanGetValue(kCFBooleanFalse))},@{@"role":@"AXButton",@"label":@"Unknown",@"enabled":NSNull.null}],@"truncated":cv_json_bool(NO),@"visited":@3},@"window":@{@"on_screen":cv_json_bool(NO)}};
 NSData *data=[NSJSONSerialization dataWithJSONObject:out options:0 error:nil];fwrite(data.bytes,1,data.length,stdout);return 0;
}}`
	if err := os.WriteFile(source, []byte(code), 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("clang", "-fobjc-arc", "-framework", "Foundation", "-I", filepath.Dir(file), source, "-o", binary).CombinedOutput(); err != nil {
		t.Fatalf("compile: %v: %s", err, out)
	}
	raw, err := exec.Command(binary).Output()
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Fields   platform.UISnapshot      `json:"fields"`
		Controls platform.ControlSnapshot `json:"controls"`
		Window   platform.Window          `json:"window"`
	}
	if err = json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("native UI JSON rejected: %v: %s", err, raw)
	}
	if len(parsed.Fields.Elements) != 1 || !parsed.Fields.Elements[0].Enabled || !parsed.Fields.Elements[0].Editable || parsed.Fields.Elements[0].Secure || !parsed.Fields.Truncated {
		t.Fatal(parsed.Fields)
	}
	controls := parsed.Controls.Controls
	if len(controls) != 3 || controls[0].Enabled == nil || !*controls[0].Enabled || controls[1].Enabled == nil || *controls[1].Enabled || controls[2].Enabled != nil || parsed.Controls.Truncated {
		t.Fatal(parsed.Controls)
	}
	if parsed.Window.OnScreen == nil || *parsed.Window.OnScreen {
		t.Fatal(parsed.Window)
	}
}

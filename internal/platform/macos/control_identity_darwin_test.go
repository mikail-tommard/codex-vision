//go:build darwin && cgo

package macos

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// Exercise the actual native cache using Foundation objects; no desktop access,
// TCC changes, mouse events, UI contents or model calls are involved.
func TestNativeControlIdentityIsScopedBoundedAndExpires(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	dir := t.TempDir()
	source, binary := filepath.Join(dir, "identity.m"), filepath.Join(dir, "identity")
	code := `#import "control_identity.h"
#include <assert.h>
int main(void){@autoreleasepool{
 CVControlIdentityCache *cache=[CVControlIdentityCache new];
 NSObject *root=[NSObject new],*element=[NSObject new];
 [cache beginRoot:root document:@"private-document-A" at:10];
 NSString *context=cache.contextID,*identity=[cache identityFor:element];
 assert([cache.contextKind isEqual:@"native_window_document"]);
 [cache beginRoot:root document:@"private-document-A" at:69];
 assert([cache.contextID isEqual:context]&&[[cache identityFor:element] isEqual:identity]);
 assert(![[cache identityFor:[NSObject new]] isEqual:identity]);
 [cache beginRoot:root document:@"private-document-B" at:69];
 assert(![cache.contextID isEqual:context]&&![cache elementForIdentity:identity]);
 context=cache.contextID;identity=[cache identityFor:element];
 [cache beginRoot:root document:@"private-document-B" at:129];
 assert(![cache.contextID isEqual:context]&&![[cache identityFor:element] isEqual:identity]);
 context=cache.contextID;[cache beginRoot:[NSObject new] document:nil at:130];
 assert(![cache.contextID isEqual:context]&&[cache.contextKind isEqual:@"native_window"]);
 id first=nil;NSString *firstID=nil;
 for(int i=0;i<2000;i++){id obj=[NSObject new];NSString *token=[cache identityFor:obj];assert(token);if(i==0){first=obj;firstID=token;}}
 assert(![cache identityFor:[NSObject new]]&&[[cache identityFor:first] isEqual:firstID]);
 [cache beginRoot:root document:nil at:131];assert(![cache elementForIdentity:firstID]);
 return 0;
}}`
	if err := os.WriteFile(source, []byte(code), 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("clang", "-fobjc-arc", "-framework", "Foundation", "-framework", "CoreFoundation", "-I", filepath.Dir(file), source, "-o", binary).CombinedOutput(); err != nil {
		t.Fatalf("compile identity cache: %v: %s", err, out)
	}
	if out, err := exec.Command(binary).CombinedOutput(); err != nil {
		t.Fatalf("identity cache: %v: %s", err, out)
	}
}

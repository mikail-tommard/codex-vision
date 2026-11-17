//go:build darwin && cgo

package macos

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"example.com/codex-vision/internal/config"
	"example.com/codex-vision/internal/platform"
	"example.com/codex-vision/internal/safety"
	"log/slog"
)

const nativeControlFixture = `#import <AppKit/AppKit.h>
#include <stdio.h>
@interface Fixture : NSObject<NSApplicationDelegate>
@property(strong) NSWindow *window;
@property(strong) NSButton *button;
@property(strong) NSMutableArray *retired;
@end
@implementation Fixture
- (void)addButton {
 self.button=[[NSButton alloc] initWithFrame:NSMakeRect(40,100,130,32)];
 self.button.title=@"Execute";self.button.bezelStyle=NSBezelStyleRounded;
 self.button.target=self;self.button.action=@selector(clicked:);
 [self.window.contentView addSubview:self.button];
}
- (void)clicked:(id)sender {puts("CLICKED");fflush(stdout);}
- (void)applicationDidFinishLaunching:(NSNotification *)note {
 self.window=[[NSWindow alloc] initWithContentRect:NSMakeRect(0,0,480,240) styleMask:NSWindowStyleMaskTitled backing:NSBackingStoreBuffered defer:NO];
 self.window.title=@"Codex Vision controlled native fixture";
 self.retired=[NSMutableArray array];
 self.window.representedURL=[NSURL fileURLWithPath:[NSProcessInfo.processInfo.arguments[1] stringByAppendingPathComponent:@"document-a"]];
 [self addButton];[self.window center];[self.window makeKeyAndOrderFront:nil];[NSApp activateIgnoringOtherApps:YES];
 puts("READY");fflush(stdout);
 dispatch_async(dispatch_get_global_queue(QOS_CLASS_USER_INITIATED,0), ^{
  char *line=NULL;size_t capacity=0;
  while(getline(&line,&capacity,stdin)>0){NSString *command=[@(line) stringByTrimmingCharactersInSet:NSCharacterSet.whitespaceAndNewlineCharacterSet];
   dispatch_sync(dispatch_get_main_queue(), ^{
    if([command isEqual:@"document"]){self.window.representedURL=[NSURL fileURLWithPath:[NSProcessInfo.processInfo.arguments[1] stringByAppendingPathComponent:@"document-b"]];self.window.title=@"Codex Vision controlled native fixture";puts("DOCUMENT");}
    else if([command isEqual:@"replace"]){[self.retired addObject:self.button];[self.button removeFromSuperview];self.button=nil;[self addButton];puts("REPLACED");}
    fflush(stdout);
   });
  }
  free(line);
 });
}
@end
int main(int argc,const char **argv){@autoreleasepool{
 if(argc!=2)return 2;NSApplication *app=NSApplication.sharedApplication;
 [app setActivationPolicy:NSApplicationActivationPolicyRegular];Fixture *fixture=[Fixture new];app.delegate=fixture;[app run];return 0;
}}`

func buildNativeControlFixture(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	source, binary := filepath.Join(dir, "fixture.m"), filepath.Join(dir, "native-fixture")
	if err := os.WriteFile(source, []byte(nativeControlFixture), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "clang", "-fobjc-arc", "-framework", "AppKit", source, "-o", binary).CombinedOutput(); err != nil {
		t.Fatalf("compile native GUI fixture: %v: %s", err, out)
	}
	return binary, dir
}

func TestNativeControlledFixtureCompiles(t *testing.T) { buildNativeControlFixture(t) }

type countedNativeDesktop struct {
	*Desktop
	executions int
}

func (d *countedNativeDesktop) Execute(ctx context.Context, a platform.Action) error {
	d.executions++
	return d.Desktop.Execute(ctx, a)
}

// Explicit input opt-in is distinct from the existing read-only integration
// flag. Only our freshly compiled fixture executable is locally allowlisted.
func TestNativeControlledUIIntegration(t *testing.T) {
	if os.Getenv("CODEX_VISION_NATIVE_INPUT_INTEGRATION") != "1" {
		t.Skip("requires explicit CODEX_VISION_NATIVE_INPUT_INTEGRATION=1 and unlocked macOS GUI")
	}
	p, err := Permissions(false)
	if err != nil {
		t.Fatal(err)
	}
	if !statusBool(p["unlocked_console"]) || !statusBool(p["accessibility_permission"]) || !statusBool(p["post_event_permission"]) {
		t.Skip("native input unavailable: console, Accessibility and post-event permissions are required; no input was attempted")
	}
	binary, dir := buildNativeControlFixture(t)
	realBinary, err := filepath.EvalSymlinks(binary)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	cmd := exec.CommandContext(ctx, binary, dir)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); _ = cmd.Wait() })
	reader := bufio.NewReader(stdout)
	reply := func(wanted string) {
		t.Helper()
		result := make(chan string, 1)
		go func() { line, _ := reader.ReadString('\n'); result <- strings.TrimSpace(line) }()
		select {
		case line := <-result:
			if line != wanted {
				t.Fatalf("fixture response: want %q, got %q", wanted, line)
			}
		case <-ctx.Done():
			t.Fatal("native fixture timed out", ctx.Err())
		}
	}
	reply("READY")
	d, err := New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Release() })
	var w platform.Window
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		w, err = d.Active(ctx)
		if err == nil && platform.ProcessEqual(w.Process, realBinary) {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(20 * time.Millisecond):
		}
	}
	if err != nil || !platform.ProcessEqual(w.Process, realBinary) {
		t.Fatal("fixture foreground not confirmed; no input attempted", err)
	}
	read := func() (platform.ControlSnapshot, platform.UIControl) {
		t.Helper()
		raw, err := d.ReadControls(ctx, w, 256)
		if err != nil || raw.Truncated || raw.Context == nil {
			t.Fatal("native control snapshot unavailable", err)
		}
		for _, c := range raw.Controls {
			if c.Label == "Execute" && c.Role == "AXButton" && c.Identity != "" {
				return raw, c
			}
		}
		t.Fatal("fixture button missing native identity")
		return raw, platform.UIControl{}
	}
	before, old := read()
	if _, err := fmt.Fprintln(stdin, "replace"); err != nil {
		t.Fatal(err)
	}
	reply("REPLACED")
	_, fresh := read()
	if old.Identity == fresh.Identity || old.Label != fresh.Label || old.Bounds != fresh.Bounds {
		t.Fatal("fixture did not replace an otherwise identical native element")
	}
	cfg := config.Default()
	cfg.Mode, cfg.LocalApproval, cfg.ConfirmEachAction = "assist", true, false
	cfg.Allow, cfg.AllowedProcesses = []string{"mouse_click"}, []string{realBinary}
	cfg.StopFile = filepath.Join(dir, "stop")
	backend := &countedNativeDesktop{Desktop: d}
	session := safety.New(cfg, backend, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	action := func(raw platform.ControlSnapshot, c platform.UIControl) platform.Action {
		return platform.Action{Tool: "mouse_click", WindowID: w.ID, X: c.Bounds.X + c.Bounds.Width/2, Y: c.Bounds.Y + c.Bounds.Height/2, Button: "left", Count: 1, ControlExpected: &c, ControlUniqueSelector: true, ControlMaxNodes: 256, ContextExpected: raw.Context}
	}
	if err := session.Execute(ctx, action(before, old)); err == nil || backend.executions != 0 {
		t.Fatal("same-looking replacement received native input", err)
	}
	t.Run("document_context", func(t *testing.T) {
		if before.Context.Kind != "native_window_document" {
			t.Skip("fixture/OS does not expose AXDocument; document-context behavior remains unverified")
		}
		if _, err := fmt.Fprintln(stdin, "document"); err != nil {
			t.Fatal(err)
		}
		reply("DOCUMENT")
		after, _ := read()
		afterWindow, err := d.Active(ctx)
		if err != nil || afterWindow.ID != w.ID || afterWindow.Title != w.Title {
			t.Fatal("fixture did not preserve window ID/title across document switch", err)
		}
		if platform.MatchUIContext(before.Context, after.Context) {
			t.Fatal("document switch retained old context")
		}
	})
	current, target := read()
	if err := session.Execute(ctx, action(current, target)); err != nil {
		t.Fatal("verified native fixture click failed", err)
	}
	reply("CLICKED") // Application callback, not just OS event acceptance.
}

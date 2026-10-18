//go:build darwin && cgo

package macos

import (
	"context"
	"errors"
	"example.com/codex-vision/internal/platform"
	"strings"
	"testing"
)

func TestSemanticUIRequiresAccessibilityAndHonorsCancel(t *testing.T) {
	d, _ := New()
	s := allowedState()
	s.AccessibilityPermission = false
	d.readPermissions = func() PermissionState { return s }
	if _, err := d.ReadUI(context.Background(), platform.Window{}, 100); err == nil || !strings.Contains(err.Error(), "accessibility_permission_denied") {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := d.ReadUI(ctx, platform.Window{}, 100); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := d.Execute(ctx, platform.Action{Tool: "ui_fill"}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if !d.Supports("ui_fill") {
		t.Fatal("semantic fill capability missing")
	}
}

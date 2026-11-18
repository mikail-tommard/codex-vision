package wayland

import (
	"context"
	"os"
	"testing"
	"time"

	"example.com/codex-vision/internal/platform"
	"example.com/codex-vision/internal/vision"
)

// Explicit operator opt-in: choose a disposable test window in the system
// portal. Never runs in ordinary CI, saves pixels, or generates GUI input.
func TestNativeScreenCastIntegration(t *testing.T) {
	if os.Getenv("CODEX_VISION_WAYLAND_STREAM_INTEGRATION") != "1" {
		t.Skip("requires explicit operator consent in a real Wayland session")
	}
	if os.Getenv("XDG_SESSION_TYPE") != "wayland" && os.Getenv("WAYLAND_DISPLAY") == "" {
		t.Fatal("interactive Wayland session required")
	}
	desktop, err := NewStreaming()
	if err != nil {
		t.Fatal(err)
	}
	defer desktop.Release()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	source, err := desktop.(platform.ScreenCaster).StartScreenCast(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	im, err := source.Next(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if im == nil || vision.Validate(im.Bounds()) != nil {
		t.Fatal("no valid consented frame")
	}
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-source.Done():
	case <-time.After(time.Second):
		t.Fatal("closed source remained active")
	}
	if _, err := source.Next(ctx); err == nil {
		t.Fatal("closed source returned pixels")
	}
}

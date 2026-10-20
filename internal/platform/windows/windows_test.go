//go:build windows && (amd64 || arm64)

package windows

import (
	"context"
	"example.com/codex-vision/internal/platform"
	"image"
	"os"
	"testing"
	"unsafe"
)

func TestWin64ABILayout(t *testing.T) {
	if unsafe.Sizeof(packet{}) != 40 || unsafe.Offsetof(packet{}.Data) != 8 || unsafe.Sizeof(mousePacket{}) != 32 || unsafe.Sizeof(keyPacket{}) != 24 || unsafe.Sizeof(bitmapHeader{}) != 40 || unsafe.Sizeof(bitmapInfo{}) != 44 || unsafe.Offsetof(bitmapInfo{}.Colors) != 40 || unsafe.Sizeof(monitorInfo{}) != 104 {
		t.Fatal("Win32 ABI layout mismatch")
	}
}
func TestNativeObserveIntegration(t *testing.T) {
	if os.Getenv("CODEX_VISION_WINDOWS_INTEGRATION") != "1" {
		t.Skip("set CODEX_VISION_WINDOWS_INTEGRATION=1 in unlocked Windows desktop; read-only capture")
	}
	d, e := New()
	if e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	ms, e := d.Monitors(ctx)
	if e != nil {
		t.Fatal(e)
	}
	b, e := platform.Bounds(ms)
	if e != nil {
		t.Fatal(e)
	}
	r := platform.Rect{X: b.X, Y: b.Y, Width: 32, Height: 32}
	im, e := d.Capture(ctx, r)
	if e != nil || im.Bounds() != image.Rect(0, 0, 32, 32) {
		t.Fatal(e)
	}
	if _, e = d.Windows(ctx); e != nil {
		t.Fatal(e)
	}
	if _, e = d.Active(ctx); e != nil {
		t.Fatal(e)
	}
}

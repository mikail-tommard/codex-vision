// Package mock is a deterministic simulated desktop; it never controls the host.
package mock

import (
	"context"
	"example.com/codex-vision/internal/platform"
	"image"
	"image/color"
	"sync"
)

type Desktop struct {
	mu       sync.Mutex
	Actions  []platform.Action
	Released int
}

func (*Desktop) Name() string { return "mock (synthetic desktop)" }
func (*Desktop) Monitors(ctx context.Context) ([]platform.Monitor, error) {
	return []platform.Monitor{{ID: "mock-primary", Bounds: platform.Rect{X: 0, Y: 0, Width: 640, Height: 480}, Primary: true, DPI: 96}, {ID: "mock-left", Bounds: platform.Rect{X: -320, Y: 0, Width: 320, Height: 240}, DPI: 96}}, ctx.Err()
}
func (*Desktop) Capture(ctx context.Context, r platform.Rect) (image.Image, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	im := image.NewRGBA(image.Rect(0, 0, r.Width, r.Height))
	for y := 0; y < r.Height; y++ {
		for x := 0; x < r.Width; x++ {
			im.SetRGBA(x, y, color.RGBA{uint8(x + r.X), uint8(y + r.Y), 80, 255})
		}
	}
	return im, nil
}
func (*Desktop) Windows(ctx context.Context) ([]platform.Window, error) {
	return []platform.Window{{ID: "mock-window", Title: "Synthetic test window", Process: "mock.exe", Bounds: platform.Rect{X: 0, Y: 0, Width: 640, Height: 480}}}, ctx.Err()
}
func (d *Desktop) Active(ctx context.Context) (platform.Window, error) {
	w, e := d.Windows(ctx)
	return w[0], e
}
func (d *Desktop) Execute(ctx context.Context, a platform.Action) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.Actions = append(d.Actions, a)
	return nil
}
func (d *Desktop) Release() error { d.mu.Lock(); defer d.mu.Unlock(); d.Released++; return nil }

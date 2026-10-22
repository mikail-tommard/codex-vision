//go:build windows && (amd64 || arm64)

package windows

import (
	"context"
	"example.com/codex-vision/internal/input"
	"example.com/codex-vision/internal/platform"
	"fmt"
	"runtime"
	"time"
	"unicode/utf16"
	"unsafe"
)

// Win64 INPUT = DWORD type + 4 padding + 32-byte union. No 32-bit ABI assumptions.
type packet struct {
	Type    uint32
	Padding uint32
	Data    [32]byte
}
type mousePacket struct {
	X, Y              int32
	Data, Flags, Time uint32
	Extra             uintptr
}
type keyPacket struct {
	VK, Scan    uint16
	Flags, Time uint32
	Extra       uintptr
}

func mouse(x, y int32, data, flags uint32) packet {
	p := packet{}
	*(*mousePacket)(unsafe.Pointer(&p.Data[0])) = mousePacket{X: x, Y: y, Data: data, Flags: flags}
	return p
}
func key(vk, scan uint16, flags uint32) packet {
	p := packet{Type: 1}
	*(*keyPacket)(unsafe.Pointer(&p.Data[0])) = keyPacket{VK: vk, Scan: scan, Flags: flags}
	return p
}
func raw(p packet) error {
	r, _, e := sendInput.Call(1, uintptr(unsafe.Pointer(&p)), unsafe.Sizeof(p))
	if r != 1 {
		return fmt.Errorf("SendInput failed (UIPI, desktop change, or OS rejection): %w", e)
	}
	return nil
}
func (d *Desktop) push(ctx context.Context, p packet, release *packet) error {
	if e := d.ready(ctx); e != nil {
		return e
	}
	if release != nil {
		d.mu.Lock()
		d.held = append(d.held, *release)
		d.mu.Unlock()
	}
	return raw(p)
}
func (d *Desktop) Release() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	var first error
	pending := []packet{}
	for i := len(d.held) - 1; i >= 0; i-- {
		if e := raw(d.held[i]); e != nil {
			if first == nil {
				first = e
			}
			pending = append(pending, d.held[i])
		}
	}
	d.held = pending
	return first
}
func (d *Desktop) move(ctx context.Context, x, y int, b platform.Rect) error {
	xx, yy, e := platform.Normalize(x, y, b)
	if e != nil {
		return e
	}
	return d.push(ctx, mouse(xx, yy, 0, 0x0001|0x8000|0x4000), nil)
}
func (d *Desktop) Execute(ctx context.Context, a platform.Action) (err error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err = d.ready(ctx); err != nil {
		return err
	}
	defer func() {
		e := d.Release()
		if err == nil {
			err = e
		}
	}()
	ms, e := d.Monitors(ctx)
	if e != nil {
		return e
	}
	b, e := platform.Bounds(ms)
	if e != nil {
		return e
	}
	if e = input.Validate(a, b); e != nil {
		return e
	}
	switch a.Tool {
	case "mouse_move":
		if a.Duration != 0 {
			return fmt.Errorf("smooth mouse movement currently requires the macOS backend")
		}
		return d.move(ctx, a.X, a.Y, b)
	case "mouse_scroll":
		var point struct{ X, Y int32 }
		r, _, e := getCursor.Call(uintptr(unsafe.Pointer(&point)))
		if r == 0 {
			return fmt.Errorf("GetCursorPos: %w", e)
		}
		h, _, _ := getForeground.Call()
		under, _, _ := windowFromPoint.Call(uintptr(uint64(uint32(point.X)) | uint64(uint32(point.Y))<<32))
		root, _, _ := getAncestor.Call(under, 2)
		if h == 0 || root != h {
			return fmt.Errorf("scroll requires cursor above foreground window")
		}
		flags := uint32(0x0800)
		if a.Horizontal {
			flags = 0x1000
		}
		return d.push(ctx, mouse(0, 0, uint32(int32(a.Delta)), flags), nil)
	case "mouse_click", "mouse_drag":
		if e = d.move(ctx, a.X, a.Y, b); e != nil {
			return e
		}
		down, up := uint32(2), uint32(4)
		if a.Button == "right" {
			down, up = 8, 16
		}
		if a.Button == "middle" {
			down, up = 32, 64
		}
		release := mouse(0, 0, 0, up)
		count := a.Count
		if a.Tool == "mouse_drag" {
			count = 1
		}
		for i := 0; i < count; i++ {
			if e = d.push(ctx, mouse(0, 0, 0, down), &release); e != nil {
				return e
			}
			if a.Tool == "mouse_drag" {
				steps := max(1, int(a.Duration/(10*time.Millisecond)))
				timer := time.NewTicker(a.Duration / time.Duration(steps))
				for j := 1; j <= steps; j++ {
					select {
					case <-ctx.Done():
						timer.Stop()
						return ctx.Err()
					case <-timer.C:
					}
					if e = d.move(ctx, a.X+(a.EndX-a.X)*j/steps, a.Y+(a.EndY-a.Y)*j/steps, b); e != nil {
						timer.Stop()
						return e
					}
				}
				timer.Stop()
			}
			if e = d.Release(); e != nil {
				return e
			}
		}
		return nil
	case "keyboard_type":
		for _, scan := range utf16.Encode([]rune(a.Text)) {
			release := key(0, scan, 4|2)
			if e = d.push(ctx, key(0, scan, 4), &release); e != nil {
				return e
			}
			if e = d.Release(); e != nil {
				return e
			}
		}
		return nil
	case "keyboard_press", "keyboard_hotkey":
		for _, s := range a.Keys {
			vk, e := input.Key(s)
			if e != nil {
				return e
			}
			flags := uint32(0)
			if vk >= 33 && vk <= 46 || vk == 91 {
				flags = 1
			}
			release := key(vk, 0, flags|2)
			if e = d.push(ctx, key(vk, 0, flags), &release); e != nil {
				return e
			}
		}
		return d.Release()
	case "window_focus":
		h, e := d.resolve(a.WindowID)
		if e != nil {
			return e
		}
		if e = d.ready(ctx); e != nil {
			return e
		}
		return focusWindow(ctx, h, 2*time.Second, focusOps{
			check: func(ctx context.Context) error {
				if err := d.ready(ctx); err != nil {
					return err
				}
				current, err := d.resolve(a.WindowID)
				if err != nil {
					return err
				}
				if current != h {
					return fmt.Errorf("focus window identity changed; stop input")
				}
				return nil
			},
			foreground: func() uintptr { active, _, _ := getForeground.Call(); return active },
			minimized:  func() bool { iconic, _, _ := user.NewProc("IsIconic").Call(h); return iconic != 0 },
			restore:    func() bool { accepted, _, _ := user.NewProc("ShowWindowAsync").Call(h, 9); return accepted != 0 }, // SW_RESTORE
			raise:      func() bool { accepted, _, _ := user.NewProc("BringWindowToTop").Call(h); return accepted != 0 },
			activate:   func() bool { accepted, _, _ := user.NewProc("SetForegroundWindow").Call(h); return accepted != 0 },
		})
	}
	return fmt.Errorf("unsupported action")
}

func (*Desktop) Supports(tool string) bool {
	switch tool {
	case "mouse_move", "mouse_scroll", "window_focus", "mouse_click", "mouse_drag", "keyboard_type", "keyboard_press", "keyboard_hotkey":
		return true
	}
	return false
}

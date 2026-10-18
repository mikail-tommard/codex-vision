//go:build windows && (amd64 || arm64)

package windows

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"example.com/codex-vision/internal/platform"
	"fmt"
	"golang.org/x/sys/windows"
	"unsafe"
)

func (d *Desktop) describe(h uintptr) (platform.Window, error) {
	var pid uint32
	_, e := windows.GetWindowThreadProcessId(windows.HWND(h), &pid)
	if e != nil || pid == 0 {
		return platform.Window{}, fmt.Errorf("window no longer available")
	}
	var b nativeRect
	r, _, e := getRect.Call(h, uintptr(unsafe.Pointer(&b)))
	if r == 0 {
		return platform.Window{}, fmt.Errorf("GetWindowRect: %w", e)
	}
	var title [1024]uint16
	getText.Call(h, uintptr(unsafe.Pointer(&title[0])), uintptr(len(title)))
	process := ""
	p, e := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if e == nil {
		var name [32768]uint16
		n := uint32(len(name))
		if windows.QueryFullProcessImageName(p, 0, &name[0], &n) == nil {
			process = windows.UTF16ToString(name[:n])
		}
		windows.CloseHandle(p)
	}
	d.mu.Lock()
	tok, ok := d.tokens[h]
	if !ok || tok.PID != pid {
		var id [16]byte
		if _, e = rand.Read(id[:]); e != nil {
			d.mu.Unlock()
			return platform.Window{}, e
		}
		tok = token{ID: hex.EncodeToString(id[:]), PID: pid}
		if len(d.tokens) >= 4096 {
			d.mu.Unlock()
			return platform.Window{}, fmt.Errorf("window token limit reached; restart locally")
		}
		d.tokens[h] = tok
	}
	d.mu.Unlock()
	return platform.Window{ID: tok.ID, Title: windows.UTF16ToString(title[:]), Process: process, Bounds: platform.Rect{X: int(b.Left), Y: int(b.Top), Width: int(b.Right - b.Left), Height: int(b.Bottom - b.Top)}}, nil
}
func (d *Desktop) Windows(ctx context.Context) ([]platform.Window, error) {
	d.windowMu.Lock()
	defer d.windowMu.Unlock()
	if d.windowCallback == 0 {
		d.windowCallback = windows.NewCallback(func(h, data uintptr) uintptr { return d.windowFn(h, data) })
	}
	if e := d.ready(ctx); e != nil {
		return nil, e
	}
	ws := []platform.Window{}
	d.windowFn = func(h, data uintptr) uintptr {
		if ctx.Err() != nil || len(ws) >= 1024 {
			return 0
		}
		visible, _, _ := isVisible.Call(h)
		if visible != 0 {
			w, e := d.describe(h)
			if e == nil {
				ws = append(ws, w)
			}
		}
		return 1
	}
	defer func() { d.windowFn = nil }()
	r, _, e := enumWindows.Call(d.windowCallback, 0)
	if ce := ctx.Err(); ce != nil {
		return nil, ce
	}
	if r == 0 && len(ws) < 1024 {
		return nil, fmt.Errorf("EnumWindows failed: %w", e)
	}
	return ws, nil
}
func (d *Desktop) Active(ctx context.Context) (platform.Window, error) {
	if e := d.ready(ctx); e != nil {
		return platform.Window{}, e
	}
	h, _, _ := getForeground.Call()
	if h == 0 {
		return platform.Window{}, fmt.Errorf("no foreground window")
	}
	return d.describe(h)
}
func (d *Desktop) resolve(id string) (uintptr, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for h, t := range d.tokens {
		if t.ID == id {
			var pid uint32
			_, e := windows.GetWindowThreadProcessId(windows.HWND(h), &pid)
			if e != nil || pid != t.PID {
				return 0, fmt.Errorf("stale window ID")
			}
			return h, nil
		}
	}
	return 0, fmt.Errorf("unknown window ID; list windows first")
}

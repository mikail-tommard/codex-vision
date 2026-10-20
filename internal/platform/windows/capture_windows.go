//go:build windows && (amd64 || arm64)

package windows

import (
	"context"
	"example.com/codex-vision/internal/platform"
	"example.com/codex-vision/internal/vision"
	"fmt"
	"golang.org/x/sys/windows"
	"image"
	"runtime"
	"unsafe"
)

type monitorInfo struct {
	Size    uint32
	Monitor nativeRect
	Work    nativeRect
	Flags   uint32
	Device  [32]uint16
}

func (d *Desktop) Monitors(ctx context.Context) ([]platform.Monitor, error) {
	d.monitorMu.Lock()
	defer d.monitorMu.Unlock()
	if d.monitorCallback == 0 {
		d.monitorCallback = windows.NewCallback(func(h, dc, rect, data uintptr) uintptr { return d.monitorFn(h, dc, rect, data) })
	}
	if e := d.ready(ctx); e != nil {
		return nil, e
	}
	var ms []platform.Monitor
	var callErr error
	d.monitorFn = func(h, dc, rect, data uintptr) uintptr {
		if ctx.Err() != nil {
			return 0
		}
		mi := monitorInfo{}
		mi.Size = uint32(unsafe.Sizeof(mi))
		r, _, e := getMonitorInfo.Call(h, uintptr(unsafe.Pointer(&mi)))
		if r == 0 {
			callErr = e
			return 0
		}
		b := mi.Monitor
		ms = append(ms, platform.Monitor{ID: windows.UTF16ToString(mi.Device[:]), Bounds: platform.Rect{X: int(b.Left), Y: int(b.Top), Width: int(b.Right - b.Left), Height: int(b.Bottom - b.Top)}, Primary: mi.Flags&1 != 0})
		return 1
	}
	defer func() { d.monitorFn = nil }()
	r, _, e := enumMonitors.Call(0, 0, d.monitorCallback, 0)
	if callErr != nil {
		return nil, callErr
	}
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	if r == 0 {
		return nil, fmt.Errorf("EnumDisplayMonitors failed: %w", e)
	}
	return ms, nil
}

type bitmapHeader struct {
	Size                   uint32
	Width, Height          int32
	Planes, BitCount       uint16
	Compression, SizeImage uint32
	XPels, YPels           int32
	ClrUsed, ClrImportant  uint32
}

// BITMAPINFO includes the color-table slot, even though BI_RGB at 32 bpp
// needs no palette. Do not pass just a BITMAPINFOHEADER as LPBITMAPINFO.
type bitmapInfo struct {
	Header bitmapHeader
	Colors [1]uint32
}

func (d *Desktop) Capture(ctx context.Context, r platform.Rect) (image.Image, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if e := d.ready(ctx); e != nil {
		return nil, e
	}
	if !r.Valid() {
		return nil, fmt.Errorf("invalid region")
	}
	if e := vision.Validate(image.Rect(0, 0, r.Width, r.Height)); e != nil {
		return nil, e
	}
	ms, e := d.Monitors(ctx)
	if e != nil {
		return nil, e
	}
	b, e := platform.Bounds(ms)
	if e != nil || !b.Includes(r) {
		return nil, fmt.Errorf("capture outside virtual desktop")
	}
	dc, _, e := user.NewProc("GetDC").Call(0)
	if dc == 0 {
		return nil, fmt.Errorf("GetDC: %w", e)
	}
	defer user.NewProc("ReleaseDC").Call(0, dc)
	mem, _, e := gdi.NewProc("CreateCompatibleDC").Call(dc)
	if mem == 0 {
		return nil, fmt.Errorf("CreateCompatibleDC: %w", e)
	}
	defer gdi.NewProc("DeleteDC").Call(mem)
	info := bitmapInfo{Header: bitmapHeader{Size: uint32(unsafe.Sizeof(bitmapHeader{})), Width: int32(r.Width), Height: -int32(r.Height), Planes: 1, BitCount: 32}}
	var pixels unsafe.Pointer
	bmp, _, e := gdi.NewProc("CreateDIBSection").Call(dc, uintptr(unsafe.Pointer(&info)), 0, uintptr(unsafe.Pointer(&pixels)), 0, 0)
	if bmp == 0 {
		return nil, fmt.Errorf("CreateDIBSection (%dx%d): %w", r.Width, r.Height, e)
	}
	defer gdi.NewProc("DeleteObject").Call(bmp)
	if pixels == nil {
		return nil, fmt.Errorf("CreateDIBSection returned a bitmap without pixel storage")
	}
	selectObj := gdi.NewProc("SelectObject")
	old, _, e := selectObj.Call(mem, bmp)
	if old == 0 || old == ^uintptr(0) {
		return nil, fmt.Errorf("SelectObject: %w", e)
	}
	selected := true
	defer func() {
		if selected {
			selectObj.Call(mem, old)
		}
	}()
	clear, _, clearErr := gdi.NewProc("PatBlt").Call(mem, 0, 0, uintptr(r.Width), uintptr(r.Height), 0x42)
	if clear == 0 {
		return nil, fmt.Errorf("clear capture buffer: %w", clearErr)
	}
	ok, _, e := gdi.NewProc("BitBlt").Call(mem, 0, 0, uintptr(r.Width), uintptr(r.Height), dc, uintptr(r.X), uintptr(r.Y), 0x00CC0020|0x40000000)
	if ok == 0 {
		return nil, fmt.Errorf("BitBlt: %w", e)
	}
	// Synchronize GDI drawing before touching DIB memory on this same locked
	// OS thread. No DDB conversion/GetDIBits or retry with partial pixels.
	flushed, _, _ := gdi.NewProc("GdiFlush").Call()
	if flushed == 0 {
		return nil, fmt.Errorf("GdiFlush failed while completing screen capture")
	}
	restored, _, e := selectObj.Call(mem, old)
	if restored == 0 || restored == ^uintptr(0) {
		return nil, fmt.Errorf("restore GDI object: %w", e)
	}
	selected = false
	// 32-bpp scan lines are DWORD aligned at any width. Copy to Go-owned
	// storage before DeleteObject frees the native DIB allocation.
	im, e := copyDIBPixels(ctx, r.Width, r.Height, unsafe.Slice((*byte)(pixels), r.Width*r.Height*4))
	if e != nil {
		return nil, e
	}
	if e = d.ready(ctx); e != nil {
		return nil, e
	}
	return im, nil
}

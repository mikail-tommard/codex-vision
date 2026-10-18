//go:build windows && (amd64 || arm64)

package windows

import (
	"context"
	"fmt"
	"golang.org/x/sys/windows"
	"strings"
	"sync"
	"unsafe"
)

var user = windows.NewLazySystemDLL("user32.dll")
var gdi = windows.NewLazySystemDLL("gdi32.dll")
var enumMonitors = user.NewProc("EnumDisplayMonitors")
var getMonitorInfo = user.NewProc("GetMonitorInfoW")
var openDesktop = user.NewProc("OpenInputDesktop")
var closeDesktop = user.NewProc("CloseDesktop")
var userInfo = user.NewProc("GetUserObjectInformationW")
var getForeground = user.NewProc("GetForegroundWindow")
var getRect = user.NewProc("GetWindowRect")
var getText = user.NewProc("GetWindowTextW")
var isVisible = user.NewProc("IsWindowVisible")
var enumWindows = user.NewProc("EnumWindows")
var getCursor = user.NewProc("GetCursorPos")
var windowFromPoint = user.NewProc("WindowFromPoint")
var getAncestor = user.NewProc("GetAncestor")
var sendInput = user.NewProc("SendInput")

type nativeRect struct{ Left, Top, Right, Bottom int32 }
type token struct {
	ID  string
	PID uint32
}
type Desktop struct {
	mu              sync.Mutex
	tokens          map[uintptr]token
	held            []packet
	monitorMu       sync.Mutex
	monitorCallback uintptr
	monitorFn       func(uintptr, uintptr, uintptr, uintptr) uintptr
	windowMu        sync.Mutex
	windowCallback  uintptr
	windowFn        func(uintptr, uintptr) uintptr
}

func New() (*Desktop, error) {
	p := user.NewProc("SetProcessDpiAwarenessContext")
	if e := p.Find(); e != nil {
		return nil, fmt.Errorf("per-monitor DPI awareness requires Windows 10 1703 or later: %w", e)
	}
	// DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 = (HANDLE)-4.
	r, _, e := p.Call(^uintptr(3))
	if r == 0 {
		return nil, fmt.Errorf("cannot establish per-monitor-v2 DPI awareness: %w", e)
	}
	d := &Desktop{tokens: make(map[uintptr]token)}
	if e = d.ready(context.Background()); e != nil {
		return nil, e
	}
	return d, nil
}
func (*Desktop) Name() string { return "windows-win32" }
func (d *Desktop) ready(ctx context.Context) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	h, _, e := openDesktop.Call(0, 0, 1)
	if h == 0 {
		return fmt.Errorf("interactive desktop unavailable (locked, secure, or disconnected): %w", e)
	}
	defer closeDesktop.Call(h)
	var buf [256]uint16
	var needed uint32
	r, _, e := userInfo.Call(h, 2, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)*2), uintptr(unsafe.Pointer(&needed)))
	if r == 0 {
		return fmt.Errorf("cannot inspect input desktop: %w", e)
	}
	if !strings.EqualFold(windows.UTF16ToString(buf[:]), "Default") {
		return fmt.Errorf("secure or non-default desktop is unsupported")
	}
	return nil
}

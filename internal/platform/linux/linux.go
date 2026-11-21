// Package linux implements an X11 desktop using installed, local X utilities.
// The package is portable so command contracts can be tested without X11.
package linux

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"example.com/codex-vision/internal/platform"
	"example.com/codex-vision/internal/platform/command"
	"example.com/codex-vision/internal/vision"
	"fmt"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

type token struct {
	Native  string
	PID     string
	Process string
	Opaque  string
}
type Desktop struct {
	runner      command.Runner
	paths       map[string]string
	mu          sync.Mutex
	tokens      map[string]token
	readlink    func(string) (string, error)
	session     string
	display     string
	uid         int
	heldButtons map[int]bool
	heldKeys    []string
}

func New() (*Desktop, error) {
	if os.Getenv("XDG_SESSION_TYPE") == "wayland" || (os.Getenv("XDG_SESSION_TYPE") != "x11" && os.Getenv("WAYLAND_DISPLAY") != "") {
		return nil, fmt.Errorf("Wayland input requires a RemoteDesktop portal backend; XWayland is not a full desktop. Opt-in single screenshots: CODEX_VISION_WAYLAND_PORTAL=1; use X11 for existing desktop control")
	}
	display := os.Getenv("DISPLAY")
	if display == "" {
		return nil, fmt.Errorf("Linux X11 requires a local DISPLAY")
	}
	if _, e := localDisplay(display); e != nil {
		return nil, fmt.Errorf("remote X11 DISPLAY is disabled; use a local Unix display")
	}
	d := &Desktop{runner: command.Native{}, paths: map[string]string{}, tokens: map[string]token{}, readlink: os.Readlink, session: os.Getenv("XDG_SESSION_ID"), display: display, uid: os.Getuid(), heldButtons: map[int]bool{}}
	for _, name := range []string{"xrandr", "xprop", "xdotool", "import", "loginctl"} {
		path, e := exec.LookPath(name)
		if e != nil {
			return nil, fmt.Errorf("Linux X11 requires installed %s (not automatically installed)", name)
		}
		absolute, e := filepath.Abs(path)
		if e != nil {
			return nil, e
		}
		d.paths[name] = absolute
	}
	// Keep installations with ImageMagick alone usable. Distribution builds
	// of FFmpeg can omit x11grab, so check the compiled input devices once.
	if path, err := exec.LookPath("ffmpeg"); err == nil {
		if absolute, err := filepath.Abs(path); err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			devices, err := d.runner.Run(ctx, absolute, "-hide_banner", "-devices")
			cancel()
			if err == nil && hasX11Grab(devices) {
				d.paths["ffmpeg"] = absolute
			}
		}
	}
	if d.session != "" && !sessionIDRE.MatchString(d.session) {
		return nil, fmt.Errorf("invalid XDG_SESSION_ID; launch from the local desktop session")
	}
	return d, nil
}
func (*Desktop) Name() string { return "linux-x11" }
func (*Desktop) Supports(tool string) bool {
	switch tool {
	case "mouse_move", "mouse_scroll", "window_focus", "mouse_click", "mouse_drag", "keyboard_type", "keyboard_press", "keyboard_hotkey":
		return true
	}
	return false
}
func (d *Desktop) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return d.runner.Run(ctx, d.paths[name], args...)
}

var monitorRE = regexp.MustCompile(`^\s*\d+:\s+([^\s]+)\s+(\d+)/\d+x(\d+)/\d+([+-]\d+)([+-]\d+)\s+(.+)$`)

func parseMonitors(data []byte) ([]platform.Monitor, error) {
	ms := []platform.Monitor{}
	for _, line := range strings.Split(string(data), "\n") {
		v := monitorRE.FindStringSubmatch(line)
		if v == nil {
			continue
		}
		w, _ := strconv.Atoi(v[2])
		h, _ := strconv.Atoi(v[3])
		x, _ := strconv.Atoi(v[4])
		y, _ := strconv.Atoi(v[5])
		ms = append(ms, platform.Monitor{ID: strings.TrimSpace(v[6]), Bounds: platform.Rect{X: x, Y: y, Width: w, Height: h}, Primary: strings.Contains(v[1], "*")})
	}
	if _, e := platform.Bounds(ms); e != nil {
		return nil, fmt.Errorf("cannot parse xrandr monitors: %w", e)
	}
	return ms, nil
}
func (d *Desktop) Monitors(ctx context.Context) ([]platform.Monitor, error) {
	if err := d.unlocked(ctx); err != nil {
		return nil, err
	}
	return d.monitors(ctx)
}
func (d *Desktop) monitors(ctx context.Context) ([]platform.Monitor, error) {
	b, e := d.run(ctx, "xrandr", "--listmonitors")
	if e != nil {
		return nil, e
	}
	return parseMonitors(b)
}
func (d *Desktop) Capture(ctx context.Context, r platform.Rect) (image.Image, error) {
	if e := d.unlocked(ctx); e != nil {
		return nil, e
	}
	ms, e := d.monitors(ctx)
	if e != nil {
		return nil, e
	}
	b, e := platform.Bounds(ms)
	if e != nil || !b.Includes(r) {
		return nil, fmt.Errorf("region outside X11 desktop")
	}
	size := image.Rect(0, 0, r.Width, r.Height)
	if err := vision.Validate(size); err != nil {
		return nil, err
	}
	if r == b {
		return d.importPNG(ctx, r, "-window", "root")
	}
	if err := d.checkRootSize(ctx, b); err != nil {
		return nil, err
	}
	x, y := r.X-b.X, r.Y-b.Y
	var im image.Image
	if d.paths["ffmpeg"] != "" {
		im, e = d.grabRegion(ctx, size, x, y)
	} else {
		// ImageMagick reads the root window internally. Bound its allocation
		// and crop before PNG encoding so only the requested pixels cross stdout.
		if err := vision.Validate(image.Rect(0, 0, b.Width, b.Height)); err != nil {
			return nil, err
		}
		geometry := fmt.Sprintf("%dx%d+%d+%d", r.Width, r.Height, x, y)
		im, e = d.importPNG(ctx, r, "-window", "root", "-crop", geometry, "+repage")
	}
	if e != nil {
		return nil, e
	}
	if e = d.unlocked(ctx); e != nil {
		return nil, e
	}
	after, e := d.monitors(ctx)
	if e != nil {
		return nil, e
	}
	if !slices.Equal(ms, after) {
		return nil, fmt.Errorf("X11 monitor layout changed during capture")
	}
	if e = d.checkRootSize(ctx, b); e != nil {
		return nil, e
	}
	return im, ctx.Err()
}

func (d *Desktop) importPNG(ctx context.Context, r platform.Rect, args ...string) (image.Image, error) {
	data, e := d.run(ctx, "import", append(args, "png:-")...)
	if e != nil {
		return nil, e
	}
	cfg, e := png.DecodeConfig(bytes.NewReader(data))
	if e != nil {
		return nil, e
	}
	if cfg.Width != r.Width || cfg.Height != r.Height {
		return nil, fmt.Errorf("X11 frame size differs from requested region")
	}
	im, e := png.Decode(bytes.NewReader(data))
	if e != nil {
		return nil, e
	}
	return im, ctx.Err()
}

func (d *Desktop) grabRegion(ctx context.Context, size image.Rectangle, x, y int) (image.Image, error) {
	display, err := localDisplay(d.display)
	if err != nil {
		return nil, err
	}
	data, err := d.run(ctx, "ffmpeg", "-nostdin", "-hide_banner", "-loglevel", "error",
		"-f", "x11grab", "-draw_mouse", "0", "-video_size", fmt.Sprintf("%dx%d", size.Dx(), size.Dy()),
		"-grab_x", strconv.Itoa(x), "-grab_y", strconv.Itoa(y), "-framerate", "60", "-probesize", "32", "-i", display,
		"-frames:v", "1", "-filter_threads", "1", "-threads", "1", "-pix_fmt", "rgba", "-f", "rawvideo", "pipe:1")
	if err != nil {
		return nil, err
	}
	if len(data) != size.Dx()*size.Dy()*4 {
		return nil, fmt.Errorf("incomplete X11 region frame")
	}
	return &image.RGBA{Pix: data, Stride: size.Dx() * 4, Rect: size}, ctx.Err()
}
func hasX11Grab(devices []byte) bool {
	for _, line := range strings.Split(string(devices), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && strings.Contains(fields[0], "D") && fields[1] == "x11grab" {
			return true
		}
	}
	return false
}

func (d *Desktop) checkRootSize(ctx context.Context, b platform.Rect) error {
	data, err := d.run(ctx, "xdotool", "getdisplaygeometry")
	if err != nil {
		return err
	}
	fields := strings.Fields(string(data))
	if len(fields) != 2 || fields[0] != strconv.Itoa(b.Width) || fields[1] != strconv.Itoa(b.Height) {
		return fmt.Errorf("X11 root size differs from monitor layout")
	}
	return nil
}

var windowIDsRE = regexp.MustCompile(`0x[0-9a-fA-F]+`)
var pidRE = regexp.MustCompile(`=\s*(\d+)`)

func fields(data []byte) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		a, b, ok := strings.Cut(line, "=")
		if ok {
			out[strings.TrimSpace(a)] = strings.TrimSpace(b)
		}
	}
	return out
}
func (d *Desktop) describe(ctx context.Context, id string) (platform.Window, error) {
	if !regexp.MustCompile(`^(0x[0-9a-fA-F]+|[0-9]+)$`).MatchString(id) {
		return platform.Window{}, fmt.Errorf("invalid native window ID")
	}
	n, e := strconv.ParseUint(id, 0, 64)
	if e != nil {
		return platform.Window{}, e
	}
	id = strconv.FormatUint(n, 10)
	props, e := d.run(ctx, "xprop", "-id", id, "_NET_WM_PID", "_NET_WM_STATE", "WM_STATE")
	if e != nil {
		return platform.Window{}, e
	}
	if bytes.Contains(props, []byte("_NET_WM_STATE_HIDDEN")) || bytes.Contains(props, []byte("Iconic")) {
		return platform.Window{}, fmt.Errorf("window is not visible")
	}
	v := pidRE.FindSubmatch(props)
	if len(v) != 2 {
		return platform.Window{}, fmt.Errorf("window has no process identity")
	}
	pid := string(v[1])
	path, e := d.readlink("/proc/" + pid + "/exe")
	if e != nil {
		path = ""
	}
	geometry, e := d.run(ctx, "xdotool", "getwindowgeometry", "--shell", id)
	if e != nil {
		return platform.Window{}, e
	}
	f := fields(geometry)
	nums := map[string]int{}
	for _, name := range []string{"X", "Y", "WIDTH", "HEIGHT"} {
		value, e := strconv.Atoi(f[name])
		if e != nil {
			return platform.Window{}, fmt.Errorf("invalid window geometry")
		}
		nums[name] = value
	}
	title, e := d.run(ctx, "xdotool", "getwindowname", id)
	if e != nil {
		return platform.Window{}, e
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	t, ok := d.tokens[id]
	if !ok || t.PID != pid || t.Process != path {
		if len(d.tokens) >= 4096 {
			return platform.Window{}, fmt.Errorf("window ID budget exhausted")
		}
		var opaque [16]byte
		if _, e = rand.Read(opaque[:]); e != nil {
			return platform.Window{}, e
		}
		t = token{id, pid, path, hex.EncodeToString(opaque[:])}
		d.tokens[id] = t
	}
	return platform.Window{ID: t.Opaque, Title: strings.TrimSuffix(string(title), "\n"), Process: path, Bounds: platform.Rect{X: nums["X"], Y: nums["Y"], Width: nums["WIDTH"], Height: nums["HEIGHT"]}}, nil
}
func (d *Desktop) Windows(ctx context.Context) ([]platform.Window, error) {
	if e := d.unlocked(ctx); e != nil {
		return nil, e
	}
	data, e := d.run(ctx, "xprop", "-root", "_NET_CLIENT_LIST_STACKING")
	if e != nil {
		return nil, e
	}
	ids := windowIDsRE.FindAllString(string(data), 1025)
	if len(ids) > 1024 {
		return nil, fmt.Errorf("too many windows")
	}
	ws := []platform.Window{}
	for _, id := range ids {
		if e = ctx.Err(); e != nil {
			return nil, e
		}
		w, e := d.describe(ctx, id)
		if e == nil {
			ws = append(ws, w)
		}
	}
	return ws, ctx.Err()
}
func (d *Desktop) Active(ctx context.Context) (platform.Window, error) {
	if e := d.unlocked(ctx); e != nil {
		return platform.Window{}, e
	}
	return d.active(ctx)
}
func (d *Desktop) active(ctx context.Context) (platform.Window, error) {
	data, e := d.run(ctx, "xdotool", "getactivewindow")
	if e != nil {
		return platform.Window{}, e
	}
	return d.describe(ctx, strings.TrimSpace(string(data)))
}

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
	"image/draw"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

type token struct {
	Native  string
	PID     string
	Process string
	Opaque  string
}
type Desktop struct {
	runner   command.Runner
	paths    map[string]string
	mu       sync.Mutex
	tokens   map[string]token
	readlink func(string) (string, error)
	session  string
}

func New() (*Desktop, error) {
	if os.Getenv("XDG_SESSION_TYPE") == "wayland" || os.Getenv("WAYLAND_DISPLAY") != "" {
		return nil, fmt.Errorf("Wayland desktop control requires a consent-based portal backend; XWayland is not a full desktop; use an X11 session")
	}
	display := os.Getenv("DISPLAY")
	if display == "" {
		return nil, fmt.Errorf("Linux X11 requires a local DISPLAY")
	}
	if !regexp.MustCompile(`^(unix)?:[0-9]+(\.[0-9]+)?$`).MatchString(display) {
		return nil, fmt.Errorf("remote X11 DISPLAY is disabled; use a local Unix display")
	}
	d := &Desktop{runner: command.Native{}, paths: map[string]string{}, tokens: map[string]token{}, readlink: os.Readlink, session: os.Getenv("XDG_SESSION_ID")}
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
	if !regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_-]*$`).MatchString(d.session) {
		return nil, fmt.Errorf("Linux native backend requires XDG_SESSION_ID from a local logind desktop session")
	}
	return d, nil
}
func (*Desktop) Name() string { return "linux-x11" }
func (*Desktop) Supports(tool string) bool {
	return tool == "mouse_move" || tool == "mouse_scroll" || tool == "window_focus"
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
	ms, e := d.Monitors(ctx)
	if e != nil {
		return nil, e
	}
	b, e := platform.Bounds(ms)
	if e != nil || !b.Includes(r) {
		return nil, fmt.Errorf("region outside X11 desktop")
	}
	if e = vision.Validate(image.Rect(0, 0, b.Width, b.Height)); e != nil {
		return nil, e
	}
	data, e := d.run(ctx, "import", "-window", "root", "png:-")
	if e != nil {
		return nil, e
	}
	cfg, e := png.DecodeConfig(bytes.NewReader(data))
	if e != nil {
		return nil, e
	}
	if cfg.Width != b.Width || cfg.Height != b.Height {
		return nil, fmt.Errorf("X11 capture bounds changed or root contains unreported outputs; retry after monitor setup")
	}
	im, e := png.Decode(bytes.NewReader(data))
	if e != nil {
		return nil, e
	}
	out := image.NewRGBA(image.Rect(0, 0, r.Width, r.Height))
	draw.Draw(out, out.Bounds(), im, image.Pt(r.X-b.X, r.Y-b.Y), draw.Src)
	return out, ctx.Err()
}

var windowIDsRE = regexp.MustCompile(`0x[0-9a-fA-F]+`)
var pidRE = regexp.MustCompile(`=\s*(\d+)`)

func fields(data []byte) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		a, b, ok := strings.Cut(line, "=")
		if ok {
			out[a] = b
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
	data, e := d.run(ctx, "xdotool", "getactivewindow")
	if e != nil {
		return platform.Window{}, e
	}
	return d.describe(ctx, strings.TrimSpace(string(data)))
}
func (d *Desktop) unlocked(ctx context.Context) error {
	b, e := d.run(ctx, "loginctl", "show-session", d.session, "-p", "LockedHint", "-p", "Active", "-p", "Remote", "-p", "Type", "-p", "Display")
	if e != nil {
		return e
	}
	f := fields(b)
	if f["LockedHint"] != "no" || f["Active"] != "yes" || f["Remote"] != "no" || f["Type"] != "x11" {
		return fmt.Errorf("logind must report a local active unlocked X11 session; input and screen reads refused")
	}
	configured := strings.TrimPrefix(os.Getenv("DISPLAY"), "unix")
	if configured != "" && f["Display"] != configured {
		return fmt.Errorf("logind display does not match DISPLAY; refusing access")
	}
	return nil
}
func (d *Desktop) Execute(ctx context.Context, a platform.Action) error {
	if e := d.unlocked(ctx); e != nil {
		return e
	}
	switch a.Tool {
	case "mouse_move":
		if a.Duration != 0 {
			return fmt.Errorf("smooth mouse movement currently requires the macOS backend")
		}
		_, e := d.run(ctx, "xdotool", "mousemove", "--", strconv.Itoa(a.X), strconv.Itoa(a.Y))
		return e
	case "mouse_scroll":
		if a.Delta == 0 || a.Delta%120 != 0 {
			return fmt.Errorf("X11 scroll delta must be a nonzero multiple of 120")
		}
		point, e := d.run(ctx, "xdotool", "getmouselocation", "--shell")
		if e != nil {
			return e
		}
		active, e := d.run(ctx, "xdotool", "getactivewindow")
		if e != nil {
			return e
		}
		if fields(point)["WINDOW"] != strings.TrimSpace(string(active)) {
			return fmt.Errorf("cursor must be directly above foreground X11 window")
		}
		button := 4
		if a.Delta < 0 {
			button = 5
		}
		if a.Horizontal {
			button = 7
			if a.Delta < 0 {
				button = 6
			}
		}
		count := a.Delta / 120
		if count < 0 {
			count = -count
		}
		if count > 10 {
			return fmt.Errorf("scroll count exceeds limit")
		}
		_, e = d.run(ctx, "xdotool", "click", "--repeat", strconv.Itoa(count), "--delay", "0", strconv.Itoa(button))
		return e
	case "window_focus":
		d.mu.Lock()
		var target token
		for _, t := range d.tokens {
			if t.Opaque == a.WindowID {
				target = t
				break
			}
		}
		d.mu.Unlock()
		if target.Native == "" {
			return fmt.Errorf("unknown window ID")
		}
		fresh, e := d.describe(ctx, target.Native)
		if e != nil || fresh.ID != a.WindowID {
			return fmt.Errorf("stale window identity")
		}
		if e = d.unlocked(ctx); e != nil {
			return e
		}
		_, e = d.run(ctx, "xdotool", "windowactivate", "--sync", target.Native)
		return e
	default:
		return fmt.Errorf("Linux input action requires unavailable trusted local approval")
	}
}
func (*Desktop) Release() error { return nil }

package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"example.com/codex-vision/internal/config"
	"example.com/codex-vision/internal/platform"
	"example.com/codex-vision/internal/platform/mock"
	"fmt"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"image"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"
)

func connect(t *testing.T, c config.Config) (*mcp.ClientSession, *Service) {
	t.Helper()
	return connectDesktop(t, c, &mock.Desktop{})
}

func connectDesktop(t *testing.T, c config.Config, d platform.Desktop) (*mcp.ClientSession, *Service) {
	t.Helper()
	c.StopFile = filepath.Join(t.TempDir(), "stop")
	server, svc := New(c, d, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	t.Cleanup(svc.Close)
	st, ct := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	ss, e := server.Connect(ctx, st, nil)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { ss.Close() })
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	cs, e := client.Connect(ctx, ct, nil)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { cs.Close() })
	return cs, svc
}
func call(t *testing.T, c *mcp.ClientSession, name string, args any, ok bool) *mcp.CallToolResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r, e := c.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if e != nil {
		if ok {
			t.Fatal(name, e)
		}
		return nil
	}
	if r.IsError == ok {
		t.Fatalf("%s: unexpected isError=%v: %+v", name, r.IsError, r.Content)
	}
	return r
}
func TestMCPObserveFlow(t *testing.T) {
	c, _ := connect(t, config.Default())
	ts, e := c.ListTools(context.Background(), nil)
	if e != nil || len(ts.Tools) != 27 {
		t.Fatal(ts, e)
	}
	for _, tool := range ts.Tools {
		raw, e := json.Marshal(tool.InputSchema)
		if e != nil || !bytes.Contains(raw, []byte(`"type":"object"`)) {
			t.Fatal(tool.Name, string(raw), e)
		}
	}
	call(t, c, "screen_list_monitors", map[string]any{}, true)
	r := call(t, c, "screen_capture", map[string]any{"format": "png", "max_width": 480}, true)
	if len(r.Content) != 2 {
		t.Fatal(r)
	}
	im, ok := r.Content[1].(*mcp.ImageContent)
	if !ok || im.MIMEType != "image/png" {
		t.Fatal(r)
	}
	decoded, _, e := image.Decode(bytes.NewReader(im.Data))
	if e != nil || decoded.Bounds().Dx() != 480 || decoded.Bounds().Dy() != 240 {
		t.Fatal(decoded, e)
	}
	call(t, c, "screen_region", map[string]any{"x": -320, "y": 0, "width": 100, "height": 100, "format": "jpeg"}, true)
	call(t, c, "window_list", map[string]any{}, true)
	call(t, c, "window_get_active", map[string]any{}, true)
	call(t, c, "mouse_move", map[string]any{"x": 1, "y": 1}, false)
	call(t, c, "session_status", map[string]any{}, true)
	call(t, c, "session_stop", map[string]any{}, true)
}
func TestMCPSchemasAndValidation(t *testing.T) {
	c, _ := connect(t, config.Default())
	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{"mouse_move", map[string]any{"x": "not integer", "y": 1}}, {"mouse_move", map[string]any{"x": 1}}, {"mouse_move", map[string]any{"x": 1, "y": 1, "approved": true}}, {"mouse_move", map[string]any{"x": 1.5, "y": 1}},
		{"screen_region", map[string]any{"x": 0, "y": 0, "width": 0, "height": 1}}, {"screen_region", map[string]any{"x": 600, "y": 0, "width": 100, "height": 100}},
		{"screen_capture", map[string]any{"format": "gif"}}, {"screen_capture", map[string]any{"monitor_id": "missing"}}, {"screen_capture", map[string]any{"quality": 101}},
		{"keyboard_press", map[string]any{"key": "not-a-key"}}, {"keyboard_hotkey", map[string]any{"keys": []string{"CTRL", "ctrl"}}}, {"keyboard_type", map[string]any{"text": ""}}, {"mouse_scroll", map[string]any{"delta": 0}},
	} {
		t.Run(tc.name, func(t *testing.T) { call(t, c, tc.name, tc.args, false) })
	}
}
func TestMCPAssistAndStop(t *testing.T) {
	cfg := config.Default()
	cfg.Mode = "assist"
	cfg.Allow = []string{"mouse_move"}
	cfg.AllowedProcesses = []string{"mock.exe"}
	c, svc := connect(t, cfg)
	call(t, c, "mouse_move", map[string]any{"x": 1, "y": 1}, true)
	call(t, c, "mouse_click", map[string]any{"x": 1, "y": 1}, false)
	call(t, c, "keyboard_type", map[string]any{"text": "user approves"}, false)
	call(t, c, "session_stop", map[string]any{}, true)
	call(t, c, "mouse_move", map[string]any{"x": 1, "y": 1}, false)
	if len(svc.Desktop.(*mock.Desktop).Actions) != 1 {
		t.Fatal("policy bypass")
	}
}
func TestImagesDisabledAndReadBudget(t *testing.T) {
	cfg := config.Default()
	cfg.Images = false
	cfg.MaxPerTask = 1
	c, _ := connect(t, cfg)
	call(t, c, "screen_capture", map[string]any{}, false)
	call(t, c, "window_list", map[string]any{}, false)
	call(t, c, "session_status", map[string]any{}, true)
	call(t, c, "session_stop", map[string]any{}, true)
}

func TestPermissionDiagnosticsContractOnUnsupportedBackend(t *testing.T) {
	c, _ := connect(t, config.Default())
	r := call(t, c, "macos_permission_diagnostics", map[string]any{}, true)
	if !bytes.Contains([]byte(r.Content[0].(*mcp.TextContent).Text), []byte(`"supported":false`)) {
		t.Fatal(r)
	}
	call(t, c, "macos_permission_diagnostics", map[string]any{"request_accessibility": "yes"}, false)
	call(t, c, "macos_permission_diagnostics", map[string]any{"approved": true}, false)
}

func TestKeyboardWindowIDThroughMCP(t *testing.T) {
	cfg := config.Default()
	cfg.Mode = "assist"
	cfg.LocalApproval = true
	cfg.ConfirmEachAction = false
	cfg.Allow = []string{"keyboard_type", "keyboard_press", "keyboard_hotkey"}
	cfg.AllowedProcesses = []string{"mock.exe"}
	c, svc := connect(t, cfg)
	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{"keyboard_type", map[string]any{"text": "https://www.youtube.com/"}},
		{"keyboard_press", map[string]any{"key": "ENTER"}},
		{"keyboard_hotkey", map[string]any{"keys": []string{"CMD", "L"}}},
	} {
		tc.args["window_id"] = "browser"
		call(t, c, tc.name, tc.args, false)
		if len(svc.Desktop.(*mock.Desktop).Actions) != 0 {
			t.Fatal("input reached wrong window")
		}
	}
	call(t, c, "keyboard_type", map[string]any{"text": "test", "window_id": "mock-window"}, true)
	if len(svc.Desktop.(*mock.Desktop).Actions) != 1 {
		t.Fatal("expected window rejected")
	}
}

type launchDesktop struct {
	mock.Desktop
	process string
}

func (d *launchDesktop) Active(ctx context.Context) (platform.Window, error) {
	if len(d.Actions) == 0 {
		return platform.Window{}, fmt.Errorf("no visible foreground window (Spotlight)")
	}
	return platform.Window{ID: "chrome", Process: d.process, Bounds: platform.Rect{Width: 640, Height: 480}}, ctx.Err()
}

func TestLaunchMCPFromSpotlight(t *testing.T) {
	const process = "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
	for _, kind := range []string{"allow", "denied_process", "invalid_url", "extra_arguments", "lost_focus"} {
		t.Run(kind, func(t *testing.T) {
			cfg := config.Default()
			cfg.Mode = "assist"
			cfg.LocalApproval = true
			cfg.ConfirmEachAction = false
			cfg.Allow = []string{"app_launch"}
			cfg.AllowedProcesses = []string{process}
			d := &launchDesktop{process: process}
			c, _ := connectDesktop(t, cfg, d)
			args := map[string]any{"process": process, "url": "https://www.youtube.com/"}
			if kind == "denied_process" {
				args["process"] = "/Applications/Denied.app/Contents/MacOS/Denied"
			}
			if kind == "invalid_url" {
				args["url"] = "file:///private/tmp/test"
			}
			if kind == "extra_arguments" {
				args["args"] = []string{"--disable-web-security"}
			}
			if kind == "lost_focus" {
				d.process = "another-process"
			}
			r := call(t, c, "app_launch", args, kind == "allow")
			if kind == "allow" {
				var response struct {
					OK                 bool            `json:"ok"`
					Window             platform.Window `json:"window"`
					NavigationVerified bool            `json:"navigation_verified"`
				}
				if err := json.Unmarshal([]byte(r.Content[0].(*mcp.TextContent).Text), &response); err != nil {
					t.Fatal(err)
				}
				if !response.OK || response.Window.ID != "chrome" || response.NavigationVerified {
					t.Fatal(response)
				}
				if len(d.Actions) != 1 || d.Actions[0].URL != args["url"] || d.Actions[0].Process != process {
					t.Fatal(d.Actions)
				}
			} else if kind != "lost_focus" && len(d.Actions) != 0 {
				t.Fatal("rejected launch executed", d.Actions)
			}
		})
	}
}

type cursorDesktop struct {
	mock.Desktop
	point platform.Point
}

func (d *cursorDesktop) CursorPosition(ctx context.Context) (platform.Point, error) {
	return d.point, ctx.Err()
}
func TestCapturePointerMarkerCoordinatesAndCrop(t *testing.T) {
	d := &cursorDesktop{point: platform.Point{X: -10, Y: 20}}
	c, _ := connectDesktop(t, config.Default(), d)
	r := call(t, c, "screen_region", map[string]any{"x": -20, "y": 10, "width": 40, "height": 40}, true)
	var response struct {
		Cursor struct {
			Available bool    `json:"available"`
			Included  bool    `json:"included"`
			DesktopX  int     `json:"desktop_x"`
			ImageX    float64 `json:"image_x"`
			ImageY    float64 `json:"image_y"`
		} `json:"cursor"`
	}
	if err := json.Unmarshal([]byte(r.Content[0].(*mcp.TextContent).Text), &response); err != nil {
		t.Fatal(err)
	}
	if !response.Cursor.Available || !response.Cursor.Included || response.Cursor.DesktopX != -10 || response.Cursor.ImageX != 10 || response.Cursor.ImageY != 10 {
		t.Fatal(response)
	}
	picture := r.Content[1].(*mcp.ImageContent)
	decoded, _, err := image.Decode(bytes.NewReader(picture.Data))
	if err != nil {
		t.Fatal(err)
	}
	red, green, blue, _ := decoded.At(10, 10).RGBA()
	if red != 65535 || green != 65535 || blue != 65535 {
		t.Fatal("cursor hotspot missing")
	}
	d.point = platform.Point{X: 100, Y: 100}
	r = call(t, c, "screen_region", map[string]any{"x": -20, "y": 10, "width": 40, "height": 40}, true)
	if err = json.Unmarshal([]byte(r.Content[0].(*mcp.TextContent).Text), &response); err != nil {
		t.Fatal(err)
	}
	if response.Cursor.Included {
		t.Fatal("off-crop cursor shown")
	}
}

func TestSmoothMouseArgumentsThroughMCP(t *testing.T) {
	cfg := config.Default()
	cfg.Mode = "assist"
	cfg.Allow = []string{"mouse_move"}
	cfg.AllowedProcesses = []string{"mock.exe"}
	c, svc := connect(t, cfg)
	for _, ms := range []int{-1, 9, 5001} {
		call(t, c, "mouse_move", map[string]any{"x": 10, "y": 20, "duration_ms": ms}, false)
	}
	call(t, c, "mouse_move", map[string]any{"x": 10, "y": 20, "duration_ms": 300, "window_id": "another-window"}, false)
	call(t, c, "mouse_move", map[string]any{"x": 10, "y": 20, "duration_ms": 300, "window_id": "mock-window"}, true)
	actions := svc.Desktop.(*mock.Desktop).Actions
	if len(actions) != 1 || actions[0].Duration != 300*time.Millisecond || actions[0].WindowID != "mock-window" {
		t.Fatal(actions)
	}
}

const signedChromePath = "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
const signedChromeAlias = "/private/var/folders/test/X/com.google.Chrome.code_sign_clone/session/Google Chrome.app.bundle/Contents/MacOS/Google Chrome"

type signedAliasDesktop struct {
	launchDesktop
	verified bool
}

func (d *signedAliasDesktop) MatchesWindowProcess(ctx context.Context, w platform.Window, expected string) (bool, error) {
	return d.verified && w.ID == "chrome" && w.Process == signedChromeAlias && expected == signedChromePath, ctx.Err()
}
func (d *signedAliasDesktop) Windows(ctx context.Context) ([]platform.Window, error) {
	offscreen := false
	return []platform.Window{{ID: "chrome", Process: signedChromeAlias, OnScreen: &offscreen, Bounds: platform.Rect{Width: 640, Height: 480}}, {ID: "denied", Process: "/Applications/Denied.app/Contents/MacOS/Denied", Bounds: platform.Rect{Width: 100, Height: 100}}}, ctx.Err()
}
func TestMCPReportsSignedClonePermissionAndAcceptsLaunchConfirmation(t *testing.T) {
	for _, verified := range []bool{true, false} {
		t.Run(fmt.Sprint(verified), func(t *testing.T) {
			cfg := config.Default()
			cfg.Mode = "assist"
			cfg.Allow = []string{"app_launch"}
			cfg.AllowedProcesses = []string{signedChromePath}
			cfg.LocalApproval = true
			cfg.ConfirmEachAction = false
			d := &signedAliasDesktop{launchDesktop: launchDesktop{process: signedChromeAlias}, verified: verified}
			c, _ := connectDesktop(t, cfg, d)
			r := call(t, c, "window_list", map[string]any{}, true)
			var description struct {
				Windows []platform.Window `json:"windows"`
			}
			if err := json.Unmarshal([]byte(r.Content[0].(*mcp.TextContent).Text), &description); err != nil {
				t.Fatal(err)
			}
			if len(description.Windows) != 2 {
				t.Fatal("denied/offscreen windows were hidden", description)
			}
			chrome, denied := description.Windows[0], description.Windows[1]
			if chrome.Process != signedChromeAlias || chrome.OnScreen == nil || *chrome.OnScreen || chrome.ProcessAllowed == nil || *chrome.ProcessAllowed != verified {
				t.Fatal("identity and visibility mixed", chrome)
			}
			if verified && chrome.AllowedProcess != signedChromePath {
				t.Fatal(chrome)
			}
			if !verified && (chrome.IdentityError == "" || chrome.AllowedProcess != "") {
				t.Fatal(chrome)
			}
			if denied.ProcessAllowed == nil || *denied.ProcessAllowed || denied.IdentityError == "" {
				t.Fatal(denied)
			}
			r = call(t, c, "app_launch", map[string]any{"process": signedChromePath, "url": "https://www.youtube.com/"}, verified)
			if verified {
				var launch struct {
					Window platform.Window `json:"window"`
				}
				if err := json.Unmarshal([]byte(r.Content[0].(*mcp.TextContent).Text), &launch); err != nil {
					t.Fatal(err)
				}
				if launch.Window.Process != signedChromeAlias || launch.Window.AllowedProcess != signedChromePath || launch.Window.ProcessAllowed == nil || !*launch.Window.ProcessAllowed {
					t.Fatal("signed clone launch confirmation failed", launch)
				}
			}
		})
	}
}

func TestUnlimitedReadBudget(t *testing.T) {
	cfg := config.Default()
	cfg.MaxPerMinute = 0
	cfg.MaxPerTask = 0
	c, svc := connect(t, cfg)
	for i := 0; i < 150; i++ {
		call(t, c, "window_list", map[string]any{}, true)
	}
	if svc.total != 150 || len(svc.recent) != 0 {
		t.Fatal("unlimited reads must count without retaining timestamps")
	}
	call(t, c, "session_stop", map[string]any{}, true)
	call(t, c, "observation_start", map[string]any{"preview": false}, false)
}
func TestIndependentReadBudgets(t *testing.T) {
	for _, limits := range [][2]int{{0, 1}, {1, 0}} {
		cfg := config.Default()
		cfg.MaxPerMinute = limits[0]
		cfg.MaxPerTask = limits[1]
		c, _ := connect(t, cfg)
		call(t, c, "window_list", map[string]any{}, true)
		call(t, c, "window_list", map[string]any{}, false)
	}
}

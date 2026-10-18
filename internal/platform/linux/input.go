package linux

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"example.com/codex-vision/internal/input"
	"example.com/codex-vision/internal/platform"
	"example.com/codex-vision/internal/platform/command"
)

func (d *Desktop) CursorPosition(ctx context.Context) (platform.Point, error) {
	if err := d.unlocked(ctx); err != nil {
		return platform.Point{}, err
	}
	p, _, err := d.pointer(ctx)
	return p, err
}

func (d *Desktop) pointer(ctx context.Context) (platform.Point, string, error) {
	b, err := d.run(ctx, "xdotool", "getmouselocation", "--shell")
	if err != nil {
		return platform.Point{}, "", err
	}
	f := fields(b)
	x, ex := strconv.Atoi(f["X"])
	y, ey := strconv.Atoi(f["Y"])
	if ex != nil || ey != nil || f["WINDOW"] == "" {
		return platform.Point{}, "", fmt.Errorf("invalid X11 cursor state")
	}
	return platform.Point{X: x, Y: y}, f["WINDOW"], nil
}

func (d *Desktop) checkWindow(ctx context.Context, expected platform.Window) error {
	if err := d.unlocked(ctx); err != nil {
		return err
	}
	current, err := d.active(ctx)
	if err != nil {
		return err
	}
	if current.ID != expected.ID || current.Process != expected.Process || current.Bounds != expected.Bounds {
		return fmt.Errorf("foreground X11 window identity or geometry changed; stop input")
	}
	return nil
}

func (d *Desktop) checkPointer(ctx context.Context, w platform.Window, point *platform.Point) error {
	p, native, err := d.pointer(ctx)
	if err != nil {
		return err
	}
	d.mu.Lock()
	t, found := d.tokens[native]
	d.mu.Unlock()
	if !found || t.Opaque != w.ID || !w.Bounds.Contains(p.X, p.Y) || (point != nil && p != *point) {
		return fmt.Errorf("cursor must be above the expected foreground X11 window at the requested position")
	}
	return d.checkWindow(ctx, w)
}

func (d *Desktop) move(ctx context.Context, w platform.Window, x, y int) error {
	if !w.Bounds.Contains(x, y) {
		return fmt.Errorf("cursor target outside allowed foreground window")
	}
	if err := d.checkWindow(ctx, w); err != nil {
		return err
	}
	_, err := d.run(ctx, "xdotool", "mousemove", "--", strconv.Itoa(x), strconv.Itoa(y))
	return err
}

func (d *Desktop) moveOver(ctx context.Context, w platform.Window, x, y int, duration time.Duration) error {
	if duration == 0 {
		return d.move(ctx, w, x, y)
	}
	start, _, err := d.pointer(ctx)
	if err != nil {
		return err
	}
	steps := max(1, min(30, int(duration/(40*time.Millisecond))))
	for i := 1; i <= steps; i++ {
		timer := time.NewTimer(duration / time.Duration(steps))
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		if err := d.move(ctx, w, start.X+(x-start.X)*i/steps, start.Y+(y-start.Y)*i/steps); err != nil {
			return err
		}
	}
	return nil
}

var xKeys = map[string]string{
	"ENTER": "Return", "TAB": "Tab", "ESCAPE": "Escape", "SPACE": "space",
	"BACKSPACE": "BackSpace", "DELETE": "Delete", "INSERT": "Insert",
	"HOME": "Home", "END": "End", "PAGEUP": "Prior", "PAGEDOWN": "Next",
	"LEFT": "Left", "RIGHT": "Right", "UP": "Up", "DOWN": "Down",
	"CTRL": "Control_L", "SHIFT": "Shift_L", "ALT": "Alt_L", "OPTION": "Alt_L",
	"WIN": "Super_L", "CMD": "Super_L", "COMMAND": "Super_L",
}

func keysym(key string) string {
	key = strings.ToUpper(key)
	if k, ok := xKeys[key]; ok {
		return k
	}
	if len(key) == 1 {
		return strings.ToLower(key)
	}
	return key // Only validated F1..F12 reach this case.
}

func keySequence(keys []string) []string {
	modifiers, rest := []string{}, []string{}
	for _, key := range keys {
		k := keysym(key)
		switch k {
		case "Control_L", "Shift_L", "Alt_L", "Super_L":
			modifiers = append(modifiers, k)
		default:
			rest = append(rest, k)
		}
	}
	return append(modifiers, rest...)
}

func (d *Desktop) Execute(ctx context.Context, a platform.Action) (err error) {
	if err := d.unlocked(ctx); err != nil {
		return err
	}
	if a.Tool == "window_focus" {
		return d.focus(ctx, a.WindowID)
	}
	if !d.Supports(a.Tool) {
		return fmt.Errorf("unsupported Linux input capability")
	}
	w, err := d.active(ctx)
	if err != nil {
		return err
	}
	if a.WindowID != "" && a.WindowID != w.ID {
		return fmt.Errorf("target window is not foreground; stop input")
	}
	if err := input.Validate(a, w.Bounds); err != nil {
		return err
	}
	// Release any possibly posted down events even on command failure or stop.
	defer func() { err = errors.Join(err, d.Release()) }()
	switch a.Tool {
	case "mouse_move":
		return d.moveOver(ctx, w, a.X, a.Y, a.Duration)
	case "mouse_click", "mouse_drag":
		if err := d.move(ctx, w, a.X, a.Y); err != nil {
			return err
		}
		point := platform.Point{X: a.X, Y: a.Y}
		if err := d.checkPointer(ctx, w, &point); err != nil {
			return err
		}
		button := map[string]int{"left": 1, "middle": 2, "right": 3}[a.Button]
		d.mu.Lock()
		if d.heldButtons == nil {
			d.heldButtons = map[int]bool{}
		}
		d.heldButtons[button] = true
		d.mu.Unlock()
		if a.Tool == "mouse_drag" {
			if _, err := d.run(ctx, "xdotool", "mousedown", strconv.Itoa(button)); err != nil {
				return err
			}
			return d.moveOver(ctx, w, a.EndX, a.EndY, a.Duration)
		}
		for i := 0; i < a.Count; i++ {
			if err := d.checkPointer(ctx, w, &point); err != nil {
				return err
			}
			if _, err := d.run(ctx, "xdotool", "click", strconv.Itoa(button)); err != nil {
				return err
			}
		}
		d.mu.Lock()
		delete(d.heldButtons, button)
		d.mu.Unlock()
		return nil
	case "mouse_scroll":
		if a.Delta%120 != 0 {
			return fmt.Errorf("X11 scroll delta must be a multiple of 120")
		}
		if err := d.checkPointer(ctx, w, nil); err != nil {
			return err
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
		d.mu.Lock()
		if d.heldButtons == nil {
			d.heldButtons = map[int]bool{}
		}
		d.heldButtons[button] = true
		d.mu.Unlock()
		_, err := d.run(ctx, "xdotool", "click", "--repeat", strconv.Itoa(count), "--delay", "0", strconv.Itoa(button))
		if err == nil {
			d.mu.Lock()
			delete(d.heldButtons, button)
			d.mu.Unlock()
		}
		return err
	case "keyboard_press", "keyboard_hotkey":
		ks := keySequence(a.Keys)
		if err := d.checkWindow(ctx, w); err != nil {
			return err
		}
		d.mu.Lock()
		d.heldKeys = ks
		d.mu.Unlock()
		_, err := d.run(ctx, "xdotool", "key", "--delay", "0", strings.Join(ks, "+"))
		if err == nil {
			d.mu.Lock()
			d.heldKeys = nil
			d.mu.Unlock()
		}
		return err
	case "keyboard_type":
		runner, ok := d.runner.(command.InputRunner)
		if !ok {
			return fmt.Errorf("stdin-capable native runner required for text input")
		}
		if err := d.checkWindow(ctx, w); err != nil {
			return err
		}
		keys := []string{}
		seen := map[rune]bool{}
		for _, r := range a.Text {
			if !seen[r] {
				seen[r] = true
				k := fmt.Sprintf("U%04X", r)
				switch r {
				case '\n', '\r':
					k = "Return"
				case '\t':
					k = "Tab"
				case ' ':
					k = "space"
				}
				keys = append(keys, k)
			}
		}
		d.mu.Lock()
		d.heldKeys = keys
		d.mu.Unlock()
		// Text is not a command argument, log entry, clipboard value or temp file.
		_, err := runner.RunInput(ctx, d.paths["xdotool"], a.Text, "type", "--delay", "0", "--file", "-")
		if err == nil {
			d.mu.Lock()
			d.heldKeys = nil
			d.mu.Unlock()
		}
		return err
	}
	return fmt.Errorf("unsupported Linux action")
}

func (d *Desktop) focus(ctx context.Context, id string) error {
	d.mu.Lock()
	var target token
	for _, t := range d.tokens {
		if t.Opaque == id {
			target = t
			break
		}
	}
	d.mu.Unlock()
	if target.Native == "" {
		return fmt.Errorf("unknown window ID")
	}
	fresh, err := d.describe(ctx, target.Native)
	if err != nil || fresh.ID != id {
		return fmt.Errorf("stale window identity")
	}
	if err := d.unlocked(ctx); err != nil {
		return err
	}
	if _, err := d.run(ctx, "xdotool", "windowactivate", "--sync", target.Native); err != nil {
		return err
	}
	if err := d.checkWindow(ctx, fresh); err != nil {
		return fmt.Errorf("foreground not confirmed for X11 target: %w", err)
	}
	return nil
}

func (d *Desktop) Release() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.heldButtons) == 0 && len(d.heldKeys) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var errs []error
	for button := range d.heldButtons {
		if _, err := d.run(ctx, "xdotool", "mouseup", strconv.Itoa(button)); err != nil {
			errs = append(errs, err)
		} else {
			delete(d.heldButtons, button)
		}
	}
	if len(d.heldKeys) != 0 {
		args := append([]string{"keyup", "--delay", "0"}, d.heldKeys...)
		if _, err := d.run(ctx, "xdotool", args...); err != nil {
			errs = append(errs, err)
		} else {
			d.heldKeys = nil
		}
	}
	return errors.Join(errs...)
}

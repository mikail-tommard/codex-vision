package input

import (
	"example.com/codex-vision/internal/platform"
	"strings"
	"testing"
	"time"
)

func TestValidation(t *testing.T) {
	for _, k := range []string{"A", "0", "f12", "ctrl", "ENTER", "left"} {
		if _, e := Key(k); e != nil {
			t.Fatal(k, e)
		}
	}
	for _, ks := range [][]string{{}, {"ctrl", "CTRL"}, {"F13"}, {"cmd.exe"}} {
		if ValidateKeys(ks) == nil {
			t.Fatal(ks)
		}
	}
	for _, s := range []string{"", strings.Repeat("я", 4097), "a\x00b", string([]byte{255})} {
		if ValidateText(s) == nil {
			t.Fatal("invalid text accepted")
		}
	}
	if e := ValidateText("Привет 👋"); e != nil {
		t.Fatal(e)
	}
	b := platform.Rect{X: -100, Y: 0, Width: 200, Height: 100}
	for _, a := range []platform.Action{{Tool: "mouse_move", X: 100}, {Tool: "mouse_scroll", Delta: 0}, {Tool: "mouse_click", Button: "other", Count: 1}, {Tool: "mouse_drag", Button: "left"}, {Tool: "window_focus"}} {
		if Validate(a, b) == nil {
			t.Fatal(a)
		}
	}
}

func TestMoveDurationValidation(t *testing.T) {
	b := platform.Rect{Width: 100, Height: 100}
	for _, ms := range []int{0, 10, 300, 5000} {
		if err := Validate(platform.Action{Tool: "mouse_move", X: 5, Y: 5, Duration: time.Duration(ms) * time.Millisecond}, b); err != nil {
			t.Fatal(ms, err)
		}
	}
	for _, ms := range []int{-1, 1, 9, 5001} {
		if Validate(platform.Action{Tool: "mouse_move", X: 5, Y: 5, Duration: time.Duration(ms) * time.Millisecond}, b) == nil {
			t.Fatal("accepted duration", ms)
		}
	}
}

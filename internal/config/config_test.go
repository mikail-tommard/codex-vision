package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStrictConfig(t *testing.T) {
	for _, data := range []string{`{"tool_profile":"unknown"}`, `{"mode":"auto"}`, `{"mode":"assist"}`, `{"allow":["keyboard_type"]}`, `{"timeout_ms":0}`, `{"images":false,"unknown":1}`, `{} {}`, `null`} {
		t.Run(data, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "config.json")
			if e := os.WriteFile(p, []byte(data), 0600); e != nil {
				t.Fatal(e)
			}
			_, e := Load(p)
			if e == nil {
				t.Fatal("bad configuration accepted")
			}
		})
	}
	c, e := Load("")
	if e != nil || c.Mode != "observe" || c.ToolProfile != "compact" {
		t.Fatal(c, e)
	}
}

func TestInputAllowlistRequiresLocalApproval(t *testing.T) {
	c := Default()
	c.Mode = "assist"
	c.AllowedProcesses = []string{"/Test.app/Test"}
	c.Allow = []string{"mouse_click", "keyboard_type", "keyboard_hotkey", "app_launch"}
	if c.Validate() == nil {
		t.Fatal("input enabled without local approval opt-in")
	}
	c.LocalApproval = true
	if e := c.Validate(); e != nil {
		t.Fatal(e)
	}
}

func TestConfirmationDefaultsAndOptOut(t *testing.T) {
	c := Default()
	if !c.ConfirmEachAction {
		t.Fatal("new installs must preserve confirmation by default")
	}
	c.Mode = "assist"
	c.ConfirmEachAction = false
	c.Allow = []string{"keyboard_type"}
	c.AllowedProcesses = []string{"/Test.app"}
	if c.Validate() == nil {
		t.Fatal("no local input grant")
	}
	c.LocalApproval = true
	if e := c.Validate(); e != nil {
		t.Fatal(e)
	}
}

func TestUnlimitedLimits(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, []byte(`{"max_actions_per_minute":0,"max_actions_per_task":0}`), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p)
	if err != nil || c.MaxPerMinute != 0 || c.MaxPerTask != 0 {
		t.Fatal(c, err)
	}
	for _, limits := range [][2]int{{-1, 0}, {0, -1}, {601, 0}, {0, 10001}} {
		c.MaxPerMinute, c.MaxPerTask = limits[0], limits[1]
		if c.Validate() == nil {
			t.Fatal("invalid limits accepted", limits)
		}
	}
}

func TestCursorMode(t *testing.T) {
	c := Default()
	if c.CursorMode != "smooth" {
		t.Fatal("default cursor mode changed")
	}
	c.CursorMode = "fast"
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	c.CursorMode = "instant"
	if c.Validate() == nil {
		t.Fatal("invalid cursor mode accepted")
	}
}

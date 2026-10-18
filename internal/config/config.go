package config

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type Config struct {
	CursorMode        string   `json:"cursor_mode"`
	Mode              string   `json:"mode"`
	Allow             []string `json:"allow"`
	AllowedProcesses  []string `json:"allowed_processes"`
	TimeoutMS         int      `json:"timeout_ms"`
	MaxPerMinute      int      `json:"max_actions_per_minute"`
	MaxPerTask        int      `json:"max_actions_per_task"`
	StopFile          string   `json:"stop_file"`
	AuditFile         string   `json:"audit_file"`
	Images            bool     `json:"images"`
	LocalApproval     bool     `json:"local_approval"`
	ConfirmEachAction bool     `json:"confirm_each_action"`
}

func Default() Config {
	return Config{CursorMode: "smooth", Mode: "observe", Allow: []string{}, AllowedProcesses: []string{}, TimeoutMS: 5000, MaxPerMinute: 30, MaxPerTask: 100, StopFile: "codex-vision.stop", AuditFile: "codex-vision.audit.jsonl", Images: true, ConfirmEachAction: true}
}
func Load(path string) (Config, error) {
	c := Default()
	if path != "" {
		f, e := os.Open(path)
		if e != nil {
			return c, e
		}
		defer f.Close()
		info, e := f.Stat()
		if e != nil {
			return c, e
		}
		if info.Size() > 65536 {
			return c, fmt.Errorf("configuration exceeds 64 KiB")
		}
		dec := json.NewDecoder(io.LimitReader(f, 65537))
		dec.DisallowUnknownFields()
		target := &c
		if e = dec.Decode(&target); e != nil {
			return c, e
		}
		if target == nil {
			return c, fmt.Errorf("configuration must be a JSON object")
		}
		c = *target
		var extra any
		if e = dec.Decode(&extra); e != io.EOF {
			return c, fmt.Errorf("configuration must contain exactly one JSON object")
		}
	}
	return c, c.Validate()
}
func (c Config) Validate() error {
	if c.CursorMode != "" && c.CursorMode != "smooth" && c.CursorMode != "fast" && c.CursorMode != "rapid" {
		return fmt.Errorf("cursor_mode must be rapid, fast or smooth")
	}
	if c.Mode != "observe" && c.Mode != "assist" {
		return fmt.Errorf("mode must be observe or assist; auto is disabled")
	}
	if c.TimeoutMS < 100 || c.TimeoutMS > 30000 {
		return fmt.Errorf("timeout_ms must be 100..30000")
	}
	if c.MaxPerMinute < 0 || c.MaxPerMinute > 600 || c.MaxPerTask < 0 || c.MaxPerTask > 10000 {
		return fmt.Errorf("max_actions_per_minute must be 0..600 and max_actions_per_task 0..10000; 0 disables that limit")
	}
	if c.StopFile == "" || c.AuditFile == "" {
		return fmt.Errorf("stop_file and audit_file are required")
	}
	a, _ := filepath.Abs(c.StopFile)
	b, _ := filepath.Abs(c.AuditFile)
	if a == b {
		return fmt.Errorf("stop_file and audit_file must differ")
	}
	for _, v := range c.Allow {
		switch v {
		case "mouse_move", "mouse_scroll", "window_focus":
		case "mouse_click", "mouse_drag", "keyboard_type", "keyboard_press", "keyboard_hotkey", "app_launch", "ui_fill":
			if !c.LocalApproval {
				return fmt.Errorf("%s requires local_approval=true (and a supported local UI when confirm_each_action=true)", v)
			}
		default:
			return fmt.Errorf("%s requires unavailable local approval; cannot allowlist", v)
		}
	}
	if c.Mode == "assist" && len(c.AllowedProcesses) == 0 {
		return fmt.Errorf("assist requires allowed_processes (exact full executable paths)")
	}
	for _, p := range c.AllowedProcesses {
		if p == "" {
			return fmt.Errorf("empty process path")
		}
	}
	return nil
}

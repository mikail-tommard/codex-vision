package main

import (
	"testing"

	"example.com/codex-vision/internal/platform"
)

func TestWaylandPortalRequiresExplicitLocalOptIn(t *testing.T) {
	t.Setenv("CODEX_VISION_WAYLAND_STREAM", "")
	t.Setenv("XDG_SESSION_TYPE", "wayland")
	t.Setenv("WAYLAND_DISPLAY", "wayland-test")
	t.Setenv("CODEX_VISION_WAYLAND_PORTAL", "")
	if _, err := newNative(); err == nil {
		t.Fatal("Wayland enabled without opt-in")
	}
	t.Setenv("CODEX_VISION_WAYLAND_PORTAL", "1")
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "tcp:host=remote,port=123")
	if _, err := newNative(); err == nil {
		t.Fatal("remote session bus accepted")
	}
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path=/run/user/1000/bus")
	d, err := newNative()
	if err != nil {
		t.Fatal(err)
	}
	defer d.Release()
	if _, ok := d.(platform.ConsentedScreenshot); !ok {
		t.Fatal("portal backend not selected")
	}
}

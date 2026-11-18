package wayland

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

func TestPortalLocalBusAndNoFollow(t *testing.T) {
	for _, address := range []string{"tcp:host=remote,port=123", "autolaunch:", "unix:path=/tmp/bus;tcp:host=remote", ""} {
		if localBus(address) {
			t.Fatal("remote/autolaunch bus accepted")
		}
	}
	if !localBus("unix:path=/run/user/1000/bus") {
		t.Fatal("local bus refused")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "file")
	link := filepath.Join(dir, "link")
	if err := os.WriteFile(target, []byte("image"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if f, err := openPortalFile(link); err == nil {
		f.Close()
		t.Fatal("symlink image followed")
	}
}

func TestPortalSignalOwnerAndRequestBinding(t *testing.T) {
	r := &nativeRequest{path: "/request/ours", owner: ":1.5", signals: make(chan *dbus.Signal, 4)}
	r.signals <- &dbus.Signal{Sender: ":1.attacker", Path: r.path, Name: requestInterface + ".Response", Body: []any{uint32(0), map[string]dbus.Variant{"uri": dbus.MakeVariant("file:///private")}}}
	r.signals <- &dbus.Signal{Sender: r.owner, Path: "/request/other", Name: requestInterface + ".Response", Body: []any{uint32(1), map[string]dbus.Variant{}}}
	r.signals <- &dbus.Signal{Sender: r.owner, Path: r.path, Name: requestInterface + ".Response", Body: []any{uint32(0), map[string]dbus.Variant{"uri": dbus.MakeVariant("file:///approved")}}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if uri, err := r.Wait(ctx); err != nil || uri != "file:///approved" {
		t.Fatal(uri, err)
	}
}

func TestCastPipelineUsesOnlyPortalFDStableSerialAndBoundedQueue(t *testing.T) {
	args := castPipeline(123456)
	joined := map[string]bool{}
	for _, arg := range args {
		joined[arg] = true
	}
	for _, arg := range []string{"fd=3", "target-object=123456", "dont-reconnect=true", "max-size-buffers=1", "max-rate=2", "width=1280"} {
		if arg == "width=1280" {
			arg = "video/x-raw,width=1280,pixel-aspect-ratio=1/1"
		}
		if !joined[arg] {
			t.Fatal("missing capture guard", arg)
		}
	}
	if reflect.DeepEqual(castPipeline(123456), castPipeline(1)) {
		t.Fatal("serial not bound")
	}
}

package wayland

import (
	"context"
	"fmt"
	"image"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"example.com/codex-vision/internal/platform"
)

type streamingDesktop struct {
	*Desktop
	address   string
	gstreamer string
}

// NewStreaming is separate opt-in. It requires installed GStreamer/PipeWire
// plugins and does not install software or restore previous portal permissions.
func NewStreaming() (platform.Desktop, error) {
	d, err := New()
	if err != nil {
		return nil, err
	}
	path, err := exec.LookPath("gst-launch-1.0")
	if err != nil {
		d.Release()
		return nil, fmt.Errorf("Wayland streaming requires installed gst-launch-1.0 with pipewiresrc, videorate, videoscale, videoconvert and pngenc plugins")
	}
	path, err = filepath.Abs(path)
	if err != nil {
		d.Release()
		return nil, err
	}
	return &streamingDesktop{Desktop: d, address: os.Getenv("DBUS_SESSION_BUS_ADDRESS"), gstreamer: path}, nil
}

func castPipeline(serial uint64) []string {
	return []string{"-q", "pipewiresrc", "fd=3", "target-object=" + strconv.FormatUint(serial, 10), "dont-reconnect=true", "max-buffers=4",
		"!", "queue", "max-size-buffers=1", "max-size-bytes=0", "max-size-time=0", "leaky=downstream",
		"!", "videorate", "drop-only=true", "max-rate=2", "!", "videoscale", "!", "video/x-raw,width=1280,pixel-aspect-ratio=1/1",
		"!", "videoconvert", "!", "video/x-raw,format=RGBA", "!", "pngenc", "compression-level=1", "snapshot=false", "!", "fdsink", "fd=1", "sync=false"}
}

type pipeWireSource struct {
	session *castSession
	command *exec.Cmd
	reader  io.ReadCloser
	cancel  context.CancelFunc
	once    sync.Once
	waited  chan struct{}
	life    context.Context
}

func (d *streamingDesktop) StartScreenCast(ctx context.Context) (platform.ScreenCastSource, error) {
	if d.life.Err() != nil || ctx.Err() != nil {
		return nil, fmt.Errorf("portal capture stopped")
	}
	session, err := connectCast(ctx, d.address, d.life)
	if err != nil {
		return nil, err
	}
	remote, serial, err := session.open(ctx)
	if err != nil {
		session.Close()
		return nil, err
	}
	defer remote.Close() // The child inherits a duplicate as fd 3, never a node on the system remote.
	life, cancel := context.WithCancel(session.ctx)
	command := exec.CommandContext(life, d.gstreamer, castPipeline(serial)...)
	command.ExtraFiles = []*os.File{remote}
	command.Stderr = io.Discard // Plugin diagnostics can contain private source names.
	command.WaitDelay = 250 * time.Millisecond
	reader, err := command.StdoutPipe()
	if err != nil {
		cancel()
		session.Close()
		return nil, fmt.Errorf("cannot open PipeWire frame reader")
	}
	if err := command.Start(); err != nil {
		reader.Close()
		cancel()
		session.Close()
		return nil, fmt.Errorf("cannot start local PipeWire frame consumer")
	}
	source := &pipeWireSource{session: session, command: command, reader: reader, cancel: cancel, waited: make(chan struct{}), life: life}
	go func() { _ = command.Wait(); cancel(); close(source.waited) }()
	return source, nil
}

func (s *pipeWireSource) Next(ctx context.Context) (image.Image, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	stop := context.AfterFunc(ctx, func() { _ = s.Close() })
	defer stop()
	im, err := readStreamPNG(s.reader)
	if err != nil {
		return nil, fmt.Errorf("PipeWire frame stream ended or is invalid; check local GStreamer plugins and portal consent")
	}
	return im, ctx.Err()
}

func (s *pipeWireSource) Done() <-chan struct{} { return s.life.Done() }

func (s *pipeWireSource) Close() error {
	s.once.Do(func() { s.cancel(); _ = s.reader.Close(); _ = s.session.Close() })
	select {
	case <-s.waited:
		return nil
	case <-time.After(time.Second):
		return fmt.Errorf("PipeWire consumer cleanup still pending")
	}
}

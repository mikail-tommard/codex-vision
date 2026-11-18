// Package wayland implements opt-in, consent-based single screenshots.
// Continuous PipeWire capture and RemoteDesktop input are not implemented.
package wayland

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"example.com/codex-vision/internal/platform"
	"example.com/codex-vision/internal/vision"
)

var ErrUnsupported = errors.New("Wayland portal backend supports only explicit screen_capture; no desktop coordinates, windows, continuous observation or input")
var ErrDenied = platform.ErrCaptureDenied

func responseURI(code uint32, value any) (string, error) {
	if code == 1 {
		return "", ErrDenied
	}
	if code != 0 {
		return "", fmt.Errorf("screenshot portal did not complete the request")
	}
	uri, ok := value.(string)
	if !ok || uri == "" || len(uri) > 8192 {
		return "", fmt.Errorf("screenshot portal returned no bounded image URI")
	}
	return uri, nil
}

type screenshotRequest interface {
	Wait(context.Context) (string, error)
	Close() error
}
type screenshotPortal interface {
	Begin(context.Context) (screenshotRequest, error)
}

type Desktop struct {
	portal screenshotPortal
	open   func(string) (*os.File, error)
	life   context.Context
	stop   context.CancelFunc
}

func newDesktop(p screenshotPortal, open func(string) (*os.File, error)) *Desktop {
	ctx, cancel := context.WithCancel(context.Background())
	return &Desktop{portal: p, open: open, life: ctx, stop: cancel}
}
func (*Desktop) Name() string { return "linux-wayland-portal" }
func (*Desktop) CoordinateSpace() string {
	return "portal image pixels only; desktop mapping unavailable"
}
func (*Desktop) Supports(string) bool                                 { return false }
func (*Desktop) Monitors(context.Context) ([]platform.Monitor, error) { return nil, ErrUnsupported }
func (*Desktop) Windows(context.Context) ([]platform.Window, error)   { return nil, ErrUnsupported }
func (*Desktop) Active(context.Context) (platform.Window, error) {
	return platform.Window{}, ErrUnsupported
}
func (*Desktop) Capture(context.Context, platform.Rect) (image.Image, error) {
	return nil, ErrUnsupported
}
func (*Desktop) Execute(context.Context, platform.Action) error { return ErrUnsupported }
func (d *Desktop) StopCapture()                                 { d.stop() }
func (d *Desktop) Release() error                               { d.stop(); return nil }

func (d *Desktop) Screenshot(ctx context.Context) (image.Image, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(d.life, cancel)
	defer stop()
	if d.life.Err() != nil {
		return nil, fmt.Errorf("portal capture stopped")
	}
	request, err := d.portal.Begin(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}
	defer request.Close()
	uri, err := request.Wait(ctx)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return decodeScreenshot(ctx, uri, d.open)
}

func decodeScreenshot(ctx context.Context, rawURI string, open func(string) (*os.File, error)) (image.Image, error) {
	if len(rawURI) > 8192 {
		return nil, fmt.Errorf("portal image URI exceeds budget")
	}
	u, err := url.Parse(rawURI)
	if err != nil || u.Scheme != "file" || u.Host != "" || u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.Fragment != "" || !filepath.IsAbs(u.Path) || strings.ContainsRune(u.Path, 0) {
		return nil, fmt.Errorf("portal returned an invalid local image URI")
	}
	f, err := open(u.Path)
	if err != nil {
		return nil, fmt.Errorf("cannot open portal image")
	} // Never expose its private path.
	defer f.Close()
	info, err := f.Stat()
	const maxBytes = 64 << 20
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxBytes {
		return nil, fmt.Errorf("portal image must be a bounded regular file")
	}
	data, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil || len(data) > maxBytes {
		return nil, fmt.Errorf("portal image exceeds read budget")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("invalid portal image format")
	}
	if err := vision.Validate(image.Rect(0, 0, cfg.Width, cfg.Height)); err != nil {
		return nil, err
	}
	im, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("invalid portal image")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return im, nil
}

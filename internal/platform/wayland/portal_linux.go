package wayland

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
	"golang.org/x/sys/unix"
)

const portalName = "org.freedesktop.portal.Desktop"
const portalPath = dbus.ObjectPath("/org/freedesktop/portal/desktop")
const requestInterface = "org.freedesktop.portal.Request"

type nativePortal struct{ address string }
type nativeRequest struct {
	conn           *dbus.Conn
	path           dbus.ObjectPath
	signals        chan *dbus.Signal
	cancel         context.CancelFunc
	once           sync.Once
	owner          string
	responded      bool
	context        context.Context
	keepConnection bool
}

// New does not capture or prompt. Each explicit screen_capture opens its own
// bounded portal request. Remote bus transports and bus autolaunch are refused.
func New() (*Desktop, error) {
	address := os.Getenv("DBUS_SESSION_BUS_ADDRESS")
	if !localBus(address) {
		return nil, fmt.Errorf("Wayland portal requires an existing local Unix DBUS_SESSION_BUS_ADDRESS")
	}
	return newDesktop(nativePortal{address}, openPortalFile), nil
}

func localBus(address string) bool {
	return !strings.Contains(address, ";") && (strings.HasPrefix(address, "unix:path=") || strings.HasPrefix(address, "unix:abstract="))
}

func openPortalFile(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), "portal-screenshot"), nil
}

func (p nativePortal) Begin(ctx context.Context) (screenshotRequest, error) {
	ctx, limitCancel := context.WithTimeout(ctx, 30*time.Second)
	// Keep the authenticated private connection alive long enough to send
	// Request.Close after caller cancellation. Connection setup itself is cancelled.
	connectionContext, connectionCancel := context.WithCancel(context.WithoutCancel(ctx))
	stopSetup := context.AfterFunc(ctx, connectionCancel)
	conn, err := dbus.Connect(p.address, dbus.WithContext(connectionContext))
	setupStopped := stopSetup()
	cancel := func() { limitCancel(); connectionCancel() }
	if err != nil {
		cancel()
		return nil, fmt.Errorf("cannot connect to local screenshot portal bus")
	}
	if !setupStopped || ctx.Err() != nil {
		cancel()
		_ = conn.Close()
		return nil, ctx.Err()
	}
	r := &nativeRequest{conn: conn, cancel: cancel, signals: make(chan *dbus.Signal, 8), context: ctx}
	transferred := false
	defer func() {
		if !transferred {
			_ = r.Close()
		}
	}()
	var version dbus.Variant
	err = conn.Object(portalName, portalPath).CallWithContext(ctx, "org.freedesktop.DBus.Properties.Get", 0, "org.freedesktop.portal.Screenshot", "version").Store(&version)
	v, ok := version.Value().(uint32)
	if err != nil || !ok || v < 2 {
		return nil, fmt.Errorf("Screenshot portal version 2 or newer required for interactive consent")
	}
	if err := conn.BusObject().CallWithContext(ctx, "org.freedesktop.DBus.GetNameOwner", 0, portalName).Store(&r.owner); err != nil || !strings.HasPrefix(r.owner, ":") {
		return nil, fmt.Errorf("cannot verify screenshot portal owner")
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return nil, err
	}
	token := "codexvision_" + hex.EncodeToString(random[:])
	sender := strings.ReplaceAll(strings.TrimPrefix(conn.Names()[0], ":"), ".", "_")
	r.path = dbus.ObjectPath("/org/freedesktop/portal/desktop/request/" + sender + "/" + token)
	conn.Signal(r.signals)
	err = conn.AddMatchSignalContext(ctx, dbus.WithMatchSender(portalName), dbus.WithMatchObjectPath(r.path), dbus.WithMatchInterface(requestInterface), dbus.WithMatchMember("Response"))
	if err != nil {
		return nil, fmt.Errorf("cannot subscribe to screenshot portal response")
	}
	options := map[string]dbus.Variant{"handle_token": dbus.MakeVariant(token), "interactive": dbus.MakeVariant(true), "modal": dbus.MakeVariant(true)}
	var handle dbus.ObjectPath
	err = conn.Object(portalName, portalPath).CallWithContext(ctx, "org.freedesktop.portal.Screenshot.Screenshot", 0, "", options).Store(&handle)
	if err != nil {
		return nil, fmt.Errorf("screenshot portal request failed")
	}
	if handle != r.path {
		// Fail closed on an old/unexpected protocol rather than miss an early
		// response or accept a signal for another request.
		if handle.IsValid() && strings.HasPrefix(string(handle), "/org/freedesktop/portal/desktop/request/") {
			r.path = handle
		}
		return nil, fmt.Errorf("screenshot portal returned an unexpected request handle")
	}
	transferred = true
	return r, nil
}

func (r *nativeRequest) Wait(ctx context.Context) (string, error) {
	values, err := r.waitResult(ctx)
	if err != nil {
		return "", err
	}
	var uri any
	if value, exists := values["uri"]; exists {
		uri = value.Value()
	}
	return responseURI(0, uri)
}

func (r *nativeRequest) waitResult(ctx context.Context) (map[string]dbus.Variant, error) {
	var requestDone <-chan struct{}
	if r.context != nil {
		requestDone = r.context.Done()
	}
	for {
		select {
		case <-requestDone:
			return nil, r.context.Err()
		case <-ctx.Done():
			return nil, ctx.Err()
		case signal, ok := <-r.signals:
			if !ok {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				return nil, fmt.Errorf("portal connection closed")
			}
			if signal == nil || signal.Sender != r.owner || signal.Path != r.path || signal.Name != requestInterface+".Response" {
				continue
			}
			r.responded = true
			if len(signal.Body) != 2 {
				return nil, fmt.Errorf("invalid portal response")
			}
			code, ok := signal.Body[0].(uint32)
			if !ok {
				return nil, fmt.Errorf("invalid portal status")
			}
			values, ok := signal.Body[1].(map[string]dbus.Variant)
			if !ok {
				return nil, fmt.Errorf("invalid portal results")
			}
			if code == 1 {
				return nil, ErrDenied
			}
			if code != 0 {
				return nil, fmt.Errorf("portal request failed")
			}
			return values, nil
		}
	}
}

func (r *nativeRequest) Close() error {
	r.once.Do(func() {
		if !r.responded && r.path != "" && r.conn.Connected() {
			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			_ = r.conn.Object(portalName, r.path).CallWithContext(ctx, requestInterface+".Close", 0).Err
			cancel()
		}
		r.conn.RemoveSignal(r.signals)
		r.cancel()
		if !r.keepConnection {
			_ = r.conn.Close()
		} // Shared only within one bounded cast session.
	})
	return nil
}

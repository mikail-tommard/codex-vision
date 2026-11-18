package wayland

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"example.com/codex-vision/internal/platform"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
	"golang.org/x/sys/unix"
)

const castInterface = "org.freedesktop.portal.ScreenCast"
const sessionInterface = "org.freedesktop.portal.Session"

type castSession struct {
	conn             *dbus.Conn
	owner            string
	path             dbus.ObjectPath
	signals          chan *dbus.Signal
	ctx              context.Context
	cancel           context.CancelFunc
	connectionCancel context.CancelFunc
	unlink           func() bool
	once             sync.Once
}

func portalToken() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return "codexvision_" + hex.EncodeToString(value[:]), nil
}

func connectCast(ctx context.Context, address string, desktopLife context.Context) (*castSession, error) {
	life, cancel := context.WithCancel(ctx)
	unlink := context.AfterFunc(desktopLife, cancel)
	// Keep the bus alive for explicit Request/Session.Close during cancellation.
	connectionContext, connectionCancel := context.WithCancel(context.WithoutCancel(life))
	stopSetup := context.AfterFunc(life, connectionCancel)
	conn, err := dbus.Connect(address, dbus.WithContext(connectionContext))
	setupStopped := stopSetup()
	if err != nil || !setupStopped || life.Err() != nil {
		unlink()
		cancel()
		connectionCancel()
		if conn != nil {
			_ = conn.Close()
		}
		return nil, fmt.Errorf("cannot connect to local ScreenCast portal bus")
	}
	s := &castSession{conn: conn, ctx: life, cancel: cancel, connectionCancel: connectionCancel, unlink: unlink, signals: make(chan *dbus.Signal, 16)}
	return s, nil
}

func (s *castSession) property(ctx context.Context, name string) (dbus.Variant, error) {
	var value dbus.Variant
	err := s.conn.Object(s.owner, portalPath).CallWithContext(ctx, "org.freedesktop.DBus.Properties.Get", 0, castInterface, name).Store(&value)
	return value, err
}

func (s *castSession) request(ctx context.Context, method string, options map[string]dbus.Variant, prefix ...any) (map[string]dbus.Variant, error) {
	token, err := portalToken()
	if err != nil {
		return nil, err
	}
	sender := strings.ReplaceAll(strings.TrimPrefix(s.conn.Names()[0], ":"), ".", "_")
	r := &nativeRequest{conn: s.conn, owner: s.owner, path: dbus.ObjectPath("/org/freedesktop/portal/desktop/request/" + sender + "/" + token), signals: make(chan *dbus.Signal, 8), context: ctx, cancel: func() {}, keepConnection: true}
	s.conn.Signal(r.signals)
	defer r.Close()
	if err := s.conn.AddMatchSignalContext(ctx, dbus.WithMatchSender(s.owner), dbus.WithMatchObjectPath(r.path), dbus.WithMatchInterface(requestInterface), dbus.WithMatchMember("Response")); err != nil {
		return nil, fmt.Errorf("cannot subscribe to ScreenCast request")
	}
	options["handle_token"] = dbus.MakeVariant(token)
	args := append(prefix, options)
	var handle dbus.ObjectPath
	if err := s.conn.Object(s.owner, portalPath).CallWithContext(ctx, castInterface+"."+method, 0, args...).Store(&handle); err != nil {
		return nil, fmt.Errorf("ScreenCast portal request failed")
	}
	if handle != r.path {
		return nil, fmt.Errorf("ScreenCast portal returned an unexpected request handle")
	}
	return r.waitResult(ctx)
}

func (s *castSession) open(ctx context.Context) (_ *os.File, _ uint64, finalErr error) {
	setup, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer func() {
		if finalErr != nil && setup.Err() != nil {
			finalErr = setup.Err()
		}
		cancel()
	}()
	// Activate the portal, then pin every call and signal to its unique owner.
	var ignored dbus.Variant
	if err := s.conn.Object(portalName, portalPath).CallWithContext(setup, "org.freedesktop.DBus.Properties.Get", 0, castInterface, "version").Store(&ignored); err != nil {
		return nil, 0, fmt.Errorf("ScreenCast portal unavailable")
	}
	if err := s.conn.BusObject().CallWithContext(setup, "org.freedesktop.DBus.GetNameOwner", 0, portalName).Store(&s.owner); err != nil || !strings.HasPrefix(s.owner, ":") {
		return nil, 0, fmt.Errorf("cannot verify ScreenCast portal owner")
	}
	version, err := s.property(setup, "version")
	v, ok := version.Value().(uint32)
	if err != nil || !ok || v < 6 {
		return nil, 0, fmt.Errorf("%w: ScreenCast portal v6 required for non-reused PipeWire serial targeting", platform.ErrCaptureUnsupported)
	}
	types, err := s.property(setup, "AvailableSourceTypes")
	if err != nil {
		return nil, 0, fmt.Errorf("ScreenCast source capabilities unavailable")
	}
	mask, ok := types.Value().(uint32)
	if !ok || mask&3 == 0 {
		return nil, 0, fmt.Errorf("ScreenCast has no monitor/window source")
	}
	cursors, err := s.property(setup, "AvailableCursorModes")
	if err != nil {
		return nil, 0, fmt.Errorf("ScreenCast cursor capabilities unavailable")
	}
	modes, ok := cursors.Value().(uint32)
	if !ok || modes&1 == 0 {
		return nil, 0, fmt.Errorf("ScreenCast hidden cursor mode unavailable")
	}
	token, err := portalToken()
	if err != nil {
		return nil, 0, err
	}
	sender := strings.ReplaceAll(strings.TrimPrefix(s.conn.Names()[0], ":"), ".", "_")
	s.path = dbus.ObjectPath("/org/freedesktop/portal/desktop/session/" + sender + "/" + token)
	s.conn.Signal(s.signals)
	if err := s.conn.AddMatchSignalContext(setup, dbus.WithMatchSender(s.owner), dbus.WithMatchObjectPath(s.path), dbus.WithMatchInterface(sessionInterface), dbus.WithMatchMember("Closed")); err != nil {
		return nil, 0, fmt.Errorf("cannot watch ScreenCast session closure")
	}
	if err := s.conn.AddMatchSignalContext(setup, dbus.WithMatchSender("org.freedesktop.DBus"), dbus.WithMatchInterface("org.freedesktop.DBus"), dbus.WithMatchMember("NameOwnerChanged"), dbus.WithMatchArg(0, portalName)); err != nil {
		return nil, 0, fmt.Errorf("cannot watch ScreenCast owner changes")
	}
	go s.watch()
	created, err := s.request(setup, "CreateSession", map[string]dbus.Variant{"session_handle_token": dbus.MakeVariant(token)})
	if err != nil {
		return nil, 0, err
	}
	handle, ok := created["session_handle"].Value().(string)
	if !ok || handle != string(s.path) {
		return nil, 0, fmt.Errorf("ScreenCast portal returned an unexpected session")
	}
	_, err = s.request(setup, "SelectSources", map[string]dbus.Variant{"types": dbus.MakeVariant(mask & 3), "multiple": dbus.MakeVariant(false), "cursor_mode": dbus.MakeVariant(uint32(1)), "persist_mode": dbus.MakeVariant(uint32(0))}, s.path)
	if err != nil {
		return nil, 0, err
	}
	started, err := s.request(setup, "Start", map[string]dbus.Variant{}, s.path, "")
	if err != nil {
		return nil, 0, err
	}
	var streams []struct {
		Node       uint32
		Properties map[string]dbus.Variant
	}
	value, exists := started["streams"]
	if !exists || value.Store(&streams) != nil || len(streams) != 1 {
		return nil, 0, fmt.Errorf("ScreenCast requires exactly one consented stream")
	}
	serial, ok := streams[0].Properties["pipewire-serial"].Value().(uint64)
	if !ok || serial == 0 {
		return nil, 0, fmt.Errorf("ScreenCast stream has no stable PipeWire serial")
	}
	if value, exists := streams[0].Properties["source_type"]; exists {
		kind, ok := value.Value().(uint32)
		if !ok || (kind != 1 && kind != 2) || kind&mask == 0 {
			return nil, 0, fmt.Errorf("ScreenCast returned an unexpected source type")
		}
	}
	var remote dbus.UnixFD
	if err := s.conn.Object(s.owner, portalPath).CallWithContext(setup, castInterface+".OpenPipeWireRemote", 0, s.path, map[string]dbus.Variant{}).Store(&remote); err != nil {
		return nil, 0, fmt.Errorf("cannot open consented PipeWire remote")
	}
	fd := int(remote)
	unix.CloseOnExec(fd)
	f := os.NewFile(uintptr(fd), "portal-pipewire")
	if f == nil {
		return nil, 0, fmt.Errorf("invalid PipeWire descriptor")
	}
	if _, err := unix.GetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_TYPE); err != nil {
		f.Close()
		return nil, 0, fmt.Errorf("PipeWire descriptor is not a socket")
	}
	if s.ctx.Err() != nil {
		f.Close()
		return nil, 0, s.ctx.Err()
	}
	return f, serial, nil
}

func (s *castSession) watch() {
	for {
		select {
		case <-s.ctx.Done():
			_ = s.Close()
			return
		case signal, ok := <-s.signals:
			if !ok {
				_ = s.Close()
				return
			}
			if signal == nil {
				continue
			}
			closed := signal.Sender == s.owner && signal.Path == s.path && signal.Name == sessionInterface+".Closed"
			ownerLost := signal.Sender == "org.freedesktop.DBus" && signal.Name == "org.freedesktop.DBus.NameOwnerChanged" && len(signal.Body) == 3 && signal.Body[0] == portalName && signal.Body[1] == s.owner && signal.Body[2] != s.owner
			if closed || ownerLost {
				_ = s.Close()
				return
			}
		}
	}
}

func (s *castSession) Close() error {
	s.once.Do(func() {
		s.cancel()
		s.unlink()
		if s.path != "" && s.owner != "" && s.conn.Connected() {
			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			_ = s.conn.Object(s.owner, s.path).CallWithContext(ctx, sessionInterface+".Close", 0).Err
			cancel()
		}
		s.conn.RemoveSignal(s.signals)
		s.connectionCancel()
		_ = s.conn.Close()
	})
	return nil
}
